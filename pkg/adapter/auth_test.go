package adapter_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
)

func startAuthHelper(t *testing.T, mode string) (*adapter.Client, error) {
	t.Helper()
	return adapter.Start(context.Background(), adapter.Config{
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestAdapterAuthHelperProcess"},
		Env:          append(os.Environ(), "GO_WANT_AUTH_HELPER_PROCESS=1", "GFLOW_AUTH_HELPER_MODE="+mode),
		CoreVersion:  "test-core",
		CloseTimeout: 200 * time.Millisecond,
	})
}

func TestAuthCapabilityValidation(t *testing.T) {
	// auth-login without auth-status must be rejected at handshake.
	client, err := startAuthHelper(t, "auth-login-without-status")
	if client != nil {
		_ = client.Close()
	}
	if !errors.Is(err, adapter.ErrProtocol) {
		t.Fatalf("auth-login without auth-status error = %v, want ErrProtocol", err)
	}
	// describe-only and login-supported must be accepted.
	for _, mode := range []string{"auth-describe-only", "auth-login-supported"} {
		c, err := startAuthHelper(t, mode)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		_ = c.Close()
	}
}

func TestAuthDescribeDispatch(t *testing.T) {
	client, err := startAuthHelper(t, "auth-describe-only")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload, err := client.AuthDescribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.Ready || payload.Summary == "" {
		t.Fatalf("describe payload = %+v, want ready with summary", payload)
	}
	if payload.Checks == nil || payload.NextSteps == nil {
		t.Fatal("describe payload slices must be non-nil after normalization")
	}
	if len(payload.Checks) != 1 || payload.Checks[0].Name != "session" {
		t.Fatalf("checks = %+v", payload.Checks)
	}
}

func TestAuthLoginDispatch(t *testing.T) {
	client, err := startAuthHelper(t, "auth-login-supported")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	desc, err := client.AuthDescribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if desc.Ready {
		t.Fatal("describe should report not-ready before login")
	}
	login, err := client.AuthLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !login.Ready {
		t.Fatalf("login payload = %+v, want ready", login)
	}
	if login.Checks == nil || login.NextSteps == nil {
		t.Fatal("login payload slices must be non-nil")
	}
}

func TestAuthLoginRequiresCapability(t *testing.T) {
	client, err := startAuthHelper(t, "auth-describe-only")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.AuthLogin(context.Background())
	if !errors.Is(err, adapter.ErrUnsupportedCapability) {
		t.Fatalf("login without capability error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestLegacyAdapterAuthUnsupported(t *testing.T) {
	client, err := startAuthHelper(t, "legacy-no-auth")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.AuthDescribe(ctx)
	if !errors.Is(err, adapter.ErrUnsupportedCapability) {
		t.Fatalf("legacy describe error = %v, want ErrUnsupportedCapability", err)
	}
	_, err = client.AuthLogin(ctx)
	if !errors.Is(err, adapter.ErrUnsupportedCapability) {
		t.Fatalf("legacy login error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestAuthDeclaredButMissingMethodMapsToUnsupported(t *testing.T) {
	client, err := startAuthHelper(t, "auth-declared-but-missing")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.AuthDescribe(ctx)
	if !errors.Is(err, adapter.ErrUnsupportedCapability) {
		t.Fatalf("missing-method error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestAdapterAuthHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AUTH_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Getenv("GFLOW_AUTH_HELPER_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	writeResult := func(id int, result any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	writeMethodNotFound := func(id int) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": -32601, "message": "Method not found"},
		})
	}
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
			var caps []application.Capability
			switch mode {
			case "auth-describe-only":
				caps = []application.Capability{application.CapabilityAuthStatus}
			case "auth-login-supported":
				caps = []application.Capability{application.CapabilityAuthStatus, application.CapabilityAuthLogin}
			case "auth-login-without-status":
				caps = []application.Capability{application.CapabilityAuthLogin}
			case "auth-declared-but-missing":
				caps = []application.Capability{application.CapabilityAuthStatus}
			case "legacy-no-auth":
				caps = []application.Capability{application.CapabilityImageGeneration}
			default:
				os.Exit(9)
			}
			writeResult(request.ID, map[string]any{
				"provider_id": "test.provider", "display_name": "Test Provider", "adapter_version": "1",
				"protocol_version": adapter.ProtocolVersion, "capabilities": caps,
			})
			continue
		}
		switch request.Method {
		case adapter.MethodAuthDescribe:
			if mode == "auth-declared-but-missing" {
				writeMethodNotFound(request.ID)
				continue
			}
			if mode == "auth-login-supported" {
				writeResult(request.ID, map[string]any{
					"ready": false, "summary": "Not logged in",
					"checks":     []map[string]any{{"name": "session", "ok": false, "detail": "Missing"}},
					"next_steps": []string{"Run vendor login"},
				})
				continue
			}
			writeResult(request.ID, map[string]any{
				"ready": true, "summary": "Ready",
				"checks":     []map[string]any{{"name": "session", "ok": true, "detail": "OK"}},
				"next_steps": []string{},
			})
		case adapter.MethodAuthLogin:
			if mode == "auth-login-supported" {
				writeResult(request.ID, map[string]any{
					"ready": true, "summary": "Logged in",
					"checks":     []map[string]any{{"name": "session", "ok": true}},
					"next_steps": []string{},
				})
				continue
			}
			writeMethodNotFound(request.ID)
		default:
			writeMethodNotFound(request.ID)
		}
	}
	os.Exit(0)
}
