package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xibodev/gflow/pkg/application"
)

type imageProvider struct {
	descriptor application.Descriptor
	request    application.ImageRequest
}

func (p *imageProvider) Descriptor() application.Descriptor { return p.descriptor }

func (p *imageProvider) GenerateImage(_ context.Context, request application.ImageRequest) (application.ImageResult, error) {
	p.request = request
	return application.ImageResult{Assets: []application.Asset{{ID: "image-1"}}}, nil
}

type descriptorOnlyProvider struct {
	descriptor application.Descriptor
}

func (p descriptorOnlyProvider) Descriptor() application.Descriptor { return p.descriptor }

type asyncProvider struct {
	descriptor application.Descriptor
	request    application.VideoRequest
	polledID   string
}

func (p *asyncProvider) Descriptor() application.Descriptor { return p.descriptor }

func (p *asyncProvider) SubmitVideoOperation(_ context.Context, request application.VideoRequest) (application.VideoOperation, error) {
	p.request = request
	return application.VideoOperation{ID: "operation-1"}, nil
}

func (p *asyncProvider) PollVideoOperation(_ context.Context, operationID string) (application.VideoOperation, error) {
	p.polledID = operationID
	return application.VideoOperation{ID: operationID, Status: "running"}, nil
}

func TestRegistryRejectsDuplicateRegistration(t *testing.T) {
	registry := application.NewRegistry()
	provider := descriptorOnlyProvider{descriptor: application.Descriptor{ID: application.ProviderFlow}}
	if err := registry.Register(provider); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := registry.Register(provider); !errors.Is(err, application.ErrDuplicateProvider) {
		t.Fatalf("duplicate registration error = %v, want ErrDuplicateProvider", err)
	}
}

func TestRegistryReturnsUnknownProviderError(t *testing.T) {
	_, err := application.NewRegistry().Provider("missing")
	if !errors.Is(err, application.ErrUnknownProvider) {
		t.Fatalf("error = %v, want ErrUnknownProvider", err)
	}
	var unknown *application.UnknownProviderError
	if !errors.As(err, &unknown) || unknown.Provider != "missing" {
		t.Fatalf("error = %#v, want provider-specific error", err)
	}
}

func TestDescriptorCapabilities(t *testing.T) {
	descriptor := application.Descriptor{
		ID:           application.ProviderGemini,
		Capabilities: []application.Capability{application.CapabilityImageGeneration},
	}
	if !descriptor.Supports(application.CapabilityImageGeneration) {
		t.Fatal("image generation capability not reported")
	}
	if descriptor.Supports(application.CapabilityVideoGeneration) {
		t.Fatal("undeclared video generation capability reported")
	}
}

func TestServiceDispatchesImageGeneration(t *testing.T) {
	provider := &imageProvider{descriptor: application.Descriptor{
		ID:           application.ProviderGemini,
		Capabilities: []application.Capability{application.CapabilityImageGeneration},
	}}
	registry := application.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(registry)
	result, err := service.GenerateImage(context.Background(), application.ProviderGemini, application.ImageRequest{Prompt: "test prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.request.Prompt != "test prompt" || len(result.Assets) != 1 || result.Assets[0].ID != "image-1" {
		t.Fatalf("dispatch request/result mismatch: request=%+v result=%+v", provider.request, result)
	}
}

func TestServiceRejectsUnsupportedCapability(t *testing.T) {
	registry := application.NewRegistry()
	if err := registry.Register(descriptorOnlyProvider{descriptor: application.Descriptor{ID: application.ProviderMiniMax}}); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(registry)
	_, err := service.GenerateImage(context.Background(), application.ProviderMiniMax, application.ImageRequest{})
	if !errors.Is(err, application.ErrUnsupportedCapability) {
		t.Fatalf("error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestServiceDispatchesAsyncVideoOperations(t *testing.T) {
	provider := &asyncProvider{descriptor: application.Descriptor{
		ID:           "async",
		Capabilities: []application.Capability{application.CapabilityVideoSubmit, application.CapabilityVideoPoll},
	}}
	registry := application.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(registry)
	submission, err := service.SubmitVideoOperation(context.Background(), "async", application.VideoRequest{Prompt: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if submission.ID != "operation-1" || provider.request.Duration != 10 {
		t.Fatalf("submission/request = %+v / %+v", submission, provider.request)
	}
	poll, err := service.PollVideoOperation(context.Background(), "async", submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if poll.Status != "running" || provider.polledID != submission.ID {
		t.Fatalf("poll/provider ID = %+v / %q", poll, provider.polledID)
	}
}

func TestServiceRejectsMissingAsyncCapabilityOrImplementation(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []application.Capability
		submit       bool
	}{
		{name: "undeclared submit", capabilities: []application.Capability{application.CapabilityVideoPoll}, submit: true},
		{name: "undeclared poll", capabilities: []application.Capability{application.CapabilityVideoSubmit}},
		{name: "declared without implementation", capabilities: []application.Capability{application.CapabilityVideoSubmit}, submit: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := application.NewRegistry()
			if err := registry.Register(descriptorOnlyProvider{descriptor: application.Descriptor{ID: "limited", Capabilities: test.capabilities}}); err != nil {
				t.Fatal(err)
			}
			service := application.NewService(registry)
			var err error
			if test.submit {
				_, err = service.SubmitVideoOperation(context.Background(), "limited", application.VideoRequest{Prompt: "video"})
			} else {
				_, err = service.PollVideoOperation(context.Background(), "limited", "operation-1")
			}
			if !errors.Is(err, application.ErrUnsupportedCapability) {
				t.Fatalf("error = %v, want ErrUnsupportedCapability", err)
			}
		})
	}
}

func TestRequestNormalization(t *testing.T) {
	image, err := application.NormalizeImageRequest(application.ImageRequest{Prompt: "image"})
	if err != nil || image.Count != 1 {
		t.Fatalf("image normalization = %+v, %v", image, err)
	}
	video, err := application.NormalizeVideoRequest(application.VideoRequest{Prompt: "video"})
	if err != nil || video.Duration != 10 {
		t.Fatalf("video normalization = %+v, %v", video, err)
	}
	badSeed := application.MaxSeed + 1
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "empty image prompt", err: func() error { _, err := application.NormalizeImageRequest(application.ImageRequest{}); return err }()},
		{name: "image count", err: func() error {
			_, err := application.NormalizeImageRequest(application.ImageRequest{Prompt: "x", Count: 5})
			return err
		}()},
		{name: "image seed", err: func() error {
			_, err := application.NormalizeImageRequest(application.ImageRequest{Prompt: "x", Seed: &badSeed})
			return err
		}()},
		{name: "video duration", err: func() error {
			_, err := application.NormalizeVideoRequest(application.VideoRequest{Prompt: "x", Duration: 5})
			return err
		}()},
		{name: "video end", err: func() error {
			_, err := application.NormalizeVideoRequest(application.VideoRequest{Prompt: "x", End: "end"})
			return err
		}()},
	} {
		if !errors.Is(test.err, application.ErrValidation) {
			t.Errorf("%s error = %v, want ErrValidation", test.name, test.err)
		}
	}
	if _, err := application.NormalizeVideoRequest(application.VideoRequest{Prompt: "x", Resolution: "provider-specific"}); err != nil {
		t.Fatalf("core rejected provider-specific resolution: %v", err)
	}
}
