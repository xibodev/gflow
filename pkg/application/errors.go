package application

import (
	"context"
	"errors"
)

// Shared error taxonomy. Providers wrap exactly one of these class sentinels
// (fmt.Errorf("%w: ...", ErrQuota)) so that every surface — CLI exit codes,
// MCP error_code fields, and HTTP API error codes — classifies a failure from
// the error's type instead of guessing from message text.
var (
	// ErrAuth means the provider session is missing, expired, or signed out.
	ErrAuth = errors.New("authentication error")
	// ErrUpstream is an unclassified provider failure.
	ErrUpstream = errors.New("upstream error")
	// ErrTerminal is a generation the provider reported as failed.
	ErrTerminal = errors.New("terminal generation failure")
	// ErrNotFound means a referenced job, media ID, or resource does not exist.
	ErrNotFound = errors.New("not found")
	// ErrQuota means a usage, credit, or rate limit was reached.
	ErrQuota = errors.New("usage limit reached")
	// ErrContentPolicy means the provider refused the prompt or inputs.
	ErrContentPolicy = errors.New("rejected by content policy")
	// ErrPlanRequired means the signed-in account's plan lacks the capability.
	ErrPlanRequired = errors.New("not available on this account's plan")
	// ErrRiskBlocked means upstream abuse protection rejected the request.
	ErrRiskBlocked = errors.New("blocked by upstream abuse protection")
	// ErrFrontendChanged means the provider's web app changed in a way this
	// build cannot drive (moved host, retired endpoints, new page structure).
	ErrFrontendChanged = errors.New("upstream web app changed")
	// ErrTransient is a temporary failure that may succeed on a later retry.
	ErrTransient = errors.New("temporary upstream failure")
	// ErrNotSubmitted marks a failure that happened before anything was sent
	// upstream, so a fallback cannot double-charge the account. It is a marker
	// combined with a class sentinel, never a class on its own.
	ErrNotSubmitted = errors.New("request was not submitted upstream")
)

// Stable machine-readable error codes exposed to agents and scripts.
const (
	CodeValidation      = "VALIDATION"
	CodeUnsupported     = "UNSUPPORTED"
	CodeAuthRequired    = "AUTH_REQUIRED"
	CodeQuotaExhausted  = "QUOTA_EXHAUSTED"
	CodeContentPolicy   = "CONTENT_POLICY"
	CodePlanRequired    = "PLAN_REQUIRED"
	CodeRiskBlocked     = "RISK_BLOCKED"
	CodeFrontendChanged = "FRONTEND_CHANGED"
	CodeNotFound        = "NOT_FOUND"
	CodeQueueFull       = "QUEUE_FULL"
	CodeTimeout         = "TIMEOUT"
	CodeCancelled       = "CANCELLED"
	CodeTransient       = "TRANSIENT"
	CodeUpstream        = "UPSTREAM_ERROR"
	CodeUnknown         = "UNKNOWN"
)

// ErrorCode maps an error to its stable code. More specific classes win over
// generic ones when an error wraps several sentinels.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrValidation):
		return CodeValidation
	case errors.Is(err, ErrUnsupportedCapability), errors.Is(err, ErrUnknownProvider):
		return CodeUnsupported
	case errors.Is(err, ErrFrontendChanged):
		return CodeFrontendChanged
	case errors.Is(err, ErrPlanRequired):
		return CodePlanRequired
	case errors.Is(err, ErrQuota):
		return CodeQuotaExhausted
	case errors.Is(err, ErrContentPolicy):
		return CodeContentPolicy
	case errors.Is(err, ErrRiskBlocked):
		return CodeRiskBlocked
	case errors.Is(err, ErrAuth):
		return CodeAuthRequired
	case errors.Is(err, ErrNotFound):
		return CodeNotFound
	case errors.Is(err, ErrQueueFull):
		return CodeQueueFull
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	case errors.Is(err, context.Canceled):
		return CodeCancelled
	case errors.Is(err, ErrTransient):
		return CodeTransient
	case errors.Is(err, ErrUpstream), errors.Is(err, ErrTerminal):
		return CodeUpstream
	default:
		return CodeUnknown
	}
}

// ErrorGuidance describes whether a code is worth retrying and the next step
// an operator or agent should take. Guidance never suggests working around
// provider protections: blocked or limited requests must wait or go manual.
func ErrorGuidance(code string) (retryable bool, action string) {
	switch code {
	case CodeValidation:
		return false, "Fix the request parameters; the same request will fail again."
	case CodeUnsupported:
		return false, "Use a provider that supports this capability (see `gflow providers`)."
	case CodeAuthRequired:
		return false, "Run `gflow status`, then `gflow login -P <provider> --launch` to refresh the session."
	case CodeQuotaExhausted:
		return false, "The account's usage limit was reached. Wait for it to reset (see the provider message) before retrying."
	case CodeContentPolicy:
		return false, "The provider refused the prompt or inputs. Rephrase or change the inputs; retrying unchanged will fail again."
	case CodePlanRequired:
		return false, "This capability is not included in the signed-in account's plan."
	case CodeRiskBlocked:
		return false, "Upstream abuse protection rejected the request. Stop automated submissions for a while and use the provider's web app manually; do not retry in a loop."
	case CodeFrontendChanged:
		return false, "The provider's web app changed in a way this gflow build cannot drive. Update gflow or use another provider."
	case CodeNotFound:
		return false, "The referenced job or media ID does not exist on this server or provider."
	case CodeQueueFull:
		return true, "Too many jobs are queued; wait for running jobs to finish, then resubmit."
	case CodeTimeout:
		return false, "The operation timed out. Check `gflow history` and the provider before resubmitting to avoid paying twice."
	case CodeCancelled:
		return false, "The operation was cancelled."
	case CodeTransient:
		return true, "Temporary upstream failure; retry once after a short delay."
	default:
		return false, "Inspect the error message; retry at most once."
	}
}

// Retryable reports whether err is worth an automatic retry.
func Retryable(err error) bool {
	retry, _ := ErrorGuidance(ErrorCode(err))
	return retry
}
