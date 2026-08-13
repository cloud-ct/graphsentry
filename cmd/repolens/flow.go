package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/huandert/repolens/internal/diagram"
	"github.com/huandert/repolens/internal/graph"
)

// flowJSON is the --json shape for `flow`: the raw paths (for callers that
// want to build their own rendering, e.g. a webview) plus the ASCII and
// Mermaid renderings already computed, so simple consumers can just
// display them.
type flowJSON struct {
	Root    string              `json:"root"`
	Paths   [][]*graph.PathStep `json:"paths"`
	ASCII   string              `json:"ascii"`
	Mermaid string              `json:"mermaid"`
}

func newFlowCmd() *cobra.Command {
	var repoFlag string
	var depth int
	var mermaid bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "flow <endpoint|function>",
		Short: "Render the call-flow diagram starting from an endpoint or function (deterministic, no LLM)",
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
			paths := g.FlowPaths(id, depth)
			if asJSON {
				return printJSON(flowJSON{
					Root:    id,
					Paths:   paths,
					ASCII:   diagram.ASCIIFlow(paths),
					Mermaid: diagram.MermaidFlow(paths),
				})
			}
			fmt.Println(diagram.ASCIIFlow(paths))
			if mermaid {
				fmt.Println("\n```mermaid")
				fmt.Print(diagram.MermaidFlow(paths))
				fmt.Println("```")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to query (default: last analyzed)")
	cmd.Flags().IntVar(&depth, "depth", 5, "max hops to traverse")
	cmd.Flags().BoolVar(&mermaid, "mermaid", false, "also print a Mermaid diagram")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output structured JSON (paths + ascii + mermaid) instead of the human-readable rendering")
	return cmd
}
