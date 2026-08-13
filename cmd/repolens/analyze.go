package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/huandert/repolens/internal/graph"
	"github.com/huandert/repolens/internal/ingest"
	"github.com/huandert/repolens/internal/parser"
	"github.com/huandert/repolens/internal/parser/golang"
	"github.com/huandert/repolens/internal/parser/typescript"
)

func newAnalyzeCmd() *cobra.Command {
	var branch, token string
	cmd := &cobra.Command{
		Use:   "analyze <url|path>",
		Short: "Clone (or open) a repository, parse it, and build the local code graph",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			if token == "" {
				token = os.Getenv("GITHUB_TOKEN")
			}
			return runAnalyze(target, branch, token)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "branch to check out (default: remote's default branch)")
	cmd.Flags().StringVar(&token, "token", "", "auth token for HTTPS clone (default: $GITHUB_TOKEN)")
	return cmd
}

func runAnalyze(target, branch, token string) error {
	cloneDir, dbPath, err := repoWorkspace(target)
	if err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}

	fmt.Printf("→ Fetching %s...\n", target)
	result, err := ingest.Clone(ingest.CloneOptions{URL: target, Branch: branch, Token: token, Dest: cloneDir})
	if err != nil {
		return err
	}
	fmt.Printf("  ready at %s (commit %s)\n", result.Path, shortHash(result.Commit))

	registry := parser.NewRegistry(golang.New(), typescript.New())
	extSet := map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true}

	files, err := ingest.DiscoverFiles(result.Path, extSet)
	if err != nil {
		return fmt.Errorf("discover files: %w", err)
	}
	fmt.Printf("→ Found %d source files\n", len(files))

	var analyses []*parser.FileAnalysis
	langCounts := map[string]int{}
	for _, f := range files {
		analyzer, ok := registry.For(f.Ext)
		if !ok {
			continue
		}
		content, err := os.ReadFile(f.AbsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", f.RelPath, err)
			continue
		}
		fa, err := analyzer.Analyze(f.RelPath, content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", f.RelPath, err)
			continue
		}
		analyses = append(analyses, fa)
		langCounts[analyzer.Language()]++
	}
	for lang, n := range langCounts {
		fmt.Printf("  %s: %d files\n", lang, n)
	}

	fmt.Println("→ Building code graph...")
	g := graph.NewBuilder().Build(analyses)
	fmt.Printf("  %d nodes, %d edges\n", len(g.Nodes), len(g.Edges))

	store, err := graph.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open graph store: %w", err)
	}
	defer store.Close()
	if err := store.Save(g); err != nil {
		return fmt.Errorf("save graph: %w", err)
	}
	_ = store.SetMeta("target", target)
	_ = store.SetMeta("commit", result.Commit)

	if err := setLastTarget(target); err != nil {
		return err
	}

	fmt.Printf("✓ Analysis complete. Try:\n  repolens coupling\n  repolens impact <symbol>\n  repolens flow <endpoint|function>\n")
	return nil
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
