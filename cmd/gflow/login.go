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
	"github.com/xibodev/gflow/pkg/adapter"
	"github.com/xibodev/gflow/pkg/application"
)

var (
	loginAll    bool
	loginLaunch bool
)

var loginCmd = &cobra.Command{
	Use:     "login",
	Aliases: []string{"auth", "signin"},
	Short:   "Check provider login status and guide setup via installed adapter",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if loginAll {
			return runLoginAll(cmd, ctx)
		}
		selected := application.ProviderID(getProvider())
		return runLoginOne(cmd, ctx, selected)
	},
}

func init() {
	loginCmd.Flags().BoolVar(&loginAll, "all", false, "Check all discovered providers")
	loginCmd.Flags().BoolVar(&loginLaunch, "launch", false, "Let the adapter start its interactive sign-in surface and wait for sign-in")
	rootCmd.AddCommand(loginCmd)
}

func interruptCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

func runLoginAll(cmd *cobra.Command, ctx context.Context) error {
	discovered := adapter.DiscoverAdapters(ctx, version)
	if len(discovered) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No provider adapters installed in ~/.gflow/adapters or PATH.")
		fmt.Fprintln(cmd.OutOrStdout(), "See docs/adapter-protocol.md to install or write an adapter.")
		return nil
	}

	for id := range discovered {
		_ = runLoginOne(cmd, ctx, id)
	}
	return nil
}

func runLoginOne(cmd *cobra.Command, ctx context.Context, selected application.ProviderID) error {
	cfg, found, err := adapter.ResolveAdapter(ctx, selected, version)
	if err != nil {
		return err
	}
	if !found {
		return &application.UnknownProviderError{Provider: selected}
	}

	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client, err := adapter.Start(startCtx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	if !client.Descriptor().Supports(application.CapabilityAuthStatus) {
		status := application.NormalizeAuthStatus(application.AuthStatus{
			Provider: selected,
			Ready:    true,
			Summary:  "Adapter active (does not declare auth-status capability)",
		})
		printAuthStatus(cmd, status)
		return nil
	}

	describeCtx, cancelDescribe := context.WithTimeout(ctx, 15*time.Second)
	defer cancelDescribe()
	payload, err := client.AuthDescribe(describeCtx)
	if err != nil {
		return fmt.Errorf("adapter auth.describe failed: %w", err)
	}
	status := application.NormalizeAuthStatus(application.AuthStatus{
		Provider:  selected,
		Ready:     payload.Ready,
		Summary:   payload.Summary,
		Checks:    toAppChecks(payload.Checks),
		NextSteps: payload.NextSteps,
	})

	if !loginLaunch || !client.Descriptor().Supports(application.CapabilityAuthLogin) {
		if jsonOutput {
			data, _ := json.MarshalIndent(status, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}
		printAuthStatus(cmd, status)
		return nil
	}

	loginCtx, cancelLogin := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelLogin()
	loginPayload, err := client.AuthLogin(loginCtx)
	if err != nil {
		printAuthStatus(cmd, status)
		return fmt.Errorf("adapter auth.login failed: %w", err)
	}
	loginStatus := application.NormalizeAuthStatus(application.AuthStatus{
		Provider:  selected,
		Ready:     loginPayload.Ready,
		Summary:   loginPayload.Summary,
		Checks:    toAppChecks(loginPayload.Checks),
		NextSteps: loginPayload.NextSteps,
	})
	if jsonOutput {
		data, _ := json.MarshalIndent(loginStatus, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}
	printAuthStatus(cmd, loginStatus)
	return nil
}

func printAuthStatus(cmd *cobra.Command, status application.AuthStatus) {
	out := cmd.OutOrStdout()
	state := "[OK] Ready"
	if !status.Ready {
		state = "[--] Not ready"
	}
	fmt.Fprintf(out, "[%s] %s\n", status.Provider, state)
	if status.Summary != "" {
		fmt.Fprintf(out, "  Summary: %s\n", status.Summary)
	}
	for _, c := range status.Checks {
		mark := "[OK]"
		if !c.OK {
			mark = "[--]"
		}
		if c.Detail != "" {
			fmt.Fprintf(out, "  %s %s: %s\n", mark, c.Name, c.Detail)
		} else {
			fmt.Fprintf(out, "  %s %s\n", mark, c.Name)
		}
	}
	for _, step := range status.NextSteps {
		fmt.Fprintf(out, "  Next: %s\n", step)
	}
	fmt.Fprintln(out)
}

func toAppChecks(checks []adapter.AuthCheck) []application.AuthCheck {
	out := make([]application.AuthCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, application.AuthCheck{Name: c.Name, OK: c.OK, Detail: c.Detail})
	}
	return out
}
