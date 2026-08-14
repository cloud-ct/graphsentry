# Contributing to RepoLens

Thanks for considering a contribution! RepoLens is designed so most
contributions don't require touching the core — the most common one is
adding support for a new language.

## Development setup

```bash
git clone https://github.com/<org>/repolens.git
cd repolens
go build ./...
go test ./...
```

Go 1.25+ is required (pulled in transitively by the pure-Go SQLite driver). No CGO-free constraint applies to the parser package
(tree-sitter grammars use cgo), but `internal/graph`'s SQLite driver
(`modernc.org/sqlite`) is pure Go — don't introduce a CGO SQLite driver.

## Adding a new language

Language support is a `parser.LanguageAnalyzer` implementation — you do not
need to touch `internal/graph` or the CLI.

1. Create `internal/parser/<language>/analyzer.go`.
2. Implement the interface from `internal/parser/analyzer.go`:

   ```go
   type LanguageAnalyzer interface {
       Language() string
       Extensions() []string
       Analyze(path string, content []byte) (*FileAnalysis, error)
   }
   ```

3. Use a tree-sitter grammar via `github.com/smacker/go-tree-sitter` to
   parse `content` and walk the tree, extracting:
   - **Symbols**: functions, methods, types/classes/interfaces, and (if the
     language has an obvious web framework convention, like Express routes
     or ASP.NET controller actions) HTTP endpoints. See
     `internal/parser/golang/analyzer.go` and
     `internal/parser/typescript/analyzer.go` for reference implementations.
   - **Imports**: the file's own import/using/require statements.
   - **Calls**: best-effort call-target names found inside each symbol's
     body — the graph builder resolves these against known symbols, so
     over-reporting is fine (unresolved calls are silently dropped) but
     under-reporting loses real edges.

4. Register your analyzer in `cmd/repolens/analyze.go`'s
   `parser.NewRegistry(...)` call and add its extensions to the discovery
   `extSet`.
5. Add fixtures under `internal/parser/<language>/testdata/` (or inline
   test strings, see the existing analyzer tests) and a
   `TestAnalyze` covering at least: one function/method, one
   type/class/interface, one import, and one call edge.
6. Run `go build ./... && go test ./...` before opening a PR.

## Code style

- Keep comments explaining *why*, not restating the code.
- Errors that a user will see should be actionable (say what to do next),
  matching the style in `internal/ingest/clone.go` and `internal/ai/provider.go`.
- Deterministic commands (`impact`, `coupling`, `flow`, `risk`) must never
  require or call an LLM provider.

## Pull requests

CI runs `go build ./...`, `go test ./...`, and `golangci-lint` on every PR.
Please make sure all three pass locally first.

**Target `develop`, not `main`.** `develop` is the repo's default branch
and where all contributions land; `main` only advances via a
maintainer-initiated `develop` -> `main` PR. A PR opened against `main` is
closed automatically (see `.github/workflows/enforce-pr-target.yml`) —
this isn't personal, it's just so history stays predictable. If that
happens to you, retarget the PR to `develop` (or open a fresh one there)
and it'll be picked back up.

Only the maintainer merges PRs — both branches require a review before
merging. Feel free to open issues for bugs, ideas, or questions even if
you're not planning to submit code; issues are always welcome regardless
of the PR flow above.
