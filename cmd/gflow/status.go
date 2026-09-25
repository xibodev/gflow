package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check status of configured and installed AI provider adapters",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		discovered := adapter.DiscoverAdapters(ctx, version)
		if len(discovered) == 0 {
			if jsonOutput {
				fmt.Println("{}")
				return nil
			}
			fmt.Println("=== AI Providers Status ===")
			fmt.Println()
			fmt.Println("No provider adapters installed.")
			fmt.Println()
			fmt.Printf("To install adapters, place adapter binaries in %s or on PATH.\n", adapter.AdaptersDir())
			fmt.Println("See docs/adapter-protocol.md to configure or build custom provider adapters.")
			return nil
		}

		byProvider := map[string]application.AuthStatus{}
		for id, da := range discovered {
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			client, err := adapter.Start(pctx, ConfigFromDiscovered(da))
			if err != nil {
				cancel()
				byProvider[string(id)] = application.NormalizeAuthStatus(application.AuthStatus{
					Provider: id,
					Ready:    false,
					Summary:  fmt.Sprintf("Failed to start adapter: %v", err),
				})
				continue
			}

			if !client.Descriptor().Supports(application.CapabilityAuthStatus) {
				client.Close()
				cancel()
				byProvider[string(id)] = application.NormalizeAuthStatus(application.AuthStatus{
					Provider: id,
					Ready:    true,
					Summary:  "Adapter running (auth-status not declared)",
				})
				continue
			}

			descCtx, cancelDesc := context.WithTimeout(pctx, 8*time.Second)
			payload, err := client.AuthDescribe(descCtx)
			cancelDesc()
			client.Close()
			cancel()

			if err != nil {
				byProvider[string(id)] = application.NormalizeAuthStatus(application.AuthStatus{
					Provider: id,
					Ready:    false,
					Summary:  fmt.Sprintf("Status probe failed: %v", err),
				})
				continue
			}

			byProvider[string(id)] = application.NormalizeAuthStatus(application.AuthStatus{
				Provider:  id,
				Ready:     payload.Ready,
				Summary:   payload.Summary,
				Checks:    toAppChecks(payload.Checks),
				NextSteps: payload.NextSteps,
			})
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(byProvider, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Println("=== AI Providers Status ===")
		fmt.Println()
		for _, st := range byProvider {
			printAuthStatus(cmd, st)
		}
		fmt.Println("Hint: run `gflow login -P <provider>` for provider-specific setup steps.")
		return nil
	},
}

func ConfigFromDiscovered(da adapter.DiscoveredAdapter) adapter.Config {
	return adapter.Config{
		Command:         da.Command,
		Args:            da.Args,
		CoreVersion:     version,
		CloseTimeout:    2 * time.Second,
		MaxMessageBytes: 1024 * 1024,
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
