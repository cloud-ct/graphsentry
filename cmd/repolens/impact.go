package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cloud-ct/repolens/internal/diagram"
	"github.com/cloud-ct/repolens/internal/graph"
)

func newImpactCmd() *cobra.Command {
	var repoFlag string
	var depth int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Show what depends on a symbol — what could break if you change it (deterministic, no LLM)",
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
			res := g.Impact(id, depth)
			if asJSON {
				return printJSON(res)
			}
			fmt.Print(diagram.ASCIIImpact(res))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to query (default: last analyzed)")
	cmd.Flags().IntVar(&depth, "depth", 0, "max hops to traverse (0 = unlimited)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output structured JSON instead of the human-readable rendering")
	return cmd
}

// findSymbol looks up a node by exact ID, then by exact name, then by a
// case-insensitive substring match on name — erroring with the candidate
// list when the query is ambiguous.
func findSymbol(g *graph.Graph, query string) (string, error) {
	if _, ok := g.Nodes[query]; ok {
		return query, nil
	}
	var exact []string
	var partial []string
	lowerQ := strings.ToLower(query)
	for id, n := range g.Nodes {
		if n.Name == query {
			exact = append(exact, id)
		} else if strings.Contains(strings.ToLower(n.Name), lowerQ) {
			partial = append(partial, id)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) > 1:
		return "", fmt.Errorf("ambiguous symbol %q, matches: %s", query, strings.Join(exact, ", "))
	case len(partial) == 1:
		return partial[0], nil
	case len(partial) > 1:
		return "", fmt.Errorf("ambiguous symbol %q, matches: %s", query, strings.Join(partial, ", "))
	default:
		return "", fmt.Errorf("no symbol found matching %q", query)
	}
}
