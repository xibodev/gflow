package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/application"
)

var (
	vidAspect     string
	vidDuration   int
	vidModel      string
	vidResolution string
	vidOutput     string
	vidStart      string
	vidEnd        string
	vidSeed       int64
)

const adapterVideoPollInterval = 2 * time.Second

var videoCmd = &cobra.Command{
	Use:     "video <prompt>",
	Aliases: []string{"vid"},
	Short:   "Generate AI videos (delegated to configured provider adapter)",
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
		var seed *int64
		if cmd.Flags().Changed("seed") {
			seed = &vidSeed
		}
		request := application.VideoRequest{
			Prompt:     args[0],
			Aspect:     vidAspect,
			Duration:   vidDuration,
			Model:      vidModel,
			Resolution: vidResolution,
			Output:     vidOutput,
			Start:      vidStart,
			End:        vidEnd,
			Seed:       seed,
		}
		callCtx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
		defer cancel()

		op, err := service.SubmitVideoOperation(callCtx, provider, request)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Video operation submitted: %s. Polling for completion...\n", op.ID)

		for {
			op, err = service.PollVideoOperation(callCtx, provider, op.ID)
			if err != nil {
				return err
			}
			switch op.Status {
			case "succeeded":
				if !jsonOutput {
					fmt.Fprintln(os.Stderr)
				}
				return saveApplicationAssets(callCtx, op.Assets, request.Output, "video", request.Prompt, request.Aspect, request.Model)
			case "failed":
				if !jsonOutput {
					fmt.Fprintln(os.Stderr)
				}
				msg := op.Error
				if msg == "" {
					msg = "adapter reported failure"
				}
				return fmt.Errorf("video generation failed: %s", msg)
			case "queued", "running":
				if !jsonOutput {
					fmt.Fprint(os.Stderr, ".")
				}
			}
			timer := time.NewTimer(adapterVideoPollInterval)
			select {
			case <-callCtx.Done():
				timer.Stop()
				return callCtx.Err()
			case <-timer.C:
			}
		}
	},
}

func init() {
	videoCmd.Flags().StringVarP(&vidAspect, "aspect", "a", "landscape", "Aspect ratio: landscape, portrait, square")
	videoCmd.Flags().IntVarP(&vidDuration, "duration", "d", 10, "Duration in seconds: 4, 6, 8, 10")
	videoCmd.Flags().StringVarP(&vidModel, "model", "m", "", "Video model override")
	videoCmd.Flags().StringVarP(&vidResolution, "resolution", "r", "720p", "Resolution (e.g. 720p, 1080p, 4k)")
	videoCmd.Flags().StringVarP(&vidOutput, "output", "o", "", "Output directory or filename")
	videoCmd.Flags().StringVar(&vidStart, "start", "", "Start frame image path or media ID")
	videoCmd.Flags().StringVar(&vidEnd, "end", "", "End frame image path or media ID")
	videoCmd.Flags().Int64Var(&vidSeed, "seed", 0, "Seed for reproducible generation")
	rootCmd.AddCommand(videoCmd)
}
