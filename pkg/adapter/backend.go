package adapter

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/history"
	"github.com/xibodev/gflow/pkg/models"
	"github.com/xibodev/gflow/pkg/util"
)

const DefaultPollInterval = 2 * time.Second

// Backend adapts an external application provider to MediaCapabilities through
// MediaService. It owns the adapter client and closes it with the worker.
type Backend struct {
	client       *Client
	service      *application.Service
	provider     application.ProviderID
	output       string
	pollInterval time.Duration
	historyAdd   func(history.Entry) error
}

type BackendOption func(*Backend)

func WithPollInterval(interval time.Duration) BackendOption {
	return func(backend *Backend) {
		if interval > 0 {
			backend.pollInterval = interval
		}
	}
}

func WithHistoryWriter(writer func(history.Entry) error) BackendOption {
	return func(backend *Backend) {
		if writer != nil {
			backend.historyAdd = writer
		}
	}
}

func NewBackend(client *Client, output string, options ...BackendOption) (*Backend, error) {
	if client == nil {
		return nil, fmt.Errorf("adapter client is required")
	}
	registry := application.NewRegistry()
	if err := registry.Register(client); err != nil {
		return nil, err
	}
	backend := &Backend{
		client:       client,
		service:      application.NewService(registry),
		provider:     client.Descriptor().ID,
		output:       output,
		pollInterval: DefaultPollInterval,
		historyAdd:   history.Add,
	}
	for _, option := range options {
		option(backend)
	}
	return backend, nil
}

func (b *Backend) GenerateImage(ctx context.Context, request application.ImageRequest) (application.ImageOutput, error) {
	result, err := b.service.GenerateImage(ctx, b.provider, request)
	if err != nil {
		return application.ImageOutput{}, err
	}
	files, warnings := b.saveAssets(ctx, result.Assets, request.Output, "image", request.Prompt, request.Aspect, request.Model)
	if len(files) == 0 {
		return application.ImageOutput{}, fmt.Errorf("generation produced no downloadable assets (save errors: %v)", warnings)
	}
	abs, _ := filepath.Abs(files[0])
	return application.ImageOutput{FilePath: abs, Files: files, Warnings: warnings}, nil
}

func (b *Backend) GenerateVideo(ctx context.Context, request application.VideoRequest) (application.VideoOutput, error) {
	operation, err := b.service.SubmitVideoOperation(ctx, b.provider, request)
	if err != nil {
		return application.VideoOutput{}, err
	}
	for {
		operation, err = b.service.PollVideoOperation(ctx, b.provider, operation.ID)
		if err != nil {
			return application.VideoOutput{}, err
		}
		switch operation.Status {
		case "succeeded":
			files, warnings := b.saveAssets(ctx, operation.Assets, request.Output, "video", request.Prompt, request.Aspect, request.Model)
			if len(files) == 0 {
				return application.VideoOutput{}, fmt.Errorf("generation produced no downloadable assets (save errors: %v)", warnings)
			}
			abs, _ := filepath.Abs(files[0])
			return application.VideoOutput{FilePath: abs, Resolution: request.Resolution}, nil
		case "failed":
			if operation.Error == "" {
				operation.Error = "adapter reported failure"
			}
			return application.VideoOutput{}, fmt.Errorf("video generation failed: %s", operation.Error)
		case "queued", "running":
		}
		timer := time.NewTimer(b.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return application.VideoOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *Backend) UpsampleVideo(context.Context, application.UpsampleRequest) (application.UpsampleOutput, error) {
	return application.UpsampleOutput{}, fmt.Errorf("upsampling is unavailable with external adapters in protocol version %s", ProtocolVersion)
}

func (b *Backend) Chat(ctx context.Context, request application.ChatRequest) (application.ChatResult, error) {
	return b.service.Chat(ctx, b.provider, request)
}

func (b *Backend) GenerateAudio(ctx context.Context, request application.AudioRequest) (application.AudioResult, error) {
	result, err := b.service.GenerateAudio(ctx, b.provider, request)
	if err != nil {
		return application.AudioResult{}, err
	}
	var saved []application.Asset
	var warnings []string
	for _, asset := range result.Assets {
		files, warns := b.saveAssets(ctx, []application.Asset{asset}, request.Output, asset.Type, request.Prompt, "", "")
		warnings = append(warnings, warns...)
		if len(files) > 0 {
			asset.LocalPath = files[0]
			saved = append(saved, asset)
		}
	}
	if len(saved) == 0 {
		return application.AudioResult{}, fmt.Errorf("generation produced no downloadable audio (save errors: %v)", warnings)
	}
	return application.AudioResult{Text: result.Text, Assets: saved, Warnings: warnings}, nil
}

func (b *Backend) ProviderStatus(context.Context, int) (application.StatusOutput, error) {
	descriptor := b.client.AdapterDescriptor()
	return application.StatusOutput{Data: map[string]any{
		"provider":         descriptor.ProviderID,
		"display_name":     descriptor.DisplayName,
		"adapter_version":  descriptor.AdapterVersion,
		"protocol_version": descriptor.ProtocolVersion,
		"capabilities":     descriptor.Capabilities,
	}}, nil
}

func (b *Backend) Close() error { return b.client.Close() }

func (b *Backend) saveAssets(ctx context.Context, assets []application.Asset, output, typ, prompt, aspect, model string) ([]string, []string) {
	if output == "" {
		output = b.output
	}
	var files, warnings []string
	for i, asset := range assets {
		normalized := models.Asset(asset)
		path, err := util.SaveAssetIndexed(ctx, &normalized, output, i, len(assets))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("asset %d: %v", i+1, err))
			continue
		}
		files = append(files, path)
		if err := b.historyAdd(history.Entry{ID: normalized.ID, Type: typ, Prompt: prompt, LocalPath: path, URL: normalized.URL, Aspect: aspect, Model: model}); err != nil {
			warnings = append(warnings, fmt.Sprintf("history for asset %d: %v", i+1, err))
		}
	}
	return files, warnings
}
