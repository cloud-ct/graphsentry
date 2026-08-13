package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/huandert/repolens/internal/graph"
)

func newCouplingCmd() *cobra.Command {
	var repoFlag string
	var top int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "coupling",
		Short: "Rank the most coupled modules/symbols by fan-in + fan-out (deterministic, no LLM)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := resolveTarget(repoFlag)
			if err != nil {
				return err
			}
			dbPath, err := requireDB(target)
			if err != nil {
				return err
			}
			store, err := graph.OpenStore(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()
			g, err := store.Load()
			if err != nil {
				return err
			}
			scores := g.TopCoupled(top)
			if asJSON {
				return printJSON(scores)
			}
			if len(scores) == 0 {
				fmt.Println("(no coupling data — did analyze find any calls/imports?)")
				return nil
			}
			fmt.Printf("%-40s %-10s %8s %8s %8s\n", "SYMBOL", "KIND", "FAN-IN", "FAN-OUT", "TOTAL")
			for _, s := range scores {
				fmt.Printf("%-40s %-10s %8d %8d %8d\n", truncate(s.Node.Name, 40), s.Node.Kind, s.FanIn, s.FanOut, s.Total)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to query (default: last analyzed)")
	cmd.Flags().IntVar(&top, "top", 10, "number of results to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output structured JSON instead of the human-readable rendering")
	return cmd
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
