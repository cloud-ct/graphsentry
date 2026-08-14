# GraphSentry for VS Code

Local-first, multi-language code graph explorer, right in the editor. This
extension is a thin UI layer over the [GraphSentry CLI](https://github.com/cloud-ct/graphsentry)
— it never reimplements graph logic itself.

## What it does

- **GraphSentry: Analyze Workspace** — parses the current workspace and builds its code graph.
- **GraphSentry: Show Most Coupled Symbols** — fan-in/fan-out ranking, jump straight to a symbol.
- **GraphSentry: Impact Analysis for Symbol at Cursor** — what breaks if you change this?
- **GraphSentry: Show Call Flow for Symbol at Cursor** — Mermaid + ASCII diagram in a side panel.
- **GraphSentry: Ask a Question About This Codebase** — natural-language Q&A (requires a BYOK LLM key — see the main repo's README).

All deterministic commands work with **zero LLM key configured**. Only "Ask" needs one.

## How the binary is managed

On first use, the extension downloads the matching `graphsentry` CLI binary
for your OS/architecture from the [GraphSentry GitHub Releases](https://github.com/cloud-ct/graphsentry/releases)
and caches it in the extension's storage — no separate install step. If you'd
rather manage your own build (e.g. via `go install`), set the
`graphsentry.binaryPath` setting to point at it.

## Privacy

Your source code never leaves your machine except for the small subgraph
sent to the LLM you configure yourself when using "Ask" — see the
[main repo's privacy section](https://github.com/cloud-ct/graphsentry#privacy)
for exactly what that includes.

## License

MIT
