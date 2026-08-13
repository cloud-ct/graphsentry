# RepoLens for VS Code

Local-first, multi-language code graph explorer, right in the editor. This
extension is a thin UI layer over the [RepoLens CLI](https://github.com/huandertironi/repolens)
— it never reimplements graph logic itself.

## What it does

- **RepoLens: Analyze Workspace** — clones/parses the current workspace and builds its code graph.
- **RepoLens: Show Most Coupled Symbols** — fan-in/fan-out ranking, jump straight to a symbol.
- **RepoLens: Impact Analysis for Symbol at Cursor** — what breaks if you change this?
- **RepoLens: Show Call Flow for Symbol at Cursor** — Mermaid + ASCII diagram in a side panel.
- **RepoLens: Ask a Question About This Codebase** — natural-language Q&A (requires a BYOK LLM key — see the main repo's README).

All deterministic commands work with **zero LLM key configured**. Only "Ask" needs one.

## How the binary is managed

On first use, the extension downloads the matching `repolens` CLI binary
for your OS/architecture from the [RepoLens GitHub Releases](https://github.com/huandertironi/repolens/releases)
and caches it in the extension's storage — no separate install step. If you'd
rather manage your own build (e.g. via `go install`), set the
`repolens.binaryPath` setting to point at it.

## Privacy

Your source code never leaves your machine except for the small subgraph
sent to the LLM you configure yourself when using "Ask" — see the
[main repo's privacy section](https://github.com/huandertironi/repolens#privacy)
for exactly what that includes.

## License

MIT
