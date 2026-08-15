package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cloud-ct/graphsentry/internal/diagram"
	"github.com/cloud-ct/graphsentry/internal/graph"
)

func newDependenciesCmd() *cobra.Command {
	var repoFlag string
	var depth int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "dependencies <symbol>",
		Short: "Show what a symbol itself depends on — the mirror image of impact (deterministic, no LLM)",
		Args:  cobra.ExactArgs(1),
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
			defer func() { _ = store.Close() }()
			g, err := store.Load()
			if err != nil {
				return err
			}
			id, err := findSymbol(g, args[0])
			if err != nil {
				return err
			}
			res := g.Dependencies(id, depth)
			if asJSON {
				return printJSON(res)
			}
			fmt.Print(diagram.ASCIIDependencies(res))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to query (default: last analyzed)")
	cmd.Flags().IntVar(&depth, "depth", 0, "max hops to traverse (0 = unlimited)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output structured JSON instead of the human-readable rendering")
	return cmd
}
