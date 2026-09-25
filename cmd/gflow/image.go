package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/config"
	"github.com/xibodev/gflow/pkg/history"
	"github.com/xibodev/gflow/pkg/models"
	"github.com/xibodev/gflow/pkg/util"
)

var (
	imgAspect string
	imgCount  int
	imgModel  string
	imgOutput string
	imgRef    string
	imgSeed   int64
)

var imageCmd = &cobra.Command{
	Use:     "image <prompt>",
	Aliases: []string{"img"},
	Short:   "Generate AI images (delegated to configured provider adapter)",
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
			seed = &imgSeed
		}
		request := application.ImageRequest{
			Prompt:    args[0],
			Aspect:    imgAspect,
			Count:     imgCount,
			Model:     imgModel,
			Output:    imgOutput,
			Reference: imgRef,
			Seed:      seed,
		}
		callCtx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()

		result, err := service.GenerateImage(callCtx, provider, request)
		if err != nil {
			return err
		}
		return saveApplicationAssets(callCtx, result.Assets, request.Output, "image", request.Prompt, request.Aspect, request.Model)
	},
}

func saveApplicationAssets(ctx context.Context, assets []application.Asset, output, typ, prompt, aspect, model string) error {
	if output == "" {
		output = config.LoadConfig().OutputDir
	}
	var saved []models.Asset
	var saveErrs []string
	for i, asset := range assets {
		normalized := models.Asset(asset)
		path, err := util.SaveAssetIndexed(ctx, &normalized, output, i, len(assets))
		if err != nil {
			saveErrs = append(saveErrs, fmt.Sprintf("asset %d: %v", i+1, err))
			continue
		}
		saved = append(saved, normalized)
		abs, _ := filepath.Abs(path)
		fmt.Fprintf(os.Stderr, "Saved: %s\n", abs)
		if err := history.Add(history.Entry{ID: normalized.ID, Type: typ, Prompt: prompt, LocalPath: path, URL: normalized.URL, Aspect: aspect, Model: model}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: saved %s but history failed: %v\n", path, err)
		}
	}
	if len(saved) == 0 {
		return fmt.Errorf("no %ss saved (errors: %v)", typ, saveErrs)
	}
	if jsonOutput {
		data, _ := json.MarshalIndent(saved, "", "  ")
		fmt.Println(string(data))
	}
	if len(saveErrs) > 0 {
		return fmt.Errorf("partial save failures: %v", saveErrs)
	}
	return nil
}

func init() {
	imageCmd.Flags().StringVarP(&imgAspect, "aspect", "a", "landscape", "Aspect ratio: landscape, square, portrait, 4:3, 3:4")
	imageCmd.Flags().IntVarP(&imgCount, "count", "c", 1, "Number of images (1-4)")
	imageCmd.Flags().StringVarP(&imgModel, "model", "m", "", "Image model override")
	imageCmd.Flags().StringVarP(&imgOutput, "output", "o", "", "Output directory or filename")
	imageCmd.Flags().StringVar(&imgRef, "ref", "", "Reference image path or media ID")
	imageCmd.Flags().Int64Var(&imgSeed, "seed", 0, "Seed for reproducible generation")
	rootCmd.AddCommand(imageCmd)
}
