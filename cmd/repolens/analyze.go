package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cloud-ct/repolens/internal/graph"
	"github.com/cloud-ct/repolens/internal/ingest"
	"github.com/cloud-ct/repolens/internal/parser"
	"github.com/cloud-ct/repolens/internal/parser/csharp"
	"github.com/cloud-ct/repolens/internal/parser/golang"
	"github.com/cloud-ct/repolens/internal/parser/python"
	"github.com/cloud-ct/repolens/internal/parser/typescript"
)

func newAnalyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze <path>",
		Short: "Parse a local folder and build its code graph",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnalyze(args[0])
		},
	}
	return cmd
}

func runAnalyze(target string) error {
	dbPath, err := repoDBPath(target)
	if err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}

	commit, err := ingest.OpenLocal(target)
	if err != nil {
		return err
	}
	fmt.Printf("→ Analyzing %s%s\n", target, commitSuffix(commit))

	registry := parser.NewRegistry(golang.New(), typescript.New(), csharp.New(), python.New())
	extSet := map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".cs": true, ".py": true}

	files, err := ingest.DiscoverFiles(target, extSet)
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
	defer func() { _ = store.Close() }()
	if err := store.Save(g); err != nil {
		return fmt.Errorf("save graph: %w", err)
	}
	_ = store.SetMeta("target", target)
	_ = store.SetMeta("commit", commit)

	if err := setLastTarget(target); err != nil {
		return err
	}

	fmt.Printf("✓ Analysis complete. Try:\n  repolens coupling\n  repolens impact <symbol>\n  repolens flow <endpoint|function>\n")
	return nil
}

func commitSuffix(commit string) string {
	if commit == "" {
		return ""
	}
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return fmt.Sprintf(" (commit %s)", commit)
}
