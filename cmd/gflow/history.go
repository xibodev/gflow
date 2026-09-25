package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/history"
)

var historyLimit int

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View recently generated images and videos",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := history.List(historyLimit)
		if err != nil {
			return err
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(entries, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(entries) == 0 {
			fmt.Println("No generations recorded yet. Try: gflow image \"a sunset\"")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tTYPE\tID\tPROMPT\tLOCAL PATH")
		fmt.Fprintln(w, "----\t----\t--\t------\t----------")

		for _, e := range entries {
			timeStr := e.CreatedAt.Format("01/02 15:04")
			promptTrunc := e.Prompt
			if len(promptTrunc) > 36 {
				promptTrunc = promptTrunc[:33] + "..."
			}
			idTrunc := e.ID
			if len(idTrunc) > 12 {
				idTrunc = idTrunc[:12]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", timeStr, e.Type, idTrunc, promptTrunc, e.LocalPath)
		}
		return w.Flush()
	},
}

func init() {
	historyCmd.Flags().IntVarP(&historyLimit, "limit", "l", 20, "Number of history entries to show")
}
