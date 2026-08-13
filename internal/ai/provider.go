// Package ai defines the Provider interface used by `repolens ask` and its
// BYOK implementations (Anthropic, OpenAI, Ollama). No API key is ever
// hardcoded, logged, or persisted outside the user's local config — see
// Config in this package for how keys are resolved.
package ai

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/huandert/repolens/internal/graph"
)

// AskRequest is the bounded context sent to the LLM: the user's question
// plus a serialized subgraph (never the whole repository).
type AskRequest struct {
	Question     string
	GraphContext string
}

// AskResponse is the LLM's structured answer.
type AskResponse struct {
	Explanation string
	Mermaid     string
	ASCII       string
}

// Provider is the common interface every LLM backend implements. Adding a
// new provider means implementing this interface and registering it in
// NewProviderFromEnv/NewProvider — the rest of RepoLens is unaffected.
type Provider interface {
	// Name returns a short identifier, e.g. "anthropic".
	Name() string
	// Ask sends the bounded question+context to the model and returns a
	// structured explanation + diagrams.
	Ask(ctx context.Context, req AskRequest) (*AskResponse, error)
}

// Config holds BYOK settings resolved from environment variables or
// ~/.repolens/config.yaml. Fields here are never logged.
type Config struct {
	Provider string // "anthropic" | "openai" | "ollama"
	APIKey   string
	Model    string
	Host     string // ollama only
}

// NewProviderFromEnv resolves configuration from environment variables
// (REPOLENS_PROVIDER, ANTHROPIC_API_KEY, OPENAI_API_KEY, OLLAMA_HOST) and
// returns the matching Provider. If no provider is configured, it returns
// an error that teaches the user how to set one up — `ask` is the only
// command that requires this; impact/coupling/flow work without any key.
func NewProviderFromEnv() (Provider, error) {
	cfg := Config{
		Provider: strings.ToLower(strings.TrimSpace(os.Getenv("REPOLENS_PROVIDER"))),
	}

	if cfg.Provider == "" {
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") != "":
			cfg.Provider = "anthropic"
		case os.Getenv("OPENAI_API_KEY") != "":
			cfg.Provider = "openai"
		case os.Getenv("OLLAMA_HOST") != "":
			cfg.Provider = "ollama"
		}
	}

	switch cfg.Provider {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, noKeyErr("anthropic", "ANTHROPIC_API_KEY", "https://console.anthropic.com/settings/keys")
		}
		return newAnthropicProvider(key, os.Getenv("REPOLENS_MODEL")), nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, noKeyErr("openai", "OPENAI_API_KEY", "https://platform.openai.com/api-keys")
		}
		return newOpenAIProvider(key, os.Getenv("REPOLENS_MODEL")), nil
	case "ollama":
		host := os.Getenv("OLLAMA_HOST")
		if host == "" {
			host = "http://localhost:11434"
		}
		return newOllamaProvider(host, os.Getenv("REPOLENS_MODEL")), nil
	default:
		return nil, fmt.Errorf(`no LLM provider configured for "ask".

repolens is local-first: deterministic commands (impact, coupling, flow) work
without any key. "ask" needs an LLM you bring yourself (BYOK). Configure one:

  export REPOLENS_PROVIDER=anthropic
  export ANTHROPIC_API_KEY=sk-...        # https://console.anthropic.com/settings/keys

  # or
  export REPOLENS_PROVIDER=openai
  export OPENAI_API_KEY=sk-...           # https://platform.openai.com/api-keys

  # or, 100%% local:
  export REPOLENS_PROVIDER=ollama
  export OLLAMA_HOST=http://localhost:11434

You can also set these in ~/.repolens/config.yaml. See the README's
"Configuration (BYOK)" section for details.`)
	}
}

func noKeyErr(provider, envVar, whereToGet string) error {
	return fmt.Errorf("REPOLENS_PROVIDER=%s but %s is not set.\nGet a key at %s and export it as %s.", provider, envVar, whereToGet, envVar)
}

// SerializeSubgraph renders a subgraph as compact text for the LLM prompt:
// node signatures grouped by file, then the edges between them. This is
// the *only* code that leaves the machine when using ask, and only for the
// nodes in sub — never the full repository.
func SerializeSubgraph(sub *graph.Graph) string {
	var b strings.Builder
	b.WriteString("NODES:\n")
	for _, n := range sub.Nodes {
		sig := n.Signature
		if sig == "" {
			sig = n.Name
		}
		fmt.Fprintf(&b, "- [%s] %s (%s) :: %s\n", n.Kind, n.Name, n.File, sig)
	}
	b.WriteString("\nEDGES:\n")
	for _, e := range sub.Edges {
		fmt.Fprintf(&b, "- %s --%s--> %s\n", e.From, e.Kind, e.To)
	}
	return b.String()
}
