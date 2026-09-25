package models

import (
	"fmt"
	"strings"

	"github.com/xibodev/gflow/pkg/application"
)

// Single source for product/protocol identity exposed by the daemon.
const (
	AppName               = "gflow"
	ProtocolVersion       = "2024-11-05"
	ProductIdentity       = "gflow"
	MaxSeed         int64 = application.MaxSeed
)

// AppVersion is the running build's version. cmd/gflow stamps it at startup
// from its ldflags-injected version (or the module build info), so /health,
// the daemon version check, and MCP serverInfo all report the real build.
var AppVersion = "dev"

// Typed errors for the local daemon boundary. Use errors.Is/As to branch.
// They alias the shared taxonomy in package application so identity is
// preserved across every layer.
var (
	ErrValidation      = application.ErrValidation
	ErrUpstream        = application.ErrUpstream
	ErrAuth            = application.ErrAuth
	ErrTerminal        = application.ErrTerminal
	ErrNotFound        = application.ErrNotFound
	ErrQuota           = application.ErrQuota
	ErrContentPolicy   = application.ErrContentPolicy
	ErrPlanRequired    = application.ErrPlanRequired
	ErrRiskBlocked     = application.ErrRiskBlocked
	ErrFrontendChanged = application.ErrFrontendChanged
	ErrTransient       = application.ErrTransient
	ErrNotSubmitted    = application.ErrNotSubmitted
)

// APIError is the structured error payload returned by the local daemon.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// ImageRequest is the local daemon contract for image generation. It preserves
// the OpenAI-compatible fields while carrying gflow extensions explicitly.
type ImageRequest struct {
	Prompt            string   `json:"prompt"`
	N                 int      `json:"n"`
	Size              string   `json:"size,omitempty"`
	Model             string   `json:"model,omitempty"`
	ResponseFormat    string   `json:"response_format,omitempty"`
	Aspect            string   `json:"aspect,omitempty"`
	ReferenceMediaIDs []string `json:"reference_media_ids,omitempty"`
	Seed              *int64   `json:"seed,omitempty"`
}

// VideoSubmitRequest is the local daemon contract for submitting a video job.
// Resolution is intentionally validated at this layer: only native 720p (or
// omitted) may be submitted here. Upsampling is a separate operation.
type VideoSubmitRequest struct {
	Prompt     string `json:"prompt"`
	Aspect     string `json:"aspect,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	StartImage string `json:"start_image,omitempty"`
	EndImage   string `json:"end_image,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

// JobSubmission is returned when a video/upsample job is accepted.
type JobSubmission struct {
	JobID    string   `json:"job_id"`
	MediaIDs []string `json:"media_ids"`
	Status   string   `json:"status"`
	Created  int64    `json:"created"`
}

// JobStatus is returned by single-shot status checks.
type JobStatus struct {
	JobID  string    `json:"job_id"`
	Status string    `json:"status"` // processing|succeeded|failed
	Assets []Asset   `json:"assets,omitempty"`
	Error  *APIError `json:"error,omitempty"`
}

// ValidateSeed checks presence semantics without collapsing explicit zero.
func ValidateSeed(seed *int64) (int64, bool, error) {
	if seed == nil {
		return 0, false, nil
	}
	if err := application.ValidateSeed(seed); err != nil {
		return 0, false, err
	}
	return *seed, true, nil
}

// ValidateImageRequest enforces daemon-side contracts before any dispatch.
func ValidateImageRequest(req *ImageRequest) error {
	if req == nil {
		return fmt.Errorf("%w: nil image request", ErrValidation)
	}
	normalized, err := application.NormalizeImageRequest(application.ImageRequest{
		Prompt: req.Prompt, Count: req.N, Seed: req.Seed,
	})
	if err != nil {
		return err
	}
	req.N = normalized.Count
	if req.ResponseFormat != "" && req.ResponseFormat != "url" && req.ResponseFormat != "b64_json" {
		return fmt.Errorf("%w: response_format must be url or b64_json", ErrValidation)
	}
	return nil
}

// ValidateVideoSubmit enforces daemon-side contracts before any dispatch.
func ValidateVideoSubmit(req *VideoSubmitRequest) error {
	if req == nil {
		return fmt.Errorf("%w: nil video request", ErrValidation)
	}
	normalized, err := application.NormalizeVideoRequest(application.VideoRequest{
		Prompt: req.Prompt, Duration: req.Duration, Start: req.StartImage, End: req.EndImage, Seed: req.Seed,
	})
	if err != nil {
		return err
	}
	req.Duration = normalized.Duration
	switch strings.ToLower(strings.TrimSpace(req.Resolution)) {
	case "", "720p", "native":
		// accepted at submit time
	case "1080p", "4k":
		return fmt.Errorf("%w: resolution %q requires the upsample operation after native generation completes", ErrValidation, req.Resolution)
	default:
		return fmt.Errorf("%w: unknown resolution %q", ErrValidation, req.Resolution)
	}
	return nil
}
