package graph

import (
	"path"
	"strings"

	"github.com/huandert/repolens/internal/parser"
)

// Builder constructs a Graph from the per-file analyses produced by
// LanguageAnalyzers. It runs in two passes: first it registers every
// file/symbol node, then it resolves imports and call names against the
// symbols it knows about (best-effort — unresolved calls are dropped
// rather than creating dangling nodes, since a heuristic tree-sitter
// extraction over-reports call names that aren't real repo symbols).
type Builder struct {
	g *Graph

	// nameIndex maps a simple or qualified symbol name to the node IDs
	// that could match it, for resolving call edges.
	nameIndex map[string][]string
	// fileIndex maps a repo-relative file path (without extension, and
	// with common suffixes stripped) to its file node ID, for resolving
	// import edges.
	fileIndex map[string]string
}

// NewBuilder creates an empty Builder.
func NewBuilder() *Builder {
	return &Builder{
		g:         New(),
		nameIndex: make(map[string][]string),
		fileIndex: make(map[string]string),
	}
}

// AddFile registers a file's analysis: the file node itself, one node per
// extracted symbol, and "defines" edges from the file to each symbol.
func (b *Builder) AddFile(fa *parser.FileAnalysis) {
	fileID := "file::" + fa.Path
	b.g.AddNode(&Node{ID: fileID, Kind: NodeFile, Name: path.Base(fa.Path), File: fa.Path, Language: fa.Language})
	b.fileIndex[normalizeFileKey(fa.Path)] = fileID

	for _, sym := range fa.Symbols {
		id := symbolID(fa.Path, sym.Qualified)
		b.g.AddNode(&Node{
			ID: id, Kind: nodeKindOf(sym.Kind), Name: sym.Name, File: fa.Path, Language: fa.Language,
			StartLine: sym.StartLine, EndLine: sym.EndLine,
			Signature: sym.Signature, DocComment: sym.DocComment,
		})
		b.g.AddEdge(fileID, id, EdgeDefines)
		b.nameIndex[sym.Name] = append(b.nameIndex[sym.Name], id)
		if sym.Qualified != sym.Name {
			b.nameIndex[sym.Qualified] = append(b.nameIndex[sym.Qualified], id)
		}
	}
}

// pending holds deferred edges (calls, implements, imports) collected
// during AddFile passes, resolved once all files are registered.
type pending struct {
	fromID   string
	fromPath string
	callName string
	implName string
	imports  []parser.Import
}

// analyses accumulates raw FileAnalysis + symbol->id mapping needed for a
// second resolution pass. Build() should be called with the same list of
// analyses passed to AddFile.
func (b *Builder) Build(analyses []*parser.FileAnalysis) *Graph {
	for _, fa := range analyses {
		b.AddFile(fa)
	}
	for _, fa := range analyses {
		fileID := "file::" + fa.Path

		for _, imp := range fa.Imports {
			if targetFile, ok := b.resolveImport(fa.Path, imp.Path); ok {
				b.g.AddEdge(fileID, targetFile, EdgeImports)
			}
		}

		for _, sym := range fa.Symbols {
			fromID := symbolID(fa.Path, sym.Qualified)
			for _, call := range sym.Calls {
				if toID, ok := b.resolveName(call, fromID); ok {
					b.g.AddEdge(fromID, toID, EdgeCalls)
				}
			}
			for _, impl := range sym.Implements {
				if toID, ok := b.resolveName(impl, fromID); ok {
					b.g.AddEdge(fromID, toID, EdgeImplements)
				}
			}
		}
	}
	return b.g
}

// resolveName finds the best-matching node ID for a call/implements target
// name, preferring a match within the same file, then the sole match
// repo-wide, and giving up (no edge) if the name is ambiguous or unknown.
func (b *Builder) resolveName(name string, fromID string) (string, bool) {
	candidates := b.nameIndex[name]
	if len(candidates) == 0 {
		return "", false
	}
	if len(candidates) == 1 {
		if candidates[0] == fromID {
			return "", false
		}
		return candidates[0], true
	}
	fromFile := ""
	if idx := strings.Index(fromID, "::"); idx >= 0 {
		fromFile = fromID[len("symbol::"):]
	}
	for _, c := range candidates {
		if c != fromID && strings.HasPrefix(c, "symbol::"+fromFile) {
			return c, true
		}
	}
	return "", false // ambiguous across files; skip rather than guess wrong
}

// resolveImport maps an import path written in source to a known file
// node, handling relative paths (./foo, ../bar) by resolving them against
// the importing file's directory. External/module imports (no leading dot)
// are not resolved to a file node since they live outside the repo.
func (b *Builder) resolveImport(fromPath, importPath string) (string, bool) {
	if !strings.HasPrefix(importPath, ".") {
		return "", false
	}
	dir := path.Dir(fromPath)
	joined := path.Join(dir, importPath)
	key := normalizeFileKey(joined)
	if id, ok := b.fileIndex[key]; ok {
		return id, true
	}
	// Try common extensions/index files for TS/JS-style extension-less imports.
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", "/index.ts", "/index.js"} {
		if id, ok := b.fileIndex[normalizeFileKey(joined+ext)]; ok {
			return id, true
		}
	}
	return "", false
}

// nodeKindOf maps a parser.SymbolKind onto the graph's NodeKind. Kept as an
// explicit switch (rather than a shared type) so parser stays free of a
// dependency on graph, avoiding an import cycle.
func nodeKindOf(k parser.SymbolKind) NodeKind {
	switch k {
	case parser.KindFunction:
		return NodeFunction
	case parser.KindMethod:
		return NodeMethod
	case parser.KindType:
		return NodeType
	case parser.KindClass:
		return NodeClass
	case parser.KindInterface:
		return NodeInterface
	case parser.KindEndpoint:
		return NodeEndpoint
	default:
		return NodeType
	}
}

func symbolID(filePath, qualified string) string {
	return "symbol::" + filePath + "::" + qualified
}

func normalizeFileKey(p string) string {
	p = strings.TrimSuffix(p, path.Ext(p))
	return p
}
