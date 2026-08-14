# RepoLens

[![CI](https://github.com/cloud-ct/repolens/actions/workflows/ci.yml/badge.svg)](https://github.com/cloud-ct/repolens/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Reference](https://img.shields.io/badge/go-1.25%2B-00ADD8)](https://go.dev)

**RepoLens is a VS Code extension that turns the workspace you already have
open into an explorable code graph, then answers architecture questions
about it — in plain English, with diagrams.**

No cloning, no pointing it at a URL: it reads the folder that's already
open in your editor. Everything runs locally — parsing, the graph, and the
deterministic queries never leave your machine, and even `ask` (BYOK LLM
Q&A) only ever sends the small subgraph relevant to your question.

*(demo GIF — coming soon)*

## Why RepoLens exists

Understanding an unfamiliar codebase — especially a large, multi-language
one you just inherited — usually means grepping around and building a
mental model by hand. RepoLens builds that model for you: it parses your
workspace into a graph of files, symbols, and their relationships (calls,
imports, implements), then lets you explore it two ways, right inside the
editor:

- **Deterministically**, with zero LLM involved: "what breaks if I change
  this?", "what are the most coupled modules?", "show me the call flow for
  this endpoint." These are graph traversals, not guesses — same input,
  same answer, every time. CodeLens annotations surface impact and coupling
  inline, above every function.
- **In natural language**, via the built-in Ask panel, when you want a
  narrated explanation and a diagram. RepoLens only sends the LLM the small
  subgraph relevant to your question — never your whole repository.

## Install the VS Code extension

Search for **RepoLens** in the VS Code Marketplace, or install directly:

```
ext install cloudct.repolens
```

Open a project, wait for the sidebar's "Analyze workspace" step to finish,
and start exploring — click any function to see its coupling and impact,
Ctrl/Cmd-click a call in the flow diagram to jump to its definition, and use
the Ask panel for natural-language questions.

## Supported languages

| Language | Symbols | Endpoints |
|---|---|---|
| Go | functions, methods, types, interfaces | — |
| TypeScript / JavaScript | functions, methods, classes, interfaces | Express routes |
| C# | methods, classes, interfaces | ASP.NET controller actions |
| Python | functions, methods, classes | Flask/FastAPI-style routes |

More languages are one `parser.LanguageAnalyzer` implementation away — see
[Adding support for a new language](#adding-support-for-a-new-language)
below.

## Configuring an LLM (BYOK — Bring Your Own Key)

Only the Ask panel needs a key; every deterministic feature (impact,
coupling, flow diagrams, CodeLens) works with zero configuration. Set your
provider and key in the extension's settings (`RepoLens: Provider` /
`RepoLens: API Key`), or via environment variables if you're using the CLI
directly:

- **Anthropic** — `ANTHROPIC_API_KEY`
- **OpenAI** — `OPENAI_API_KEY`
- **Ollama** (100% local, no code ever leaves your machine) — point
  `OLLAMA_HOST` at a running `ollama serve` instance

If no provider is configured, the Ask panel tells you what to set instead
of failing with a cryptic error — every deterministic feature is
unaffected either way.

## Privacy

RepoLens is **local-first**: it operates on the workspace already open on
your machine and never uploads your source code anywhere by itself.

- Graph building, impact, coupling, and flow diagrams never make a network
  call to any AI provider — they parse your local files and query a local
  SQLite graph.
- The Ask panel is the only feature that talks to an LLM, and only sends
  the **bounded subgraph** relevant to your question — node names, file
  paths, signatures, and a couple of relevant code excerpts — never the
  full repository.
- API keys live in your VS Code settings (or local environment); they are
  never logged and never sent anywhere except the provider you chose.
- Choosing the Ollama provider keeps everything, including the Ask panel,
  entirely on your machine.

## RepoLens as a standalone CLI

The extension is powered by a Go CLI (`repolens`) that you can also use
directly — handy for CI, scripting, or exploring a repo from a terminal.

```bash
go install github.com/cloud-ct/repolens/cmd/repolens@latest
```

Or build from source:

```bash
git clone https://github.com/cloud-ct/repolens.git
cd repolens
go build -o repolens ./cmd/repolens
```

Requires Go 1.25+ (a transitive dependency of the pure-Go SQLite driver
pins this; `go build` auto-fetches the right toolchain if yours is older,
as long as `GOTOOLCHAIN` is not set to `local`). No Docker, no external
database — RepoLens stores its graph in a local SQLite file under
`~/.repolens/`.

```bash
repolens analyze ./path/to/your/repo    # parse it and build the graph
repolens coupling --top 10               # deterministic, no LLM needed
repolens impact UserService               # what breaks if I change this?
repolens ask "How does user creation work?" # needs an LLM key — see above
```

| Command | Needs an LLM? | What it does |
|---|---|---|
| `repolens analyze <path>` | no | Parses a local folder and builds its code graph |
| `repolens impact <symbol>` | no | Everything that transitively depends on a symbol |
| `repolens coupling [--top N]` | no | Most coupled symbols by fan-in + fan-out |
| `repolens flow <endpoint\|function>` | no | ASCII (and optional Mermaid) call-flow diagram |
| `repolens ask "<question>"` | **yes** | Natural-language Q&A with narration + diagrams |

Every command supports `--repo <path>` to target a specific analyzed
folder; if omitted, it defaults to whatever you last ran `analyze` on.
RepoLens only ever reads from a path already on disk — it doesn't clone or
fetch anything itself, so a repo needs to already be checked out (exactly
what the VS Code extension does for you automatically with the open
workspace).

## Adding support for a new language

Language support is a single interface implementation —
`parser.LanguageAnalyzer` — and doesn't require touching the graph, CLI,
or extension. See [CONTRIBUTING.md](CONTRIBUTING.md#adding-a-new-language)
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
├── cmd/repolens/            # CLI entry point + cobra commands
├── internal/
│   ├── ingest/               # local folder resolution, file discovery by language
│   ├── parser/                # LanguageAnalyzer interface + tree-sitter analyzers
│   │   ├── golang/            # Go
│   │   ├── typescript/        # TypeScript / JavaScript
│   │   ├── csharp/            # C#
│   │   └── python/            # Python
│   ├── graph/                  # node/edge model, SQLite store, deterministic queries
│   ├── diagram/                # ASCII + Mermaid rendering from subgraphs
│   └── ai/                      # Provider interface + anthropic/openai/ollama
│       └── prompts/             # ask prompt templates
├── editors/vscode/           # the VS Code extension (primary product)
└── examples/sample-app/      # tiny Go + TS app to try RepoLens without a real repo
```

The graph answers structural questions first, deterministically; the LLM is
only ever handed the small piece of it needed to narrate an answer.

## Try it without a real repo

```bash
repolens analyze ./examples/sample-app
repolens flow CreateUser --mermaid
repolens coupling
```

## License

MIT — see [LICENSE](LICENSE).
