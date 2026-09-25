// Package application defines provider-neutral media generation contracts.
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ProviderID is the stable identifier used to select a provider.
type ProviderID string

const (
	ProviderFlow    ProviderID = "flow"
	ProviderGemini  ProviderID = "gemini"
	ProviderMiniMax ProviderID = "minimax"
)

// Capability identifies an optional operation implemented by a provider.
type Capability string

const (
	CapabilityImageGeneration Capability = "image-generation"
	CapabilityVideoGeneration Capability = "video-generation"
	CapabilityVideoSubmit     Capability = "video-submit"
	CapabilityVideoPoll       Capability = "video-poll"
	CapabilityChat            Capability = "chat"
	CapabilityAudio           Capability = "audio"
	CapabilityAuthStatus      Capability = "auth-status"
	CapabilityAuthLogin       Capability = "auth-login"
)

var (
	ErrDuplicateProvider     = errors.New("provider already registered")
	ErrUnknownProvider       = errors.New("unknown provider")
	ErrUnsupportedCapability = errors.New("unsupported provider capability")
	ErrValidation            = errors.New("validation error")
)

const MaxSeed int64 = 4294967295

// Descriptor describes a provider without exposing its implementation.
type Descriptor struct {
	ID           ProviderID
	Name         string
	Capabilities []Capability
}

// Supports reports whether the descriptor declares a capability.
func (d Descriptor) Supports(capability Capability) bool {
	for _, candidate := range d.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// UnknownProviderError reports selection of an unregistered provider.
type UnknownProviderError struct {
	Provider ProviderID
}

func (e *UnknownProviderError) Error() string {
	return fmt.Sprintf("%s %q", ErrUnknownProvider, e.Provider)
}

func (e *UnknownProviderError) Unwrap() error { return ErrUnknownProvider }

// UnsupportedCapabilityError reports an operation the provider cannot perform.
type UnsupportedCapabilityError struct {
	Provider   ProviderID
	Capability Capability
}

func (e *UnsupportedCapabilityError) Error() string {
	return fmt.Sprintf("provider %q: %s %q", e.Provider, ErrUnsupportedCapability, e.Capability)
}

func (e *UnsupportedCapabilityError) Unwrap() error { return ErrUnsupportedCapability }

// Provider is the minimal contract shared by all provider adapters.
type Provider interface {
	Descriptor() Descriptor
}

// ImageRequest is a provider-neutral image generation request.
type ImageRequest struct {
	Prompt            string
	Aspect            string
	Count             int
	Model             string
	Output            string
	Reference         string
	ReferenceMediaIDs []string
	Seed              *int64
}

// VideoRequest is a provider-neutral video generation request.
type VideoRequest struct {
	Prompt     string
	Aspect     string
	Duration   int
	Model      string
	Resolution string
	Output     string
	Start      string
	End        string
	Seed       *int64
}

// NormalizeImageRequest applies provider-neutral defaults and validation.
func NormalizeImageRequest(request ImageRequest) (ImageRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return ImageRequest{}, fmt.Errorf("%w: prompt is required", ErrValidation)
	}
	if request.Count == 0 {
		request.Count = 1
	}
	if request.Count < 1 || request.Count > 4 {
		return ImageRequest{}, fmt.Errorf("%w: count must be in [1,4]", ErrValidation)
	}
	if err := ValidateSeed(request.Seed); err != nil {
		return ImageRequest{}, err
	}
	return request, nil
}

// NormalizeChatRequest validates a chat prompt.
func NormalizeChatRequest(request ChatRequest) (ChatRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return ChatRequest{}, fmt.Errorf("%w: prompt is required", ErrValidation)
	}
	return request, nil
}

// NormalizeAudioRequest validates an audio prompt.
func NormalizeAudioRequest(request AudioRequest) (AudioRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return AudioRequest{}, fmt.Errorf("%w: prompt is required", ErrValidation)
	}
	return request, nil
}

// NormalizeVideoRequest applies provider-neutral defaults and validation.
func NormalizeVideoRequest(request VideoRequest) (VideoRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return VideoRequest{}, fmt.Errorf("%w: prompt is required", ErrValidation)
	}
	if request.Duration == 0 {
		request.Duration = 10
	}
	switch request.Duration {
	case 4, 6, 8, 10:
	default:
		return VideoRequest{}, fmt.Errorf("%w: duration must be 4, 6, 8, or 10", ErrValidation)
	}
	if request.End != "" && request.Start == "" {
		return VideoRequest{}, fmt.Errorf("%w: end requires start", ErrValidation)
	}
	if err := ValidateSeed(request.Seed); err != nil {
		return VideoRequest{}, err
	}
	return request, nil
}

// ValidateSeed enforces the provider-neutral seed range while preserving nil.
func ValidateSeed(seed *int64) error {
	if seed != nil && (*seed < 0 || *seed > MaxSeed) {
		return fmt.Errorf("%w: seed must be in [0,%d]", ErrValidation, MaxSeed)
	}
	return nil
}

// Asset is normalized media returned by a provider operation.
type Asset struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Size      int64  `json:"size,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
}

// ImageResult is the normalized result of image generation.
type ImageResult struct {
	Assets []Asset
}

// VideoResult is the normalized result of video generation.
type VideoResult struct {
	OperationID string
	Assets      []Asset
}

// ImageGenerator is implemented by providers that generate images.
type ImageGenerator interface {
	GenerateImage(context.Context, ImageRequest) (ImageResult, error)
}

// VideoGenerator is implemented by providers that generate videos.
type VideoGenerator interface {
	GenerateVideo(context.Context, VideoRequest) (VideoResult, error)
}

// ChatRequest is a provider-neutral chat prompt.
type ChatRequest struct {
	Prompt string
	Model  string
}

// ChatResult is normalized chat output. Warning flags replies that did not
// come from the signed-in account (for example anonymous Gemini).
type ChatResult struct {
	Text    string `json:"text"`
	Warning string `json:"warning,omitempty"`
}

// ChatProvider is implemented by providers that support text chat.
// This is a first-class entry-point capability — add it by adding a provider,
// not by hardcoding chat to a single upstream.
type ChatProvider interface {
	Chat(context.Context, ChatRequest) (ChatResult, error)
}

// AudioRequest is a provider-neutral audio/music generation request.
type AudioRequest struct {
	Prompt string
	// Output is a directory or file name; empty uses the configured output dir.
	Output string
}

// AudioResult is normalized audio output: saved media assets plus the
// provider's accompanying text (title, lyrics, notes).
type AudioResult struct {
	Text     string   `json:"text,omitempty"`
	Assets   []Asset  `json:"assets,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// AudioProvider is implemented by providers that support audio generation.
type AudioProvider interface {
	GenerateAudio(context.Context, AudioRequest) (AudioResult, error)
}

// VideoOperation is a provider-owned asynchronous video operation. Its ID is
// opaque and must only be returned to the provider that issued it.
type VideoOperation struct {
	ID     string  `json:"operation_id"`
	Status string  `json:"status,omitempty"`
	Assets []Asset `json:"assets,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// AsyncVideoSubmitter starts video work without forcing it into the synchronous
// VideoGenerator contract.
type AsyncVideoSubmitter interface {
	SubmitVideoOperation(context.Context, VideoRequest) (VideoOperation, error)
}

// AsyncVideoPoller observes a provider-owned video operation.
type AsyncVideoPoller interface {
	PollVideoOperation(context.Context, string) (VideoOperation, error)
}

// Registry stores provider adapters and is safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[ProviderID]Provider)}
}

// Register adds a provider. Provider IDs cannot be replaced implicitly.
func (r *Registry) Register(provider Provider) error {
	descriptor := provider.Descriptor()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[descriptor.ID]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateProvider, descriptor.ID)
	}
	r.providers[descriptor.ID] = provider
	return nil
}

// Provider resolves an adapter by ID.
func (r *Registry) Provider(id ProviderID) (Provider, error) {
	r.mu.RLock()
	provider, ok := r.providers[id]
	r.mu.RUnlock()
	if !ok {
		return nil, &UnknownProviderError{Provider: id}
	}
	return provider, nil
}

// Descriptor returns a defensive copy of a provider descriptor.
func (r *Registry) Descriptor(id ProviderID) (Descriptor, error) {
	provider, err := r.Provider(id)
	if err != nil {
		return Descriptor{}, err
	}
	descriptor := provider.Descriptor()
	descriptor.Capabilities = append([]Capability(nil), descriptor.Capabilities...)
	return descriptor, nil
}

// Service selects providers and dispatches provider-neutral operations.
type Service struct {
	registry *Registry
}

// NewService creates an application service backed by registry.
func NewService(registry *Registry) *Service {
	return &Service{registry: registry}
}

// GenerateImage dispatches image generation to the selected provider.
func (s *Service) GenerateImage(ctx context.Context, id ProviderID, request ImageRequest) (ImageResult, error) {
	provider, err := s.registry.Provider(id)
	if err != nil {
		return ImageResult{}, err
	}
	if !provider.Descriptor().Supports(CapabilityImageGeneration) {
		return ImageResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityImageGeneration}
	}
	generator, ok := provider.(ImageGenerator)
	if !ok {
		return ImageResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityImageGeneration}
	}
	request, err = NormalizeImageRequest(request)
	if err != nil {
		return ImageResult{}, err
	}
	return generator.GenerateImage(ctx, request)
}

// GenerateVideo dispatches video generation to the selected provider.
func (s *Service) GenerateVideo(ctx context.Context, id ProviderID, request VideoRequest) (VideoResult, error) {
	provider, err := s.registry.Provider(id)
	if err != nil {
		return VideoResult{}, err
	}
	if !provider.Descriptor().Supports(CapabilityVideoGeneration) {
		return VideoResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityVideoGeneration}
	}
	generator, ok := provider.(VideoGenerator)
	if !ok {
		return VideoResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityVideoGeneration}
	}
	request, err = NormalizeVideoRequest(request)
	if err != nil {
		return VideoResult{}, err
	}
	return generator.GenerateVideo(ctx, request)
}

// SubmitVideoOperation dispatches asynchronous video submission to the selected provider.
func (s *Service) SubmitVideoOperation(ctx context.Context, id ProviderID, request VideoRequest) (VideoOperation, error) {
	provider, err := s.registry.Provider(id)
	if err != nil {
		return VideoOperation{}, err
	}
	if !provider.Descriptor().Supports(CapabilityVideoSubmit) {
		return VideoOperation{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityVideoSubmit}
	}
	submitter, ok := provider.(AsyncVideoSubmitter)
	if !ok {
		return VideoOperation{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityVideoSubmit}
	}
	request, err = NormalizeVideoRequest(request)
	if err != nil {
		return VideoOperation{}, err
	}
	return submitter.SubmitVideoOperation(ctx, request)
}

// PollVideoOperation dispatches asynchronous video polling to the selected provider.
func (s *Service) PollVideoOperation(ctx context.Context, id ProviderID, operationID string) (VideoOperation, error) {
	provider, err := s.registry.Provider(id)
	if err != nil {
		return VideoOperation{}, err
	}
	if !provider.Descriptor().Supports(CapabilityVideoPoll) {
		return VideoOperation{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityVideoPoll}
	}
	poller, ok := provider.(AsyncVideoPoller)
	if !ok {
		return VideoOperation{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityVideoPoll}
	}
	return poller.PollVideoOperation(ctx, operationID)
}

// Chat dispatches text chat to the selected provider via the registry.
func (s *Service) Chat(ctx context.Context, id ProviderID, request ChatRequest) (ChatResult, error) {
	provider, err := s.registry.Provider(id)
	if err != nil {
		return ChatResult{}, err
	}
	if !provider.Descriptor().Supports(CapabilityChat) {
		return ChatResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityChat}
	}
	chatProvider, ok := provider.(ChatProvider)
	if !ok {
		return ChatResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityChat}
	}
	request, err = NormalizeChatRequest(request)
	if err != nil {
		return ChatResult{}, err
	}
	return chatProvider.Chat(ctx, request)
}

// GenerateAudio dispatches audio generation to the selected provider via the registry.
func (s *Service) GenerateAudio(ctx context.Context, id ProviderID, request AudioRequest) (AudioResult, error) {
	provider, err := s.registry.Provider(id)
	if err != nil {
		return AudioResult{}, err
	}
	if !provider.Descriptor().Supports(CapabilityAudio) {
		return AudioResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityAudio}
	}
	audioProvider, ok := provider.(AudioProvider)
	if !ok {
		return AudioResult{}, &UnsupportedCapabilityError{Provider: id, Capability: CapabilityAudio}
	}
	request, err = NormalizeAudioRequest(request)
	if err != nil {
		return AudioResult{}, err
	}
	return audioProvider.GenerateAudio(ctx, request)
}
