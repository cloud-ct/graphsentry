// Package python implements parser.LanguageAnalyzer for Python source files
// using tree-sitter. It recognizes Flask/FastAPI-style route decorators
// (@app.route(...), @app.get(...), @blueprint.post(...)) to extract HTTP
// endpoints.
package python

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"

	"github.com/huandert/repolens/internal/parser"
)

var routeMethodAttrs = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH",
}

// Analyzer extracts functions, classes, imports, calls, and web-framework
// endpoints from Python files.
type Analyzer struct{}

// New returns a Python LanguageAnalyzer.
func New() *Analyzer { return &Analyzer{} }

func (a *Analyzer) Language() string     { return "python" }
func (a *Analyzer) Extensions() []string { return []string{".py"} }

func (a *Analyzer) Analyze(path string, content []byte) (*parser.FileAnalysis, error) {
	p := sitter.NewParser()
	p.SetLanguage(python.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	defer tree.Close()

	fa := &parser.FileAnalysis{Path: path, Language: "python"}
	root := tree.RootNode()
	src := content

	line := func(n *sitter.Node) (int, int) {
		return int(n.StartPoint().Row) + 1, int(n.EndPoint().Row) + 1
	}
	text := func(n *sitter.Node) string { return n.Content(src) }

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case "import_statement":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				c := n.NamedChild(i)
				if c.Type() == "dotted_name" || c.Type() == "aliased_import" {
					startLine, _ := line(n)
					fa.Imports = append(fa.Imports, parser.Import{Path: importName(c, src), Line: startLine})
				}
			}
			return

		case "import_from_statement":
			if mod := n.ChildByFieldName("module_name"); mod != nil {
				startLine, _ := line(n)
				fa.Imports = append(fa.Imports, parser.Import{Path: text(mod), Line: startLine})
			}
			return

		case "class_definition":
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil {
				break
			}
			className := text(nameNode)
			start, end := line(n)
			var implements []string
			if bases := n.ChildByFieldName("superclasses"); bases != nil {
				implements = extractIdentifiers(bases, src)
			}
			fa.Symbols = append(fa.Symbols, parser.Symbol{
				Kind: parser.KindClass, Name: className, Qualified: className,
				StartLine: start, EndLine: end,
				Signature:  signatureLine(text(n)),
				Implements: implements,
			})
			if body := n.ChildByFieldName("body"); body != nil {
				for i := 0; i < int(body.NamedChildCount()); i++ {
					collectFunction(body.NamedChild(i), className, fa, src, line, text)
				}
			}
			return

		case "function_definition", "decorated_definition":
			// Only handle top-level (module-scope) functions here; class
			// methods are handled via collectFunction from class_definition
			// above, so we don't double-count them.
			if isInsideClass(n) {
				break
			}
			collectFunction(n, "", fa, src, line, text)
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	return fa, nil
}

// isInsideClass reports whether n is nested (directly or via a
// decorated_definition wrapper) inside a class_definition's body — used to
// avoid double-visiting methods once as "top-level" and once as a class
// member.
func isInsideClass(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type() == "class_definition" {
			return true
		}
		if p.Type() == "function_definition" {
			return false // nested function inside another function, not a class
		}
	}
	return false
}

// collectFunction handles both a bare function_definition and a
// decorated_definition wrapping one, registering it as a function/method
// symbol and, if it carries a recognizable route decorator, an additional
// endpoint symbol.
func collectFunction(n *sitter.Node, className string, fa *parser.FileAnalysis, src []byte,
	line func(*sitter.Node) (int, int), text func(*sitter.Node) string) {
	if n == nil {
		return
	}

	var decorators []*sitter.Node
	funcNode := n
	if n.Type() == "decorated_definition" {
		funcNode = n.ChildByFieldName("definition")
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() == "decorator" {
				decorators = append(decorators, c)
			}
		}
	}
	if funcNode == nil || funcNode.Type() != "function_definition" {
		return
	}

	nameNode := funcNode.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	fName := text(nameNode)
	qualified := fName
	kind := parser.KindFunction
	if className != "" {
		qualified = className + "." + fName
		kind = parser.KindMethod
	}
	start, end := line(n)
	fa.Symbols = append(fa.Symbols, parser.Symbol{
		Kind: kind, Name: fName, Qualified: qualified,
		StartLine: start, EndLine: end,
		Signature: signatureLine(text(funcNode)),
		Calls:     extractCalls(funcNode, src),
	})

	if verb, route := endpointFromDecorators(decorators, src); verb != "" {
		fa.Symbols = append(fa.Symbols, parser.Symbol{
			Kind: parser.KindEndpoint, Name: verb + " " + route, Qualified: verb + " " + route,
			StartLine: start, EndLine: end,
			Signature: signatureLine(text(funcNode)),
			Calls:     []parser.CallRef{{Name: qualified, Line: start}},
		})
	}
}

// endpointFromDecorators scans a function's decorators for a Flask/FastAPI
// style route call — @x.route("/path", methods=["POST"]) or
// @x.get("/path") — and returns the HTTP verb + path.
func endpointFromDecorators(decorators []*sitter.Node, src []byte) (verb, route string) {
	for _, d := range decorators {
		call := findChildOfType(d, "call")
		if call == nil {
			continue
		}
		fn := call.ChildByFieldName("function")
		if fn == nil || fn.Type() != "attribute" {
			continue
		}
		attrNode := fn.ChildByFieldName("attribute")
		if attrNode == nil {
			continue
		}
		attr := attrNode.Content(src)

		args := call.ChildByFieldName("arguments")
		if args == nil {
			continue
		}
		path := firstStringArg(args, src)
		if path == "" {
			continue
		}

		if attr == "route" {
			methods := methodsKeywordArg(args, src)
			if len(methods) == 0 {
				methods = []string{"GET"}
			}
			return methods[0], path
		}
		if v, ok := routeMethodAttrs[attr]; ok {
			return v, path
		}
	}
	return "", ""
}

func firstStringArg(args *sitter.Node, src []byte) string {
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c.Type() == "string" {
			return stringContent(c, src)
		}
	}
	return ""
}

func methodsKeywordArg(args *sitter.Node, src []byte) []string {
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c.Type() != "keyword_argument" {
			continue
		}
		nameNode := c.ChildByFieldName("name")
		if nameNode == nil || nameNode.Content(src) != "methods" {
			continue
		}
		valueNode := c.ChildByFieldName("value")
		if valueNode == nil {
			continue
		}
		var methods []string
		for j := 0; j < int(valueNode.NamedChildCount()); j++ {
			s := valueNode.NamedChild(j)
			if s.Type() == "string" {
				methods = append(methods, strings.ToUpper(stringContent(s, src)))
			}
		}
		return methods
	}
	return nil
}

func stringContent(strNode *sitter.Node, src []byte) string {
	if c := findChildOfType(strNode, "string_content"); c != nil {
		return c.Content(src)
	}
	return strings.Trim(strNode.Content(src), `"'`)
}

func findChildOfType(n *sitter.Node, t string) *sitter.Node {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c.Type() == t {
			return c
		}
	}
	return nil
}

func importName(n *sitter.Node, src []byte) string {
	if n.Type() == "aliased_import" {
		if name := n.ChildByFieldName("name"); name != nil {
			return name.Content(src)
		}
	}
	return n.Content(src)
}

func extractIdentifiers(n *sitter.Node, src []byte) []string {
	var names []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "identifier" {
			names = append(names, n.Content(src))
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return names
}

func signatureLine(full string) string {
	if idx := strings.Index(full, ":"); idx >= 0 {
		full = full[:idx]
	}
	full = strings.TrimSpace(strings.Join(strings.Fields(full), " "))
	if len(full) > 200 {
		full = full[:200]
	}
	return full
}

// extractCalls walks a function body collecting call target names — plain
// identifier calls (foo()) and the rightmost attribute of member calls
// (obj.method() -> "method"). The graph builder resolves these against
// known symbols; over-reporting is fine, unresolved names are dropped.
func extractCalls(n *sitter.Node, src []byte) []parser.CallRef {
	seen := map[string]bool{}
	var calls []parser.CallRef
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "call" {
			fn := n.ChildByFieldName("function")
			if fn != nil {
				name := callTarget(fn, src)
				if name != "" && !seen[name] {
					seen[name] = true
					calls = append(calls, parser.CallRef{Name: name, Line: int(n.StartPoint().Row) + 1})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return calls
}

func callTarget(fn *sitter.Node, src []byte) string {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "attribute":
		if attr := fn.ChildByFieldName("attribute"); attr != nil {
			return attr.Content(src)
		}
	}
	return ""
}
