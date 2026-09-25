package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xibodev/gflow/pkg/application"
)

type stubAuthProvider struct {
	descriptor application.Descriptor
	status     application.AuthStatus
	err        error
}

func (p stubAuthProvider) Descriptor() application.Descriptor { return p.descriptor }

func (p stubAuthProvider) AuthStatus(context.Context) (application.AuthStatus, error) {
	if p.err != nil {
		return application.AuthStatus{}, p.err
	}
	return application.NormalizeAuthStatus(p.status), nil
}

func TestNormalizeAuthStatusGuaranteesNonNilSlices(t *testing.T) {
	status := application.NormalizeAuthStatus(application.AuthStatus{Provider: application.ProviderFlow})
	if status.Checks == nil || status.NextSteps == nil {
		t.Fatal("normalized slices must be non-nil")
	}
	if len(status.Checks) != 0 || len(status.NextSteps) != 0 {
		t.Fatalf("normalized slices = %+v", status)
	}
}

func TestAuthStatusMappingPreservesProviderReadyAndSteps(t *testing.T) {
	provider := stubAuthProvider{
		descriptor: application.Descriptor{ID: application.ProviderGemini},
		status: application.AuthStatus{
			Provider:  application.ProviderGemini,
			Ready:     true,
			Summary:   "Ready",
			Checks:    []application.AuthCheck{{Name: "cached_session", OK: true, Detail: "Ready"}},
			NextSteps: []string{},
		},
	}
	status, err := provider.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Provider != application.ProviderGemini || !status.Ready {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Checks) != 1 || status.Checks[0].Name != "cached_session" || !status.Checks[0].OK {
		t.Fatalf("checks = %+v", status.Checks)
	}
}

func TestAuthProviderSatisfiesProviderContract(t *testing.T) {
	var _ application.Provider = stubAuthProvider{}
	var _ application.AuthProvider = stubAuthProvider{}
}

func TestWaitForConditionSucceedsAfterRetries(t *testing.T) {
	calls := 0
	final, err := application.WaitForCondition(context.Background(), 5*time.Second, time.Millisecond,
		func(context.Context) (application.AuthStatus, bool, error) {
			calls++
			st := application.AuthStatus{Provider: application.ProviderGemini, Ready: calls >= 3}
			return st, calls >= 3, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || !final.Ready {
		t.Fatalf("calls=%d ready=%v", calls, final.Ready)
	}
}

func TestWaitForConditionTimesOutWithLastStatus(t *testing.T) {
	last := application.AuthStatus{Provider: application.ProviderFlow, Ready: false, Summary: "waiting"}
	_, err := application.WaitForCondition(context.Background(), 20*time.Millisecond, time.Millisecond,
		func(context.Context) (application.AuthStatus, bool, error) {
			return last, false, nil
		})
	if !errors.Is(err, application.ErrLoginWaitTimeout) {
		t.Fatalf("error = %v, want ErrLoginWaitTimeout", err)
	}
}

func TestWaitForConditionCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := application.WaitForCondition(ctx, 5*time.Second, time.Millisecond,
		func(context.Context) (application.AuthStatus, bool, error) {
			return application.AuthStatus{}, false, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
