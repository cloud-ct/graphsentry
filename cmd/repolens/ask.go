package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/huandert/repolens/internal/ai"
	"github.com/huandert/repolens/internal/graph"
)

func newAskCmd() *cobra.Command {
	var repoFlag, rootFlag string
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask a natural-language question about the repository's architecture (requires an LLM key — BYOK)",
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
			provider, err := ai.NewProviderFromEnv()
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
			return runAsk(cmd, provider, g, args[0], rootFlag)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to query (default: last analyzed)")
	cmd.Flags().StringVar(&rootFlag, "root", "", "scope the question to the subgraph around this symbol ID, instead of searching the whole repo by keyword (used by the VS Code extension's flow-panel Ask box)")
	return cmd
}

// runAsk implements the ask pipeline: find the symbols relevant to the
// question, expand a bounded subgraph around them, and hand only that
// subgraph — never the whole repo — to the LLM for narration + diagramming.
//
// Relevant symbols come from one of two sources: if root is set, it's used
// directly as the sole seed (exact node-ID match, no searching) — this is
// what lets a question asked from an already-open flow/impact view stay
// scoped to what's on screen instead of the CLI re-discovering a
// (possibly unrelated) subgraph from scratch. Otherwise, falls back to
// lexical match against node names — a lightweight stand-in for embedding
// search until the index package's cache is populated.
func runAsk(cmd *cobra.Command, provider ai.Provider, g *graph.Graph, question, root string) error {
	var seeds []string
	if root != "" {
		if _, ok := g.Nodes[root]; !ok {
			return fmt.Errorf("--root %q is not a known symbol in this graph (has it been renamed or removed since the flow view was opened? try re-analyzing)", root)
		}
		seeds = []string{root}
	} else {
		seeds = lexicalMatch(g, question, 5)
		if len(seeds) == 0 {
			fmt.Println("Couldn't find any symbols related to that question in the graph. Try mentioning a specific function, class, or endpoint name.")
			return nil
		}
	}

	sub := expandSubgraph(g, seeds, 2)
	context := ai.SerializeSubgraph(sub)

	answer, err := provider.Ask(cmd.Context(), ai.AskRequest{
		Question:     question,
		GraphContext: context,
	})
	if err != nil {
		return err
	}

	fmt.Println(answer.Explanation)
	if answer.Mermaid != "" {
		fmt.Println("\n```mermaid")
		fmt.Println(answer.Mermaid)
		fmt.Println("```")
	}
	if answer.ASCII != "" {
		fmt.Println("\n" + answer.ASCII)
	}
	return nil
}

func lexicalMatch(g *graph.Graph, question string, limit int) []string {
	words := strings.Fields(strings.ToLower(question))
	scored := map[string]int{}
	for id, n := range g.Nodes {
		name := strings.ToLower(n.Name)
		for _, w := range words {
			if len(w) < 3 {
				continue
			}
			if strings.Contains(name, w) || strings.Contains(w, name) {
				scored[id]++
			}
		}
	}
	type kv struct {
		id    string
		score int
	}
	var list []kv
	for id, s := range scored {
		list = append(list, kv{id, s})
	}
	// simple selection of top-N by score
	var out []string
	for len(out) < limit && len(list) > 0 {
		bestIdx := 0
		for i, v := range list {
			if v.score > list[bestIdx].score {
				bestIdx = i
			}
		}
		out = append(out, list[bestIdx].id)
		list = append(list[:bestIdx], list[bestIdx+1:]...)
	}
	return out
}

// expandSubgraph collects seeds plus their neighbors (both directions) up
// to depth hops, returning a small Graph suitable for sending to the LLM.
func expandSubgraph(g *graph.Graph, seeds []string, depth int) *graph.Graph {
	visited := map[string]bool{}
	queue := make([]struct {
		id string
		d  int
	}, 0, len(seeds))
	for _, s := range seeds {
		visited[s] = true
		queue = append(queue, struct {
			id string
			d  int
		}{s, 0})
	}
	sub := graph.New()
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if n, ok := g.Nodes[cur.id]; ok {
			sub.AddNode(n)
		}
		if cur.d >= depth {
			continue
		}
		for _, e := range append(g.Out(cur.id), g.In(cur.id)...) {
			other := e.To
			if e.To == cur.id {
				other = e.From
			}
			sub.AddEdge(e.From, e.To, e.Kind)
			if !visited[other] {
				visited[other] = true
				queue = append(queue, struct {
					id string
					d  int
				}{other, cur.d + 1})
			}
		}
	}
	return sub
}
