package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/application"
)

var audioOutput string

var audioCmd = &cobra.Command{
	Use:     "audio <prompt>",
	Aliases: []string{"music"},
	Short:   "Generate a music track (delegated to configured provider adapter)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		provider := application.ProviderID(getProvider())
		service, closer, err := newMediaService(cmd.Context(), provider)
		if err != nil {
			return err
		}
		if closer != nil {
			defer closeWithError(&runErr, closer)
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
		defer cancel()

		fmt.Fprintf(os.Stderr, "Generating audio via %s: %q...\n", provider, args[0])
		result, err := service.GenerateAudio(ctx, provider, application.AudioRequest{Prompt: args[0], Output: audioOutput})
		if err != nil {
			return err
		}
		for _, w := range result.Warnings {
			fmt.Fprintln(os.Stderr, "Warning: "+w)
		}
		if jsonOutput {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		if result.Text != "" {
			fmt.Println(result.Text)
		}
		for _, a := range result.Assets {
			if a.LocalPath == "" {
				continue
			}
			abs, _ := filepath.Abs(a.LocalPath)
			fmt.Printf("Saved %s: %s\n", a.Type, abs)
		}
		return nil
	},
}

func init() {
	audioCmd.Flags().StringVarP(&audioOutput, "output", "o", "", "Output directory or filename")
	rootCmd.AddCommand(audioCmd)
}
