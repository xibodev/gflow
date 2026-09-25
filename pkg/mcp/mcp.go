package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/history"
	"github.com/xibodev/gflow/pkg/models"
)

// Request represents a JSON-RPC 2.0 request with raw ID preservation.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error represents a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server implements an MCP stdio server backed by application media capabilities.
type Server struct {
	media application.MediaCapabilities

	outMu  sync.Mutex
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
	wg     sync.WaitGroup

	HistoryList func(int) ([]history.Entry, error)
}

// NewServerWithCapabilities creates an MCP wrapper around injected application capabilities.
func NewServerWithCapabilities(media application.MediaCapabilities) *Server {
	return &Server{media: media, cancel: make(map[string]context.CancelFunc), HistoryList: history.List}
}

// Close stops application workers owned by the injected service.
func (s *Server) Close() error {
	s.cancelAll()
	return s.media.Close()
}

// Run starts the JSON-RPC stdio loop. Stdout carries only JSON-RPC messages.
func (s *Server) Run() error {
	return s.RunWithIO(os.Stdin, os.Stdout)
}

// RunWithIO serves one stream pair; used by tests.
func (s *Server) RunWithIO(in io.Reader, out io.Writer) (runErr error) {
	defer func() {
		if err := s.Close(); runErr == nil {
			runErr = err
		}
	}()
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(bytes.TrimSpace(line)) > 0 {
					s.dispatch(line, out)
				}
				s.waitIdle(5 * time.Second)
				return nil
			}
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		cp := append([]byte(nil), line...)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.dispatch(cp, out)
		}()
	}
}

func (s *Server) waitIdle(d time.Duration) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func (s *Server) cancelAll() {
	s.mu.Lock()
	for _, c := range s.cancel {
		c()
	}
	s.mu.Unlock()
}

func idKey(id json.RawMessage) string { return string(bytes.TrimSpace(id)) }

func (s *Server) dispatch(line []byte, out io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			s.write(out, &Response{JSONRPC: "2.0", Error: &Error{Code: -32603, Message: fmt.Sprintf("internal error: %v", r)}})
		}
	}()
	var req Request
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		s.write(out, &Response{JSONRPC: "2.0", Error: &Error{Code: -32700, Message: "parse error"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.write(out, &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32600, Message: "invalid request"}})
		return
	}
	// Notifications carry no ID and get no response.
	if len(bytes.TrimSpace(req.ID)) == 0 {
		if req.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(req.Params, &p)
			s.mu.Lock()
			if c, ok := s.cancel[idKey(p.RequestID)]; ok {
				c()
			}
			s.mu.Unlock()
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	s.mu.Lock()
	s.cancel[idKey(req.ID)] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancel, idKey(req.ID))
		s.mu.Unlock()
	}()
	resp := s.handleRequest(ctx, &req)
	if resp != nil {
		s.write(out, resp)
	}
}

func (s *Server) write(out io.Writer, resp *Response) {
	b, _ := json.Marshal(resp)
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = io.WriteString(out, string(b)+"\n")
}

func (s *Server) handleRequest(ctx context.Context, req *Request) *Response {
	switch req.Method {
	case "initialize":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": models.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": models.AppName, "version": models.AppVersion},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "ping":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.getToolsList()}}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		dec := json.NewDecoder(bytes.NewReader(req.Params))
		dec.UseNumber()
		if err := dec.Decode(&params); err != nil || params.Name == "" {
			return &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32602, Message: "invalid params"}}
		}
		result, err := s.executeTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"isError": true,
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("Error: %v", err)}},
			}}
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		}}
	default:
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}}
	}
}

func (s *Server) getToolsList() []map[string]any {
	return []map[string]any{
		{
			"name": "generate_flow_image", "description": "Generate AI images using Google Flow (Imagen 4 / Nano Banana 2).",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{
					"prompt":          map[string]any{"type": "string", "description": "Detailed image prompt."},
					"aspect":          map[string]any{"type": "string", "description": "landscape, square, portrait, 4:3, 3:4.", "default": "landscape"},
					"model":           map[string]any{"type": "string", "description": "narwhal, harbor_seal, gem_pix_2.", "default": "narwhal"},
					"count":           map[string]any{"type": "integer", "description": "Variations 1-4.", "default": 1, "minimum": 1, "maximum": 4},
					"reference_image": map[string]any{"type": "string", "description": "Reference image file path or media ID."},
					"seed":            map[string]any{"type": "integer", "description": "Reproducible seed 0-4294967295."},
				}, "required": []string{"prompt"}},
		},
		{
			"name": "generate_flow_video",
			"description": "Start AI video generation with native synchronous audio using Google Veo 3.1.\n\n" +
				"SUPPORTED MODALITIES:\n" +
				"1. Single-Shot: Text-to-video clip (4s, 6s, 8s, 10s) with cinematic motion.\n" +
				"2. Timestamp Continuation: Multi-scene cuts within one clip using '[00:00-00:04] Wide shot of... [00:04-00:08] Close-up of...'.\n" +
				"3. Image-to-Video: Animate starting from a reference image via 'start_image'.\n\n" +
				"AUDIO & SPEECH DIRECTION:\n" +
				"• Spoken Dialogue: Colon before quotes ('Person says: \"Hello\"') prevents subtitles.\n" +
				"• Vocal Tone & Language: 'Audio: A mature female narrator speaks in Brazilian Portuguese, saying: \"...\"'.\n" +
				"• Sound Design: Layer 'SFX: ...' and 'Ambient noise: ...'. Always append '(no subtitles, no text overlays)'.\n\n" +
				"NON-BLOCKING WORKFLOW:\n" +
				"Returns immediately with a 'job_id' (<1s). Check 'get_flow_status(job_id=...)' every 10-15s until 'status' is 'completed' to get the final 'file_path'.",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{
					"prompt":      map[string]any{"type": "string", "description": "Scene, camera motion, and audio prompt (dialogue, SFX, voiceover, language)."},
					"duration":    map[string]any{"type": "integer", "description": "Clip duration in seconds: 4, 6, 8, or 10.", "default": 10, "enum": []int{4, 6, 8, 10}},
					"aspect":      map[string]any{"type": "string", "description": "landscape (16:9), portrait (9:16), square (1:1).", "default": "landscape"},
					"resolution":  map[string]any{"type": "string", "description": "720p native, or 1080p/4k via upsample.", "default": "720p"},
					"start_image": map[string]any{"type": "string", "description": "Start frame file path or media ID for image-to-video."},
					"end_image":   map[string]any{"type": "string", "description": "End frame file path or media ID for first-to-last frame interpolation."},
					"seed":        map[string]any{"type": "integer", "description": "Reproducible seed."},
				}, "required": []string{"prompt"}},
		},
		{
			"name": "upsample_flow_video", "description": "Upsample a finished video to 1080p or 4K.",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{
					"media_id":   map[string]any{"type": "string", "description": "Source video media ID."},
					"aspect":     map[string]any{"type": "string", "default": "landscape"},
					"resolution": map[string]any{"type": "string", "description": "1080p or 4k.", "default": "1080p"},
					"seed":       map[string]any{"type": "integer"},
				}, "required": []string{"media_id"}},
		},
		{
			"name":        "get_flow_status",
			"description": "Check AI provider readiness, video generation quota availability, and poll active video jobs. Call without arguments to inspect account readiness, or pass 'job_id' to check job progress and retrieve the final video file_path.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{
						"type":        "string",
						"description": "Optional video job ID (e.g. 'job_vid_...') to check generation progress or retrieve output file_path.",
					},
				},
			},
		},
		{
			"name": "get_flow_history", "description": "List recent generations.",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{"limit": map[string]any{"type": "integer", "default": 10, "minimum": 1, "maximum": 500}}},
		},
	}
}

func asSeed(v any) (*int64, error) {
	if v == nil {
		return nil, nil
	}
	var n int64
	switch t := v.(type) {
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return nil, fmt.Errorf("invalid seed")
		}
		n = i
	case float64:
		n = int64(t)
	case int:
		n = int64(t)
	case int64:
		n = t
	default:
		return nil, fmt.Errorf("invalid seed")
	}
	if n < 0 || n > models.MaxSeed {
		return nil, fmt.Errorf("seed must be in [0,%d]", models.MaxSeed)
	}
	return &n, nil
}

func (s *Server) executeTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "get_flow_status":
		jobID, _ := args["job_id"].(string)
		if jobID == "" {
			jobID, _ = args["jobId"].(string)
		}
		if jobID != "" {
			job, ok := s.media.VideoJob(jobID)
			if !ok {
				res := map[string]any{
					"status":  "not_found",
					"job_id":  jobID,
					"message": fmt.Sprintf("job %s not found in registry", jobID),
				}
				b, _ := json.MarshalIndent(res, "", "  ")
				return string(b), nil
			}
			res := map[string]any{
				"status":   job.Status,
				"job_id":   job.ID,
				"prompt":   job.Prompt,
				"duration": job.Duration,
				"aspect":   job.Aspect,
			}
			if job.Status == "queued" {
				res["message"] = "Video job is queued waiting for active generation to finish. Continue polling."
			} else if job.Status == "processing" {
				elapsed := 0
				if job.StartedAt != nil {
					elapsed = int(time.Since(*job.StartedAt).Seconds())
				} else {
					elapsed = int(time.Since(job.CreatedAt).Seconds())
				}
				res["elapsed_seconds"] = elapsed
				res["message"] = "Video is currently generating in background. Continue polling."
			} else if job.Status == "completed" {
				res["file_path"] = job.FilePath
				res["resolution"] = job.Resolution
				res["has_audio"] = job.HasAudio
				res["message"] = "Video generation completed successfully."
			} else if job.Status == "failed" {
				errLower := strings.ToLower(job.Error)
				if strings.Contains(errLower, "limit") || strings.Contains(errLower, "quota") || strings.Contains(errLower, "resets") || strings.Contains(errLower, "insufficient") {
					res["error_code"] = "QUOTA_EXHAUSTED"
					res["reason"] = "Account video generation rate limit reached on upstream Google Gemini (Veo 3.1)."
					res["action_required"] = "Wait for your rolling account quota to reset (typically 1 hour)."
					res["agent_instruction"] = "DO NOT launch browsers, DO NOT kill processes, and DO NOT retry until quota resets. Report to user that account video generation limit is reached."
				} else if strings.Contains(errLower, "auth") || strings.Contains(errLower, "unauthenticated") {
					res["error_code"] = "AUTH_REQUIRED"
					res["reason"] = "Session authentication is expired or missing."
					res["action_required"] = "Run 'gflow status' or log in to Gemini to refresh local session."
					res["agent_instruction"] = "Ask the operator to log in once. Do not attempt automated browser logins."
				} else {
					res["error"] = job.Error
					res["message"] = "Video generation failed."
				}
			}
			b, _ := json.MarshalIndent(res, "", "  ")
			return string(b), nil
		}

		status, err := s.media.Status(ctx)
		if err != nil {
			return "", err
		}
		if status.Text != "" {
			return status.Text, nil
		}
		data, _ := json.MarshalIndent(status.Data, "", "  ")
		return string(data), nil
	case "get_flow_history":
		limit := 10
		switch l := args["limit"].(type) {
		case json.Number:
			if i, err := l.Int64(); err == nil && i > 0 {
				limit = int(i)
			}
		case float64:
			if l > 0 {
				limit = int(l)
			}
		}
		entries, err := s.HistoryList(limit)
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	case "generate_flow_image":
		prompt, _ := args["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("prompt is required")
		}
		aspect, _ := args["aspect"].(string)
		if aspect == "" {
			aspect = "landscape"
		}
		model, _ := args["model"].(string)
		if model == "" {
			model = "narwhal"
		}
		count := 1
		switch c := args["count"].(type) {
		case json.Number:
			if i, err := c.Int64(); err == nil {
				count = int(i)
			}
		case float64:
			count = int(c)
		}
		seed, err := asSeed(args["seed"])
		if err != nil {
			return "", err
		}
		reference, _ := args["reference_image"].(string)
		result, err := s.media.GenerateImage(ctx, application.ImageRequest{Prompt: prompt, Aspect: aspect, Count: count, Model: model, Reference: reference, Seed: seed})
		if err != nil {
			return "", err
		}
		resultData := map[string]any{
			"status":    "completed",
			"file_path": result.FilePath,
			"files":     result.Files,
			"count":     len(result.Files),
			"aspect":    aspect,
			"model":     model,
			"prompt":    prompt,
		}
		jsonBytes, _ := json.MarshalIndent(resultData, "", "  ")
		msg := fmt.Sprintf("Generated %d image(s):\n%s", len(result.Files), string(jsonBytes))
		for _, e := range result.Warnings {
			msg += "\nWarning: " + e
		}
		return msg, nil
	case "generate_flow_video":
		prompt, _ := args["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("prompt is required")
		}
		duration := 10
		switch d := args["duration"].(type) {
		case json.Number:
			if i, err := d.Int64(); err == nil {
				duration = int(i)
			}
		case float64:
			duration = int(d)
		}
		aspect, _ := args["aspect"].(string)
		if aspect == "" {
			aspect = "landscape"
		}
		res, _ := args["resolution"].(string)
		if res == "" {
			res = "720p"
		}
		seed, err := asSeed(args["seed"])
		if err != nil {
			return "", err
		}
		start, _ := args["start_image"].(string)
		end, _ := args["end_image"].(string)
		job, err := s.media.EnqueueVideo(ctx, application.VideoRequest{Prompt: prompt, Aspect: aspect, Duration: duration, Resolution: res, Start: start, End: end, Seed: seed})
		if err != nil {
			return "", err
		}

		resultData := map[string]any{
			"status":   "queued",
			"job_id":   job.ID,
			"prompt":   prompt,
			"duration": duration,
			"aspect":   aspect,
			"message":  fmt.Sprintf("Video job queued (queue depth: %d). Poll get_flow_status(job_id=%q) every 10-15s until completed.", job.QueueDepth, job.ID),
		}
		jsonBytes, _ := json.MarshalIndent(resultData, "", "  ")
		return string(jsonBytes), nil
	case "upsample_flow_video":
		mediaID, _ := args["media_id"].(string)
		if mediaID == "" {
			mediaID, _ = args["mediaId"].(string)
		}
		if strings.TrimSpace(mediaID) == "" {
			return "", fmt.Errorf("media_id is required")
		}
		aspect, _ := args["aspect"].(string)
		if aspect == "" {
			aspect = "landscape"
		}
		res, _ := args["resolution"].(string)
		if res == "" {
			res = "1080p"
		}
		seed, err := asSeed(args["seed"])
		if err != nil {
			return "", err
		}
		result, err := s.media.UpsampleVideo(ctx, application.UpsampleRequest{MediaID: mediaID, Aspect: aspect, Resolution: res, Seed: seed})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Upsampled video (%s):\n%s", res, formatBulletList(result.Files)), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func formatBulletList(items []string) string {
	res := ""
	for _, it := range items {
		res += fmt.Sprintf("- %s\n", it)
	}
	return res
}
