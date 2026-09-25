package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
)

func closeWithError(result *error, closer io.Closer) {
	if closer == nil {
		return
	}
	*result = errors.Join(*result, closer.Close())
}

// newMediaService resolves an adapter for the requested provider and registers it.
// All provider operations (image, video, audio, chat, auth) are handled out-of-process
// by the installed adapter executable.
func newMediaService(ctx context.Context, selected application.ProviderID) (*application.Service, io.Closer, error) {
	cfg, found, err := adapter.ResolveAdapter(ctx, selected, version)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, fmt.Errorf("%w: provider %q is not installed.\n"+
			"To install adapters:\n"+
			"  • place adapter executables in %s\n"+
			"  • or install on PATH\n"+
			"  • or set GFLOW_ADAPTER_COMMAND=<path>\n"+
			"See docs/adapter-protocol.md to configure or build adapters.",
			application.ErrUnknownProvider, selected, adapter.AdaptersDir())
	}

	client, err := adapter.Start(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("start adapter for %q: %w", selected, err)
	}

	actualID := client.Descriptor().ID
	if actualID != selected && actualID != "all" {
		_ = client.Close()
		return nil, nil, fmt.Errorf("adapter provider ID %q does not match selected provider %q", actualID, selected)
	}

	registry := application.NewRegistry()
	if err := registry.Register(client); err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	return application.NewService(registry), client, nil
}
