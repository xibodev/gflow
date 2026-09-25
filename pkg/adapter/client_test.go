package adapter_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
)

func TestNegotiationAndDispatch(t *testing.T) {
	client, err := startHelper(t, "success", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	descriptor := client.AdapterDescriptor()
	if descriptor.ProviderID != "test.provider" || descriptor.DisplayName != "Test Provider" || descriptor.AdapterVersion != "1.2.3" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	result, err := client.GenerateImage(context.Background(), application.ImageRequest{Prompt: "draw", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 1 || result.Assets[0].ID != "image-1" || result.Assets[0].Type != "image" {
		t.Fatalf("image result = %+v", result)
	}
	submission, err := client.SubmitVideoOperation(context.Background(), application.VideoRequest{Prompt: "move"})
	if err != nil {
		t.Fatal(err)
	}
	if submission.ID != "operation-1" {
		t.Fatalf("submission = %+v", submission)
	}
	poll, err := client.PollVideoOperation(context.Background(), submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll.Status != "succeeded" || len(poll.Assets) != 1 || poll.Assets[0].Type != "video" {
		t.Fatalf("poll = %+v", poll)
	}
	var _ application.Provider = client
	var _ application.ImageGenerator = client
	var _ application.AsyncVideoSubmitter = client
	var _ application.AsyncVideoPoller = client
}

func TestNegotiationRejectsInvalidDescriptors(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want error
	}{
		{name: "version mismatch", mode: "version-mismatch", want: adapter.ErrProtocolMismatch},
		{name: "invalid provider ID", mode: "invalid-id", want: adapter.ErrProtocol},
		{name: "unknown capability", mode: "unknown-capability", want: adapter.ErrProtocol},
		{name: "duplicate capability", mode: "duplicate-capability", want: adapter.ErrProtocol},
		{name: "video submit only", mode: "video-submit-only", want: adapter.ErrProtocol},
		{name: "video poll only", mode: "video-poll-only", want: adapter.ErrProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := startHelper(t, test.mode, 0)
			if client != nil {
				_ = client.Close()
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMalformedAndOversizedOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
		max  int
	}{
		{name: "malformed", mode: "malformed", max: 0},
		{name: "oversized", mode: "oversized", max: 256},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := startHelper(t, test.mode, test.max)
			if client != nil {
				_ = client.Close()
			}
			if !errors.Is(err, adapter.ErrProtocol) {
				t.Fatalf("error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestCapabilityEnforcement(t *testing.T) {
	client, err := startHelper(t, "image-only", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.SubmitVideoOperation(context.Background(), application.VideoRequest{})
	if !errors.Is(err, adapter.ErrUnsupportedCapability) {
		t.Fatalf("error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestCancellationTerminatesProcess(t *testing.T) {
	client, err := startHelper(t, "block-call", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = client.GenerateImage(ctx, application.ImageRequest{Prompt: "block"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationReturnsWhenDescendantHoldsStdout(t *testing.T) {
	client, err := startHelper(t, "block-call-held-stdout", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = client.GenerateImage(ctx, application.ImageRequest{Prompt: "block"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %v while descendant held stdout", elapsed)
	}
}

func TestMismatchedResponseIDTerminatesClient(t *testing.T) {
	client, err := startHelper(t, "wrong-id-then-stale", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateImage(context.Background(), application.ImageRequest{Prompt: "draw", Count: 2})
	if !errors.Is(err, adapter.ErrProtocol) {
		t.Fatalf("first error = %v, want ErrProtocol", err)
	}
	_, err = client.GenerateImage(context.Background(), application.ImageRequest{Prompt: "draw", Count: 2})
	if !errors.Is(err, adapter.ErrClosed) {
		t.Fatalf("second error = %v, want ErrClosed", err)
	}
}

func TestCloseAllowsGracefulEOFExit(t *testing.T) {
	client, err := startHelper(t, "image-only", 0)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("graceful close exceeded bound")
	}
	_, err = client.GenerateImage(context.Background(), application.ImageRequest{})
	if !errors.Is(err, adapter.ErrClosed) {
		t.Fatalf("error after close = %v, want ErrClosed", err)
	}
}

func TestCloseReapsUncooperativeDirectChild(t *testing.T) {
	client, err := startHelper(t, "ignore-eof", 0)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	first := client.Close()
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond || elapsed > time.Second {
		t.Fatalf("uncooperative close took %v", elapsed)
	}
	second := client.Close()
	if first != second {
		t.Fatalf("Close error was not stable: first=%v second=%v", first, second)
	}
	if runtime.GOOS != "windows" && first != nil {
		t.Fatalf("Close error = %v", first)
	}
}

func TestUnexpectedProcessExit(t *testing.T) {
	client, err := startHelper(t, "exit-after-init", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.GenerateImage(context.Background(), application.ImageRequest{})
	if !errors.Is(err, adapter.ErrProcessExited) {
		t.Fatalf("error = %v, want ErrProcessExited", err)
	}
}

func startHelper(t *testing.T, mode string, maxMessage int) (*adapter.Client, error) {
	t.Helper()
	return adapter.Start(context.Background(), adapter.Config{
		Command:         os.Args[0],
		Args:            []string{"-test.run=TestAdapterHelperProcess"},
		Env:             append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "GFLOW_ADAPTER_HELPER_MODE="+mode),
		CoreVersion:     "test-core",
		MaxMessageBytes: maxMessage,
		CloseTimeout:    200 * time.Millisecond,
	})
}

func TestAdapterHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Getenv("GFLOW_ADAPTER_HELPER_MODE")
	assetURL := os.Getenv("GFLOW_ADAPTER_HELPER_ASSET_URL")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.Method == adapter.MethodInitialize {
			switch mode {
			case "malformed":
				fmt.Println(`{"jsonrpc":`)
				continue
			case "oversized":
				fmt.Println(strings.Repeat("x", 1024))
				continue
			}
			protocolVersion := adapter.ProtocolVersion
			providerID := "test.provider"
			capabilities := []application.Capability{
				application.CapabilityImageGeneration,
				application.CapabilityVideoSubmit,
				application.CapabilityVideoPoll,
			}
			switch mode {
			case "version-mismatch":
				protocolVersion = "999"
			case "invalid-id":
				providerID = "Invalid ID"
			case "unknown-capability":
				capabilities = append(capabilities, "arbitrary-tool")
			case "duplicate-capability":
				capabilities = append(capabilities, application.CapabilityImageGeneration)
			case "video-submit-only":
				capabilities = []application.Capability{application.CapabilityVideoSubmit}
			case "video-poll-only":
				capabilities = []application.Capability{application.CapabilityVideoPoll}
			case "image-only", "exit-after-init", "block-call", "block-call-held-stdout", "wrong-id-then-stale":
				capabilities = []application.Capability{application.CapabilityImageGeneration}
			}
			writeHelperResponse(request.ID, map[string]any{
				"provider_id": providerID, "display_name": "Test Provider", "adapter_version": "1.2.3",
				"protocol_version": protocolVersion, "capabilities": capabilities,
			})
			if mode == "exit-after-init" {
				os.Exit(0)
			}
			if mode == "ignore-eof" {
				for {
					time.Sleep(time.Hour)
				}
			}
			continue
		}
		if mode == "block-call" {
			time.Sleep(time.Hour)
		}
		if mode == "block-call-held-stdout" {
			var child *exec.Cmd
			if runtime.GOOS == "windows" {
				child = exec.Command("powershell.exe", "-NoProfile", "-Command", "Start-Sleep -Seconds 2")
			} else {
				child = exec.Command("sh", "-c", "sleep 2")
			}
			child.Stdout = os.Stdout
			if child.Start() != nil {
				os.Exit(7)
			}
			time.Sleep(time.Hour)
		}
		switch request.Method {
		case adapter.MethodGenerateImage:
			var params struct {
				Prompt string `json:"prompt"`
				Count  int    `json:"count"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Prompt != "draw" || params.Count != 2 {
				os.Exit(4)
			}
			if mode == "wrong-id-then-stale" {
				writeHelperResponse(request.ID+1, map[string]any{"assets": []map[string]any{{"id": "wrong", "type": "image"}}})
				writeHelperResponse(request.ID, map[string]any{"assets": []map[string]any{{"id": "stale", "type": "image"}}})
				continue
			}
			if assetURL == "" {
				assetURL = "https://example.test/image.png"
			}
			writeHelperResponse(request.ID, map[string]any{"assets": []map[string]any{{"id": "image-1", "type": "image", "url": assetURL}}})
		case adapter.MethodSubmitVideo:
			var params struct {
				Prompt string `json:"prompt"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Prompt != "move" {
				os.Exit(5)
			}
			writeHelperResponse(request.ID, map[string]any{"operation_id": "operation-1"})
		case adapter.MethodPollVideo:
			var params struct {
				OperationID string `json:"operation_id"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.OperationID != "operation-1" {
				os.Exit(6)
			}
			videoURL := "https://example.test/video.mp4"
			if mode == "success-env-url" && assetURL != "" {
				videoURL = assetURL
			}
			writeHelperResponse(request.ID, map[string]any{
				"operation_id": "operation-1", "status": "succeeded",
				"assets": []map[string]any{{"id": "video-1", "type": "video", "url": videoURL, "mime_type": "video/mp4"}},
			})
		default:
			os.Exit(3)
		}
	}
	os.Exit(0)
}

func writeHelperResponse(id int, result any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
