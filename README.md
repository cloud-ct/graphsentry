# RepoLens

[![CI](https://github.com/YOUR_ORG/repolens/actions/workflows/ci.yml/badge.svg)](https://github.com/YOUR_ORG/repolens/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Reference](https://img.shields.io/badge/go-1.25%2B-00ADD8)](https://go.dev)

**RepoLens is a local-first tool that turns any Git repository into an
explorable code graph, then answers architecture questions about it — in
plain English, with diagrams.**

```
$ repolens ask "How does user creation work?"

EXPLANATION
POST /users is handled by UsersController, which delegates to
UserService.CreateUser. CreateUser validates the input, persists the user
via UserRepository, and issues an auth token through IdentityService.

POST /users → UsersController → UserService.CreateUser()
                                      ├─► UserRepository → PostgreSQL
                                      ├─► IdentityService
                                      └─► RabbitMQ → UserCreatedEvent
```

*(asciinema demo — coming soon)*

## Why RepoLens exists

Understanding an unfamiliar codebase — especially a large, multi-language
one you just inherited — usually means grepping around and building a
mental model by hand. RepoLens builds that model for you: it parses your
repo into a graph of files, symbols, and their relationships (calls,
imports, implements), then lets you query it two ways:

- **Deterministically**, with zero LLM involved: "what breaks if I change
  this?", "what are the most coupled modules?", "show me the call flow for
  this endpoint." These are graph traversals, not guesses — same input,
  same answer, every time.
- **In natural language**, via `ask`, when you want a narrated explanation
  and a diagram. RepoLens only sends the LLM the small subgraph relevant to
  your question — never your whole repository.

## Installation

```bash
go install github.com/YOUR_ORG/repolens/cmd/repolens@latest
```

Or build from source:

```bash
git clone https://github.com/YOUR_ORG/repolens.git
cd repolens
go build -o repolens ./cmd/repolens
```

Requires Go 1.25+ (a transitive dependency of the pure-Go SQLite driver pins this; `go build` auto-fetches the right toolchain if yours is older, as long as GOTOOLCHAIN is not set to "local"). No Docker, no external database — RepoLens stores its
graph in a local SQLite file under `~/.repolens/`.

## Quickstart

```bash
repolens analyze https://github.com/you/your-repo    # clone + parse + build the graph
repolens coupling --top 10                            # deterministic, no LLM needed
repolens impact UserService                            # what breaks if I change this?
repolens ask "How does user creation work?"             # needs an LLM key — see below
```

## Commands

| Command | Needs an LLM? | What it does |
|---|---|---|
| `repolens analyze <url\|path>` | no | Clones (or opens) a repo, parses it, builds the graph |
| `repolens impact <symbol>` | no | Everything that transitively depends on a symbol |
| `repolens coupling [--top N]` | no | Most coupled symbols by fan-in + fan-out |
| `repolens flow <endpoint\|function>` | no | ASCII (and optional Mermaid) call-flow diagram |
| `repolens risk [--top N]` | no | *(Phase 3)* churn × coupling risk ranking |
| `repolens ask "<question>"` | **yes** | Natural-language Q&A with narration + diagrams |
| `repolens serve [--port]` | — | *(future)* HTTP API + web UI |

Every command supports `--repo <url|path>` to target a specific analyzed
repository; if omitted, it defaults to whatever you last ran `analyze` on.

## Configuring an LLM (BYOK — Bring Your Own Key)

Only `ask` needs a key. Every deterministic command above works with zero
configuration. Set one provider via environment variables (or
`~/.repolens/config.yaml`):

**Anthropic**
```bash
export REPOLENS_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-...   # https://console.anthropic.com/settings/keys
```

**OpenAI**
```bash
export REPOLENS_PROVIDER=openai
export OPENAI_API_KEY=sk-...          # https://platform.openai.com/api-keys
```

**Ollama (100% local, no code ever leaves your machine)**
```bash
ollama serve
ollama pull llama3.1
export REPOLENS_PROVIDER=ollama
export OLLAMA_HOST=http://localhost:11434
```

If no provider is configured, `ask` fails with instructions instead of a
cryptic error — deterministic commands are unaffected.

## Authenticating to private repositories

**HTTPS token:**
```bash
export GITHUB_TOKEN=ghp_...           # needs `repo` scope
repolens analyze https://github.com/you/private-repo
```
Or pass it inline: `repolens analyze <url> --token ghp_...`

**SSH:** if your repo URL uses the `git@github.com:...` or `ssh://...`
form, RepoLens uses your local SSH agent / default keys automatically — no
extra configuration needed, as long as `ssh -T git@github.com` already
works for you.

Auth failures return an actionable message (e.g. pointing you to
`github.com/settings/tokens` with the right scope) instead of a raw git
error.

## Privacy

RepoLens is **local-first**: your source code is cloned to
`~/.repolens/<repo>/` and never uploaded anywhere by RepoLens itself.

- `analyze`, `impact`, `coupling`, `flow`, and `risk` never make a network
  call to any AI provider — they only read the local git remote you point
  them at and operate on the local SQLite graph.
- `ask` is the only command that talks to an LLM, and only sends the
  **bounded subgraph** relevant to your question — node names, file paths,
  signatures, and a couple of relevant code excerpts — never the full
  repository. You can inspect exactly what would be sent by reading
  `internal/ai.SerializeSubgraph`.
- API keys are read from environment variables or your local
  `~/.repolens/config.yaml`; they are never logged, and never written
  anywhere else.
- Choosing the Ollama provider keeps everything, including the `ask`
  pipeline, entirely on your machine.

## Adding support for a new language

Language support is a single interface implementation —
`parser.LanguageAnalyzer` — and doesn't require touching the graph, CLI, or
any other core package. See [CONTRIBUTING.md](CONTRIBUTING.md#adding-a-new-language)
for the full guide; the short version:

```go
type LanguageAnalyzer interface {
    Language() string
    Extensions() []string
    Analyze(path string, content []byte) (*FileAnalysis, error)
}
```

Implement it under `internal/parser/<language>/`, using
`github.com/smacker/go-tree-sitter` for parsing, and register it in
`cmd/repolens/analyze.go`.

## Architecture

```
repolens/
├── cmd/repolens/          # CLI entry point + cobra commands
├── internal/
│   ├── ingest/             # clone (HTTPS token / SSH), file discovery by language
│   ├── parser/             # LanguageAnalyzer interface + tree-sitter analyzers
│   │   ├── golang/         # Go
│   │   └── typescript/     # TypeScript / JavaScript
│   ├── graph/               # node/edge model, SQLite store, deterministic queries
│   ├── diagram/             # ASCII + Mermaid rendering from subgraphs
│   ├── ai/                  # Provider interface + anthropic/openai/ollama
│   │   └── prompts/         # ask prompt templates
│   └── metrics/             # (Phase 3) churn × coupling risk scoring
└── examples/sample-app/     # tiny Go + TS app to try RepoLens without a real repo
```

The graph answers structural questions first, deterministically; the LLM is
only ever handed the small piece of it needed to narrate an answer.

## Try it without a real repo

```bash
repolens analyze ./examples/sample-app
repolens flow CreateUser --mermaid
repolens coupling
```

## Roadmap

- [x] Phase 1 — deterministic core: ingest, Go/TypeScript parsing, graph,
      `analyze`/`impact`/`coupling`/`flow`
- [x] Phase 2 — `ask` with BYOK providers (Anthropic/OpenAI/Ollama) and
      Mermaid generation
- [ ] Phase 3 — Python, Java, C# analyzers; `risk` command (git churn ×
      coupling); embedding-based semantic search for `ask`
- [ ] Phase 4 — `repolens serve`: HTTP API + interactive web UI

## License

MIT — see [LICENSE](LICENSE).
