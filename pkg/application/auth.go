// Package application defines provider-neutral media generation contracts.
package application

import (
	"context"
	"errors"
	"time"
)

// AuthCheck is a single named readiness probe within an AuthStatus.
type AuthCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// AuthStatus is a provider-neutral authentication/readiness snapshot.
// Checks is always non-nil after NormalizeAuthStatus. NextSteps is always
// non-nil and contains concrete operator actions (placeholders only, never
// secrets). Guidance-vs-automation: NextSteps tells the operator what to do;
// gflow only auto-runs what the provider/adapter explicitly declares safe.
type AuthStatus struct {
	Provider  ProviderID  `json:"provider"`
	Ready     bool        `json:"ready"`
	Summary   string      `json:"summary,omitempty"`
	Checks    []AuthCheck `json:"checks"`
	NextSteps []string    `json:"next_steps"`
}

// AuthProvider is implemented by providers that can report login/readiness
// without side effects. Implementations must be read-only: they must not
// launch browsers, start daemons, refresh tokens, or fabricate credentials.
type AuthProvider interface {
	Provider
	AuthStatus(ctx context.Context) (AuthStatus, error)
}

// NormalizeAuthStatus guarantees non-nil Checks/NextSteps slices so JSON
// output has a stable shape and callers never need nil checks.
func NormalizeAuthStatus(status AuthStatus) AuthStatus {
	if status.Checks == nil {
		status.Checks = []AuthCheck{}
	}
	if status.NextSteps == nil {
		status.NextSteps = []string{}
	}
	return status
}

const (
	// DefaultLoginWaitTimeout bounds an interactive sign-in wait.
	DefaultLoginWaitTimeout = 5 * time.Minute
	// DefaultLoginPollInterval spaces session-capture attempts while waiting.
	DefaultLoginPollInterval = 5 * time.Second
)

// LoginOptions steers an interactive login. Launch permits the adapter to
// start (or restart) its app/browser; without it the adapter may only run
// side-effect-free checks plus explicitly safe refreshes. Zero Timeout and
// PollInterval select the defaults above.
type LoginOptions struct {
	Launch       bool
	Timeout      time.Duration
	PollInterval time.Duration
	// OnProgress, when set, receives the observed status on each wait poll
	// (Summary carries a human note). Transports wire it to live output;
	// nil means silent. It is called from the waiting goroutine only.
	OnProgress func(AuthStatus)
}

// TimeoutOrDefault resolves the effective wait bound for adapters.
func (o LoginOptions) TimeoutOrDefault() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultLoginWaitTimeout
}

// PollIntervalOrDefault resolves the effective poll spacing for adapters.
func (o LoginOptions) PollIntervalOrDefault() time.Duration {
	if o.PollInterval > 0 {
		return o.PollInterval
	}
	return DefaultLoginPollInterval
}

// LoginProvider is implemented by provider adapters that own their full login
// flow. CheckAuth stays read-only; Login may launch and wait when
// opts.Launch is set. Either method returns the end-state AuthStatus. Login
// logic lives here, in the adapter, so out-of-process adapters and private
// installs can replace it without touching transports.
type LoginProvider interface {
	AuthProvider
	Login(ctx context.Context, opts LoginOptions) (AuthStatus, error)
}

// ErrLoginWaitTimeout reports a sign-in wait that outlived its bound while the
// last observed status is still returned for reporting.
var ErrLoginWaitTimeout = errors.New("timed out waiting for sign-in")

// WaitForCondition polls attempt until it reports done, the timeout elapses,
// or ctx is cancelled. It always returns the last observed status so callers
// can report the end state even on timeout/cancel.
func WaitForCondition(ctx context.Context, timeout, interval time.Duration, attempt func(context.Context) (AuthStatus, bool, error)) (AuthStatus, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, done, _ := attempt(ctx)
		if done {
			return status, nil
		}
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		if !time.Now().Before(deadline) {
			return status, ErrLoginWaitTimeout
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(interval):
		}
	}
}
