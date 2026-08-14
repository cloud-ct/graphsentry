// Package ingest resolves the local folder to analyze and enumerates its
// source files by language. RepoLens operates entirely on code already on
// disk — most commonly the workspace already open in the VS Code
// extension — it never fetches anything from a remote git host itself.
package ingest

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OpenLocal validates that path is an existing local directory and
// returns its resolved HEAD commit hash, if it's a git repository (empty
// string, no error, if it isn't — plenty of valid analysis targets, like
// a bare folder of scripts, aren't). Shells out to the system `git`
// rather than linking a git-plumbing library, since this is the only
// thing RepoLens still needs one for.
func OpenLocal(path string) (commit string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", fmt.Errorf("%s: %w", path, statErr)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}

	out, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", nil // not a git repo, or git isn't installed — not fatal, just no commit metadata
	}
	return strings.TrimSpace(string(out)), nil
}
