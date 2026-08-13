package graph

import (
	"path"
	"strings"

	"github.com/huandert/repolens/internal/parser"
)

// Builder constructs a Graph from the per-file analyses produced by
// LanguageAnalyzers. It resolves edges in three passes over the fully
// registered symbol index: imports, then implements (classes -> the
// interfaces they implement), then calls/creates — implements has to run
// before calls because interface-typed call resolution routes through the
// implements edges just built. Best-effort throughout: unresolved
// references are dropped rather than creating dangling/wrong edges, since
// this is heuristic tree-sitter extraction, not a real type checker.
type Builder struct {
	g *Graph

	// nameIndex maps a simple or qualified symbol name to the node IDs
	// that could match it, for resolving call/implements edges.
	nameIndex map[string][]string
	// typeIndex maps a class/interface/type's simple name to its node
	// IDs, for resolving `new Foo()` and interface-typed call receivers.
	typeIndex map[string][]string
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
		typeIndex: make(map[string][]string),
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
		kind := nodeKindOf(sym.Kind)
		b.g.AddNode(&Node{
			ID: id, Kind: kind, Name: sym.Name, File: fa.Path, Language: fa.Language,
			StartLine: sym.StartLine, EndLine: sym.EndLine,
			Signature: sym.Signature, DocComment: sym.DocComment,
		})
		b.g.AddEdge(fileID, id, EdgeDefines)
		b.nameIndex[sym.Name] = append(b.nameIndex[sym.Name], id)
		if sym.Qualified != sym.Name {
			b.nameIndex[sym.Qualified] = append(b.nameIndex[sym.Qualified], id)
		}
		if kind == NodeClass || kind == NodeInterface || kind == NodeType {
			b.typeIndex[sym.Name] = append(b.typeIndex[sym.Name], id)
		}
	}
}

// Build registers every file's symbols (AddFile) and then resolves
// imports/implements/calls/creates against the now-complete symbol index.
// It must be called with the full list of analyses for a repo in one pass.
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
	}

	// Implements must be resolved before calls/creates: interface-typed
	// call resolution below looks up "who implements this interface" via
	// these edges.
	for _, fa := range analyses {
		for _, sym := range fa.Symbols {
			fromID := symbolID(fa.Path, sym.Qualified)
			for _, impl := range sym.Implements {
				if toID, ok := b.resolveName(impl, fromID); ok {
					b.g.AddEdge(fromID, toID, EdgeImplements)
				}
			}
		}
	}

	for _, fa := range analyses {
		for _, sym := range fa.Symbols {
			fromID := symbolID(fa.Path, sym.Qualified)
			for _, call := range sym.Calls {
				if toID, ok := b.resolveCall(call, fromID); ok {
					b.g.AddEdge(fromID, toID, EdgeCalls)
				}
			}
			for _, typeName := range sym.Creates {
				if toID, ok := b.resolveSingle(b.typeIndex[typeName], fromID); ok {
					b.g.AddEdge(fromID, toID, EdgeInstantiates)
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
	// symbolID format is "symbol::<filePath>::<qualified>" — split on "::"
	// with a limit so a filePath containing "::" (it won't, but qualified
	// names or route strings like "POST /path" safely might) doesn't throw
	// off the file portion we compare candidates against.
	parts := strings.SplitN(fromID, "::", 3)
	fromFile := ""
	if len(parts) >= 2 {
		fromFile = parts[1]
	}
	for _, c := range candidates {
		if c != fromID && strings.HasPrefix(c, "symbol::"+fromFile+"::") {
			return c, true
		}
	}
	return "", false // ambiguous across files; skip rather than guess wrong
}

// resolveSingle accepts a pre-filtered candidate list (e.g. from
// typeIndex) and returns it only if there's exactly one usable candidate —
// used where "prefer same file" doesn't apply (type names are expected to
// be unique repo-wide) and guessing among several would likely be wrong.
func (b *Builder) resolveSingle(candidates []string, fromID string) (string, bool) {
	var usable []string
	for _, c := range candidates {
		if c != fromID {
			usable = append(usable, c)
		}
	}
	if len(usable) == 1 {
		return usable[0], true
	}
	return "", false
}

// resolveCall resolves one call target. When the analyzer supplied a
// receiver type (parser.CallRef.ReceiverType), resolution is type-scoped:
//   - a direct "<ReceiverType>.<Name>" match (the receiver's declared type
//     itself defines the method), or
//   - when ReceiverType names a known interface, the single implementing
//     class that defines "<ImplementingClass>.<Name>" (via the Implements
//     edges built in the pass before this one).
//
// If ReceiverType is a symbol in the repo but neither path finds a unique
// match, resolution stops there — it does NOT fall back to the bare-name
// heuristic, because a known, non-matching receiver type is stronger
// evidence than an unscoped name collision. If ReceiverType is set but
// isn't a symbol in the repo at all (a BCL/vendor type, e.g. a local `var
// ws = new ClientWebSocket()`), resolution refuses outright rather than
// risk matching an unrelated same-named method elsewhere in the repo. Only
// when the analyzer supplied no ReceiverType (couldn't determine one) does
// this fall back to the old bare-name heuristic.
func (b *Builder) resolveCall(call parser.CallRef, fromID string) (string, bool) {
	if call.ReceiverType == "" {
		return b.resolveName(call.Name, fromID)
	}

	ifaceIDs := b.typeIndex[call.ReceiverType]
	if len(ifaceIDs) != 1 {
		return "", false // receiver type unknown to the repo, or itself ambiguous — refuse to guess
	}
	ifaceNode, ok := b.g.Nodes[ifaceIDs[0]]
	if !ok {
		return "", false
	}

	// A concrete class field: the type itself defines the method, no
	// interface indirection to route through.
	if ifaceNode.Kind != NodeInterface {
		return b.resolveSingle(b.nameIndex[call.ReceiverType+"."+call.Name], fromID)
	}

	// An interface field: the interface's own node may itself have a
	// "<Interface>.<Method>" entry (its abstract method declaration), but
	// that's not a real call target — route to whichever concrete class
	// implements it instead.
	var match string
	found := 0
	for _, e := range b.g.In(ifaceIDs[0], EdgeImplements) {
		implementer, ok := b.g.Nodes[e.From]
		if !ok {
			continue
		}
		if toID, ok := b.resolveSingle(b.nameIndex[implementer.Name+"."+call.Name], fromID); ok {
			match = toID
			found++
		}
	}
	if found == 1 {
		return match, true
	}
	return "", false // no implementer defines it, or more than one does (ambiguous)
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
