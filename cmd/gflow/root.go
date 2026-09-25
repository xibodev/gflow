package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "1.0.0"
	commit  = "none"
	date    = "unknown"

	jsonOutput   bool
	providerFlag string
)

var rootCmd = &cobra.Command{
	Use:   "gflow",
	Short: "gflow — Lean multi-provider CLI and protocol shell for AI media generation",
	Long: `gflow is an extensible CLI and MCP server for AI image, video, audio, and chat generation.
Capabilities are fulfilled out-of-process by installed provider adapters.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")
	rootCmd.PersistentFlags().StringVarP(&providerFlag, "provider", "P", "", "Provider backend: gemini (default), minimax, flow")

	rootCmd.AddCommand(historyCmd)
}

func getProvider() string {
	if providerFlag != "" {
		return providerFlag
	}
	if p := os.Getenv("GFLOW_PROVIDER"); p != "" {
		return p
	}
	return "gemini"
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
