package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/config"
	"github.com/xibodev/gflow/pkg/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the Model Context Protocol (MCP) server over stdio",
	Long: `Starts an MCP stdio server compatible with Claude Desktop, Cursor, OpenCode, Cline, and Windsurf.
Provider capabilities are fulfilled by installed provider adapters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.LoadConfig()
		prov := application.ProviderID(getProvider())

		adapterConfig, found, err := adapter.ResolveAdapter(cmd.Context(), prov, version)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: provider %q is not installed.\n"+
				"To install adapters:\n"+
				"  • place adapter executables (e.g. gflow-adapter-%s) in %s\n"+
				"  • or install on PATH\n"+
				"  • or set GFLOW_ADAPTER_COMMAND=<path>\n"+
				"See https://github.com/xibodev/gflow-adapters for official adapters",
				application.ErrUnknownProvider, prov, prov, adapter.AdaptersDir())
		}

		client, err := adapter.Start(cmd.Context(), adapterConfig)
		if err != nil {
			return fmt.Errorf("start adapter for %q: %w", prov, err)
		}
		defer client.Close()

		backend, err := adapter.NewBackend(client, cfg.OutputDir)
		if err != nil {
			return err
		}
		srv := mcp.NewServerWithCapabilities(application.NewMediaService(backend, application.DefaultVideoQueueCapacity))
		return srv.Run()
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
