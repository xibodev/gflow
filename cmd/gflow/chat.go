package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/application"
)

var chatModel string

var chatCmd = &cobra.Command{
	Use:     "chat <prompt>",
	Aliases: []string{"ask"},
	Short:   "Chat with an AI provider (delegated to configured provider adapter)",
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
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		result, err := service.Chat(ctx, provider, application.ChatRequest{Prompt: args[0], Model: chatModel})
		if err != nil {
			return err
		}
		if result.Warning != "" {
			fmt.Fprintln(os.Stderr, "Warning: "+result.Warning)
		}
		if jsonOutput {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		fmt.Println(result.Text)
		return nil
	},
}

func init() {
	chatCmd.Flags().StringVarP(&chatModel, "model", "m", "", "Model to use (e.g. pro, flash, flash-lite)")
	rootCmd.AddCommand(chatCmd)
}
