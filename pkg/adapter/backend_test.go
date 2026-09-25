package adapter_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/history"
	"github.com/xibodev/gflow/pkg/mcp"
)

func TestAdapterBackendApplicationFlowAndClose(t *testing.T) {
	payload := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GFLOW_ADAPTER_HELPER_ASSET_URL", server.URL+"/asset")

	client, err := startHelper(t, "success-env-url", 0)
	if err != nil {
		t.Fatal(err)
	}
	var entries []history.Entry
	backend, err := adapter.NewBackend(client, t.TempDir(), adapter.WithPollInterval(time.Millisecond), adapter.WithHistoryWriter(func(entry history.Entry) error {
		entries = append(entries, entry)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewMediaService(backend, 1)
	job, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: "move", Resolution: "720p"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, ok := service.VideoJob(job.ID)
		if ok && current.Status == "completed" {
			got, err := os.ReadFile(current.FilePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) || len(entries) != 1 || entries[0].Type != "video" {
				t.Fatalf("payload/history mismatch: bytes=%v entries=%+v", got, entries)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete: %+v", current)
		}
		time.Sleep(time.Millisecond)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GenerateImage(context.Background(), application.ImageRequest{Prompt: "draw"}); err != adapter.ErrClosed {
		t.Fatalf("adapter after service close error = %v, want ErrClosed", err)
	}
}

func TestAdapterBackendThroughMCPAndClose(t *testing.T) {
	payload := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GFLOW_ADAPTER_HELPER_ASSET_URL", server.URL+"/asset")

	client, err := startHelper(t, "success-env-url", 0)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := adapter.NewBackend(client, t.TempDir(), adapter.WithHistoryWriter(func(history.Entry) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	capabilities := application.NewMediaService(backend, 1)
	serverMCP := mcp.NewServerWithCapabilities(capabilities)
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"generate_flow_image\",\"arguments\":{\"prompt\":\"draw\",\"count\":2}}}\n")
	var output bytes.Buffer
	if err := serverMCP.RunWithIO(input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `Generated 1 image`) || !strings.Contains(output.String(), `image-1`) {
		t.Fatalf("MCP output = %s", output.String())
	}
	if _, err := client.GenerateImage(context.Background(), application.ImageRequest{Prompt: "draw"}); err != adapter.ErrClosed {
		t.Fatalf("adapter after MCP close error = %v, want ErrClosed", err)
	}
}
