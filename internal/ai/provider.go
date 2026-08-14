// Package ai defines the Provider interface used by `graphsentry ask` and its
// BYOK implementations (Anthropic, OpenAI, Ollama). No API key is ever
// hardcoded, logged, or persisted outside the user's local config — see
// Config in this package for how keys are resolved.
package ai

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloud-ct/graphsentry/internal/graph"
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
// NewProviderFromEnv/NewProvider — the rest of GraphSentry is unaffected.
type Provider interface {
	// Name returns a short identifier, e.g. "anthropic".
	Name() string
	// Ask sends the bounded question+context to the model and returns a
	// structured explanation + diagrams.
	Ask(ctx context.Context, req AskRequest) (*AskResponse, error)
	// AskStream behaves like Ask, but invokes onDelta with each chunk of
	// raw text as the model generates it (so a caller — `graphsentry ask
	// --stream` — can forward it live instead of waiting for the full
	// response), and still returns the same final parsed AskResponse once
	// generation completes.
	AskStream(ctx context.Context, req AskRequest, onDelta func(string)) (*AskResponse, error)
}

// Config holds BYOK settings resolved from environment variables or
// ~/.graphsentry/config.yaml. Fields here are never logged.
type Config struct {
	Provider string // "anthropic" | "openai" | "ollama"
	APIKey   string
	Model    string
	Host     string // ollama only
}

// NewProviderFromEnv resolves configuration from environment variables
// (GRAPHSENTRY_PROVIDER, ANTHROPIC_API_KEY, OPENAI_API_KEY, OLLAMA_HOST) and
// returns the matching Provider. If no provider is configured, it returns
// an error that teaches the user how to set one up — `ask` is the only
// command that requires this; impact/coupling/flow work without any key.
func NewProviderFromEnv() (Provider, error) {
	cfg := Config{
		Provider: strings.ToLower(strings.TrimSpace(os.Getenv("GRAPHSENTRY_PROVIDER"))),
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
		return newAnthropicProvider(key, os.Getenv("GRAPHSENTRY_MODEL")), nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, noKeyErr("openai", "OPENAI_API_KEY", "https://platform.openai.com/api-keys")
		}
		return newOpenAIProvider(key, os.Getenv("GRAPHSENTRY_MODEL")), nil
	case "ollama":
		host := os.Getenv("OLLAMA_HOST")
		if host == "" {
			host = "http://localhost:11434"
		}
		return newOllamaProvider(host, os.Getenv("GRAPHSENTRY_MODEL")), nil
	default:
		return nil, fmt.Errorf(`no LLM provider configured for "ask".

graphsentry is local-first: deterministic commands (impact, coupling, flow) work
without any key. "ask" needs an LLM you bring yourself (BYOK). Configure one:

  export GRAPHSENTRY_PROVIDER=anthropic
  export ANTHROPIC_API_KEY=sk-...        # https://console.anthropic.com/settings/keys

  # or
  export GRAPHSENTRY_PROVIDER=openai
  export OPENAI_API_KEY=sk-...           # https://platform.openai.com/api-keys

  # or, 100%% local:
  export GRAPHSENTRY_PROVIDER=ollama
  export OLLAMA_HOST=http://localhost:11434

You can also set these in ~/.graphsentry/config.yaml. See the README's
"Configuration (BYOK)" section for details`) //nolint:staticcheck // this is long-form help text, not a typical wrapped error
	}
}

func noKeyErr(provider, envVar, whereToGet string) error {
	return fmt.Errorf("GRAPHSENTRY_PROVIDER=%s but %s is not set\nGet a key at %s and export it as %s", provider, envVar, whereToGet, envVar)
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
