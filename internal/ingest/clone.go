// Package ingest handles fetching a repository onto the local disk
// (cloning public/private repos over HTTPS or SSH) and enumerating its
// source files by language. The user's code never leaves their machine
// during ingest — this package only talks to the git remote the user
// pointed it at.
package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// CloneOptions configures how a repository is fetched.
type CloneOptions struct {
	URL    string // remote URL (HTTPS or SSH) or local filesystem path
	Branch string // branch to check out; "" means the remote's default
	Token  string // GitHub/GitLab token for HTTPS auth, e.g. from $GITHUB_TOKEN
	Dest   string // local destination directory
}

// CloneResult describes the repository now available on disk.
type CloneResult struct {
	Path    string // local path (Dest, or the original path if already local)
	Commit  string // resolved HEAD commit hash
	IsLocal bool   // true if URL was already a local path (no clone performed)
}

// IsLocalPath reports whether target looks like a filesystem path rather
// than a remote git URL.
func IsLocalPath(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "git@") || strings.HasPrefix(target, "ssh://") {
		return false
	}
	info, err := os.Stat(target)
	return err == nil && info.IsDir()
}

// Clone fetches a repository to opts.Dest, or opens it in place if the
// target is already a local directory. It supports HTTPS token auth
// (GITHUB_TOKEN) and SSH key auth (via the local SSH agent / default keys).
func Clone(opts CloneOptions) (*CloneResult, error) {
	if IsLocalPath(opts.URL) {
		repo, err := git.PlainOpen(opts.URL)
		commit := ""
		if err == nil {
			if head, herr := repo.Head(); herr == nil {
				commit = head.Hash().String()
			}
		}
		return &CloneResult{Path: opts.URL, Commit: commit, IsLocal: true}, nil
	}

	if err := os.MkdirAll(filepath.Dir(opts.Dest), 0o755); err != nil {
		return nil, fmt.Errorf("prepare destination: %w", err)
	}

	cloneOpts := &git.CloneOptions{
		URL:      opts.URL,
		Progress: nil,
	}
	if opts.Branch != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(opts.Branch)
		cloneOpts.SingleBranch = true
	}

	auth, err := resolveAuth(opts.URL, opts.Token)
	if err != nil {
		return nil, err
	}
	cloneOpts.Auth = auth

	repo, err := git.PlainClone(opts.Dest, false, cloneOpts)
	if err != nil {
		return nil, translateCloneErr(err, opts.URL)
	}

	commit := ""
	if head, herr := repo.Head(); herr == nil {
		commit = head.Hash().String()
	}

	return &CloneResult{Path: opts.Dest, Commit: commit}, nil
}

// resolveAuth picks HTTPS token auth or SSH key auth based on the URL
// scheme. A missing token/key is not an error here — public repos and
// repos with keys already loaded in the SSH agent still work.
func resolveAuth(url, token string) (transport.AuthMethod, error) {
	if strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://") {
		auth, err := ssh.NewSSHAgentAuth("git")
		if err != nil {
			// Not fatal: go-git will still try default key files.
			return nil, nil
		}
		return auth, nil
	}
	if token != "" {
		return &http.BasicAuth{Username: "repolens", Password: token}, nil
	}
	return nil, nil
}

func translateCloneErr(err error, url string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "authentication required") || strings.Contains(msg, "401"):
		return fmt.Errorf("authentication failed cloning %s: invalid or missing token.\n"+
			"Generate one at https://github.com/settings/tokens with 'repo' scope and pass it via --token or export GITHUB_TOKEN", url)
	case strings.Contains(msg, "not found") || strings.Contains(msg, "404"):
		return fmt.Errorf("repository not found: %s (check the URL and, for private repos, your token permissions)", url)
	default:
		return fmt.Errorf("clone %s: %w", url, err)
	}
}
