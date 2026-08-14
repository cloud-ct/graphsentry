package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "repolens",
		Short: "RepoLens — intelligent, local-first code graph exploration",
		Long: "RepoLens parses a local folder into a multi-language code graph and lets you\n" +
			"ask structural questions about its architecture — deterministically via the\n" +
			"graph, or in natural language via your own LLM key (BYOK). Built primarily to\n" +
			"power the RepoLens VS Code extension, but fully usable standalone.",
	}
	root.AddCommand(newAnalyzeCmd())
	root.AddCommand(newImpactCmd())
	root.AddCommand(newCouplingCmd())
	root.AddCommand(newFlowCmd())
	root.AddCommand(newAskCmd())
	return root
}

// workspaceRoot returns ~/.repolens, creating it if needed.
func workspaceRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".repolens")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// repoSlug derives a filesystem-safe, stable identifier for a local path
// so each analyzed folder gets its own graph.db under the workspace root.
func repoSlug(target string) string {
	sum := sha1.Sum([]byte(target))
	return hex.EncodeToString(sum[:])[:12]
}

// repoDBPath returns the graph.db path for a given analyzed folder.
func repoDBPath(target string) (string, error) {
	root, err := workspaceRoot()
	if err != nil {
		return "", err
	}
	slug := repoSlug(target)
	base := filepath.Join(root, slug)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(base, "graph.db"), nil
}

// lastTargetFile records the most recently analyzed repo target, so
// deterministic commands can default to "whatever I just analyzed" when
// --repo isn't passed.
func lastTargetFile() (string, error) {
	root, err := workspaceRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "last"), nil
}

func setLastTarget(target string) error {
	f, err := lastTargetFile()
	if err != nil {
		return err
	}
	return os.WriteFile(f, []byte(target), 0o644)
}

func getLastTarget() (string, error) {
	f, err := lastTargetFile()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return "", fmt.Errorf("no repository specified and no previous `repolens analyze` found — pass --repo <url|path> or run `repolens analyze <url|path>` first")
	}
	return string(b), nil
}

// resolveTarget returns the explicit --repo flag value if set, otherwise
// the last analyzed repo.
func resolveTarget(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return getLastTarget()
}

// requireDB resolves target's graph.db path and errors with actionable
// guidance if it hasn't been analyzed yet.
func requireDB(target string) (string, error) {
	dbPath, err := repoDBPath(target)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("no graph found for %q — run `repolens analyze %s` first", target, target)
	}
	return dbPath, nil
}
