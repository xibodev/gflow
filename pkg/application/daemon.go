package application

import "context"

// ImageGenerationRequest is the transport-independent input for direct image generation.
type ImageGenerationRequest struct {
	Prompt            string
	Aspect            string
	Count             int
	Model             string
	ReferenceMediaIDs []string
	Seed              *int64
}

// VideoSubmission is a direct provider submission. IDs are provider-owned and
// can be passed back to VideoPoller without an application queue in between.
type VideoSubmission struct {
	MediaIDs []string
}

// VideoState is a single provider status observation.
type VideoState struct {
	ID     string
	Status string
	Reason string
}

// Readiness is provider-neutral daemon readiness. Pointer fields are optional;
// transports choose their compatibility representation when a value is absent.
type Readiness struct {
	State               string
	TransportConnected  *bool
	CredentialAvailable *bool
	ActiveOperations    *int
}

type ImageGenerationCapability interface {
	GenerateImages(context.Context, ImageGenerationRequest) ([]Asset, error)
}

type VideoSubmitter interface {
	SubmitVideo(context.Context, VideoRequest) (VideoSubmission, error)
}

type VideoPoller interface {
	PollVideo(context.Context, []string) ([]VideoState, error)
	ResolveVideoAssets(context.Context, []string) ([]Asset, error)
}

type VideoUpsampler interface {
	SubmitUpsample(context.Context, UpsampleRequest) (VideoSubmission, error)
}

type ImageUploader interface {
	UploadImage(context.Context, []byte, string) (string, error)
}

type CreditsReader interface {
	Credits(context.Context) (any, error)
}

type ReadinessReader interface {
	Readiness(context.Context) (Readiness, error)
}

// DaemonCapabilities contains the independently optional operations exposed by
// the daemon HTTP transport.
type DaemonCapabilities struct {
	Images      ImageGenerationCapability
	VideoSubmit VideoSubmitter
	VideoPoll   VideoPoller
	Upsample    VideoUpsampler
	Upload      ImageUploader
	Credits     CreditsReader
}
