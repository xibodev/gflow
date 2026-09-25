package adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xibodev/gflow/pkg/application"
)

// DiscoveredAdapter holds metadata for an adapter executable found on the system.
type DiscoveredAdapter struct {
	ProviderID  application.ProviderID `json:"provider_id"`
	Command     string                 `json:"command"`
	Args        []string               `json:"args"`
	Descriptor  Descriptor             `json:"descriptor"`
}

var (
	discoveryMu    sync.Mutex
	cachedAdapters map[application.ProviderID]DiscoveredAdapter
)

// AdaptersDir returns the standard directory where adapter executables live (~/.gflow/adapters).
func AdaptersDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".gflow", "adapters")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// DiscoverAdapters scans ~/.gflow/adapters, PATH, and environment variables for
// installed gflow adapter binaries.
func DiscoverAdapters(ctx context.Context, coreVersion string) map[application.ProviderID]DiscoveredAdapter {
	discoveryMu.Lock()
	defer discoveryMu.Unlock()

	result := make(map[application.ProviderID]DiscoveredAdapter)

	// 1. Scan ~/.gflow/adapters/
	scanDirectory(ctx, AdaptersDir(), coreVersion, result)

	// 2. Check environment overrides: GFLOW_ADAPTER_COMMAND
	if cmd := os.Getenv(EnvCommand); cmd != "" {
		if desc, err := probeExecutable(ctx, cmd, nil, coreVersion); err == nil {
			result[application.ProviderID(desc.ProviderID)] = DiscoveredAdapter{
				ProviderID: application.ProviderID(desc.ProviderID),
				Command:    cmd,
				Descriptor: desc,
			}
		}
	}

	return result
}

// ResolveAdapter finds an adapter for the requested provider ID.
func ResolveAdapter(ctx context.Context, id application.ProviderID, coreVersion string) (Config, bool, error) {
	// Direct env override takes precedence
	cfg, configured, err := ConfigFromEnv(coreVersion)
	if err != nil {
		return Config{}, false, err
	}
	if configured {
		return cfg, true, nil
	}

	// Look in ~/.gflow/adapters/
	adaptersDir := AdaptersDir()
	if adaptersDir != "" {
		candidates := []string{
			filepath.Join(adaptersDir, fmt.Sprintf("gflow-adapter-%s", id)),
			filepath.Join(adaptersDir, fmt.Sprintf("gflow-adapter-%s.exe", id)),
			filepath.Join(adaptersDir, "gflow-adapter"),
			filepath.Join(adaptersDir, "gflow-adapter.exe"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				// Probe to confirm it provides the requested provider ID
				desc, err := probeExecutable(ctx, c, nil, coreVersion)
				if err == nil && (application.ProviderID(desc.ProviderID) == id || desc.ProviderID == "all") {
					return Config{
						Command:     c,
						CoreVersion: coreVersion,
					}, true, nil
				}
			}
		}
	}

	// Look on PATH
	binNames := []string{
		fmt.Sprintf("gflow-adapter-%s", id),
		"gflow-adapter",
	}
	for _, name := range binNames {
		if path, err := exec.LookPath(name); err == nil {
			desc, err := probeExecutable(ctx, path, nil, coreVersion)
			if err == nil && (application.ProviderID(desc.ProviderID) == id || desc.ProviderID == "all") {
				return Config{
					Command:     path,
					CoreVersion: coreVersion,
				}, true, nil
			}
		}
	}

	return Config{}, false, nil
}

func scanDirectory(ctx context.Context, dir string, coreVersion string, out map[application.ProviderID]DiscoveredAdapter) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasPrefix(name, "gflow-adapter") {
			continue
		}
		fullPath := filepath.Join(dir, e.Name())
		desc, err := probeExecutable(ctx, fullPath, nil, coreVersion)
		if err == nil {
			out[application.ProviderID(desc.ProviderID)] = DiscoveredAdapter{
				ProviderID: application.ProviderID(desc.ProviderID),
				Command:    fullPath,
				Descriptor: desc,
			}
		}
	}
}

func probeExecutable(ctx context.Context, command string, args []string, coreVersion string) (Descriptor, error) {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	client, err := Start(pctx, Config{
		Command:         command,
		Args:            args,
		CoreVersion:     coreVersion,
		CloseTimeout:    500 * time.Millisecond,
		MaxMessageBytes: 64 * 1024,
	})
	if err != nil {
		return Descriptor{}, err
	}
	defer client.Close()

	d := client.Descriptor()
	return Descriptor{
		ProviderID:      string(d.ID),
		DisplayName:     d.Name,
		Capabilities:    d.Capabilities,
		ProtocolVersion: ProtocolVersion,
	}, nil
}
