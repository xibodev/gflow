package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/history"
)

type fakeCapabilities struct {
	mu           sync.Mutex
	imageRequest application.ImageRequest
	videoRequest application.VideoRequest
	jobs         map[string]application.VideoJob
	jobCount     int
	closed       bool
}

func newFakeCapabilities() *fakeCapabilities {
	return &fakeCapabilities{
		jobs: make(map[string]application.VideoJob),
	}
}

func (f *fakeCapabilities) GenerateImage(_ context.Context, request application.ImageRequest) (application.ImageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageRequest = request
	return application.ImageOutput{FilePath: "generated.png", Files: []string{"generated.png"}}, nil
}

func (f *fakeCapabilities) EnqueueVideo(_ context.Context, request application.VideoRequest) (application.VideoJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.videoRequest = request
	f.jobCount++
	job := application.VideoJob{
		ID:         "job-video-1",
		Status:     "queued",
		Prompt:     request.Prompt,
		Aspect:     request.Aspect,
		Duration:   request.Duration,
		Resolution: "720p",
		HasAudio:   true,
		CreatedAt:  time.Now(),
	}
	f.jobs[job.ID] = job
	return job, nil
}

func (f *fakeCapabilities) VideoJob(id string) (application.VideoJob, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.jobs[id]
	if ok {
		// Advance status to completed on poll
		job.Status = "completed"
		job.FilePath = "completed_video.mp4"
		f.jobs[id] = job
	}
	return job, ok
}

func (f *fakeCapabilities) UpsampleVideo(context.Context, application.UpsampleRequest) (application.UpsampleOutput, error) {
	return application.UpsampleOutput{Files: []string{"upsampled.mp4"}}, nil
}

func (f *fakeCapabilities) Status(context.Context) (application.StatusOutput, error) {
	return application.StatusOutput{Data: map[string]any{"status": "healthy"}}, nil
}

func (f *fakeCapabilities) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func call(t *testing.T, s *Server, method string, id any, params any) map[string]any {
	t.Helper()
	var rawParams json.RawMessage
	if params != nil {
		b, _ := json.Marshal(params)
		rawParams = b
	}
	var rawID json.RawMessage
	if id != nil {
		b, _ := json.Marshal(id)
		rawID = b
	}
	resp := s.handleRequest(context.Background(), &Request{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams})
	if resp == nil {
		return nil
	}
	b, _ := json.Marshal(resp)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestInitializeAndToolsList(t *testing.T) {
	s := NewServerWithCapabilities(newFakeCapabilities())
	init := call(t, s, "initialize", 1, map[string]any{})
	if init["result"] == nil {
		t.Fatalf("initialize failed: %v", init)
	}
	list := call(t, s, "tools/list", 2, nil)
	res := list["result"].(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("want 5 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"generate_flow_image", "generate_flow_video", "upsample_flow_video", "get_flow_status", "get_flow_history"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}

func TestNewServerWithCapabilitiesDelegatesGeneration(t *testing.T) {
	capabilities := newFakeCapabilities()
	s := NewServerWithCapabilities(capabilities)
	out := call(t, s, "tools/call", 20, map[string]any{
		"name": "generate_flow_image",
		"arguments": map[string]any{
			"prompt": "draw an octopus",
			"count":  2,
		},
	})
	res := out["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("unexpected error response: %v", out)
	}
	if capabilities.imageRequest.Prompt != "draw an octopus" || capabilities.imageRequest.Count != 2 {
		t.Fatalf("delegated request = %+v", capabilities.imageRequest)
	}
}

func TestUnknownToolIsErrorNotProtocolError(t *testing.T) {
	s := NewServerWithCapabilities(newFakeCapabilities())
	out := call(t, s, "tools/call", 3, map[string]any{"name": "nope", "arguments": map[string]any{}})
	if out["error"] != nil {
		t.Fatalf("unknown tool must be tool-error result, got protocol error")
	}
	res := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("want isError result: %v", out)
	}
}

func TestStatusUsesCapabilities(t *testing.T) {
	s := NewServerWithCapabilities(newFakeCapabilities())
	out := call(t, s, "tools/call", 4, map[string]any{"name": "get_flow_status", "arguments": map[string]any{}})
	res := out["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "healthy") {
		t.Fatalf("status must reflect capabilities: %s", text)
	}
}

func TestNotificationsSilentAndIDsPreserved(t *testing.T) {
	s := NewServerWithCapabilities(newFakeCapabilities())
	var buf bytes.Buffer
	s.dispatch([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`+"\n"), &buf)
	if buf.Len() != 0 {
		t.Fatalf("notifications must be silent")
	}
	out := call(t, s, "ping", "abc-123", nil)
	if out["id"] != "abc-123" {
		t.Fatalf("string ID must be preserved: %v", out)
	}
}

func TestHistoryToolUsesInjectedStore(t *testing.T) {
	s := NewServerWithCapabilities(newFakeCapabilities())
	s.HistoryList = func(int) ([]history.Entry, error) {
		return []history.Entry{{ID: "h1", Type: "image"}}, nil
	}
	out := call(t, s, "tools/call", 5, map[string]any{"name": "get_flow_history", "arguments": map[string]any{"limit": 1}})
	text := out["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "h1") {
		t.Fatalf("history must come from store: %s", text)
	}
}

func TestAsyncVideoSubmitAndPoll(t *testing.T) {
	caps := newFakeCapabilities()
	s := NewServerWithCapabilities(caps)

	decodeToolData := func(out map[string]any) map[string]any {
		t.Helper()
		res := out["result"].(map[string]any)
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		var data map[string]any
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			t.Fatalf("tool response must be valid JSON: %v, raw: %s", err, text)
		}
		return data
	}

	submitOut := call(t, s, "tools/call", 10, map[string]any{
		"name": "generate_flow_video",
		"arguments": map[string]any{
			"prompt":   "test scene in city",
			"duration": 10,
			"aspect":   "landscape",
		},
	})
	submitData := decodeToolData(submitOut)
	if submitData["status"] != "queued" {
		t.Fatalf("expected queued submission, got %v", submitData["status"])
	}
	jobID, ok := submitData["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("expected non-empty job_id, got %v", submitData["job_id"])
	}

	pollOut := call(t, s, "tools/call", 11, map[string]any{
		"name":      "get_flow_status",
		"arguments": map[string]any{"job_id": jobID},
	})
	pollData := decodeToolData(pollOut)
	if pollData["status"] != "completed" {
		t.Fatalf("expected completed status on poll, got %v", pollData["status"])
	}
}
