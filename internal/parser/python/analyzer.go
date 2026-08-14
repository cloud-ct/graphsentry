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
				selfAttrs := collectSelfAttrTypes(body, src)
				for i := 0; i < int(body.NamedChildCount()); i++ {
					collectFunction(body.NamedChild(i), className, selfAttrs, fa, src, line, text)
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
			collectFunction(n, "", nil, fa, src, line, text)
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
// endpoint symbol. selfAttrs (nil for module-level functions, which have
// no `self`) supplies the class's self.attr -> ClassName type hints so
// calls through them can be scoped instead of falling back to a repo-wide
// bare-name guess.
func collectFunction(n *sitter.Node, className string, selfAttrs map[string]string, fa *parser.FileAnalysis, src []byte,
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

	localHints := map[string]string{}
	if body := funcNode.ChildByFieldName("body"); body != nil {
		collectLocalVarTypes(body, src, localHints)
	}

	fa.Symbols = append(fa.Symbols, parser.Symbol{
		Kind: kind, Name: fName, Qualified: qualified,
		StartLine: start, EndLine: end,
		Signature: signatureLine(text(funcNode)),
		Calls:     extractCalls(funcNode, src, className, selfAttrs, localHints),
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

// extractCalls walks a function body collecting call targets: plain
// identifier calls (foo(), resolved later by the builder's same-file-
// preferring bare-name heuristic — safe, since a receiver-less call is
// most often to something in the same file) and member calls
// (obj.method()), which are only kept when callTargetWithType can
// confidently attach a ReceiverType. An attribute call whose receiver
// type can't be determined is dropped entirely rather than falling back
// to the bare-name heuristic — see callTargetWithType for why a
// permissive fallback there is actively dangerous in Python, where
// there's no static typing to lean on at all.
func extractCalls(n *sitter.Node, src []byte, className string, selfAttrs, localHints map[string]string) []parser.CallRef {
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
				name, receiverType, ok := callTargetWithType(fn, src, className, selfAttrs, localHints)
				if ok {
					key := receiverType + "\x00" + name
					if name != "" && !seen[key] {
						seen[key] = true
						calls = append(calls, parser.CallRef{Name: name, ReceiverType: receiverType, Line: int(n.StartPoint().Row) + 1})
					}
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

// callTargetWithType extracts a call's method/function name and, when the
// receiver is one we can actually reason about, its type — returning
// ok=false when it isn't, which tells extractCalls to drop the call
// entirely instead of resolving it. This matters because Python has no
// static typing to fall back on: a bare-name resolution "exactly one
// candidate in the repo" shortcut has no type system stopping it from
// matching a call through a completely unrelated, unresolvable receiver.
// Concretely:
//   - `foo()` (no receiver at all): ok=true, ReceiverType="" — the
//     builder's same-file-preferring bare-name heuristic is reasonably
//     safe here, since there's no receiver to be wrong about.
//   - `self.method()`: ok=true, ReceiverType=className — calling another
//     method of the same class, which we always know precisely.
//   - `self.attr.method()` where self.attr was seen as
//     `self.attr = ClassName(...)`: ok=true, ReceiverType=ClassName.
//   - `local.method()` where local was seen as `local = ClassName(...)`:
//     ok=true, ReceiverType=ClassName.
//   - anything else — `self.attr.method()` with an untracked attr (e.g.
//     set via constructor-injected `self.attr = attr`), a receiver that's
//     itself a call's result, or any chain two or more attributes deep
//     (`client.beta.threads.create()` — the type of the intermediate
//     `client.beta.threads` isn't something we can know without a real
//     type checker, even though `client` itself might be tracked): ok=false.
//     Regression case: ChatService.chat_message called
//     client.beta.threads.create() (an OpenAI SDK object, external to the
//     repo) and it wrongly resolved to the repo's own, unrelated
//     ChatController.create — the only other symbol named "create" —
//     because this used to fall through to the bare-name heuristic with
//     no receiver type at all.
func callTargetWithType(fn *sitter.Node, src []byte, className string, selfAttrs, localHints map[string]string) (name, receiverType string, ok bool) {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src), "", true
	case "attribute":
		attrNode := fn.ChildByFieldName("attribute")
		obj := fn.ChildByFieldName("object")
		if attrNode == nil || obj == nil {
			return "", "", false
		}
		name = attrNode.Content(src)

		if obj.Type() == "identifier" {
			objName := obj.Content(src)
			if objName == "self" {
				return name, className, true
			}
			if t, found := localHints[objName]; found {
				return name, t, true
			}
			return "", "", false // untyped receiver (e.g. a constructor-injected attribute) — refuse rather than guess
		}

		// `self.attr.method()` — exactly one level of attribute chaining
		// under self — is the one deeper shape we can still resolve
		// confidently, via the tracked type of that specific attribute.
		// Anything deeper (obj itself being a multi-level chain) is out
		// of reach without real type inference.
		if obj.Type() == "attribute" {
			innerObj := obj.ChildByFieldName("object")
			innerAttr := obj.ChildByFieldName("attribute")
			if innerObj != nil && innerAttr != nil && innerObj.Type() == "identifier" && innerObj.Content(src) == "self" {
				if t, found := selfAttrs[innerAttr.Content(src)]; found {
					return name, t, true
				}
			}
		}
		return "", "", false
	}
	return "", "", false
}

// collectSelfAttrTypes scans an entire class body (every method, not just
// __init__ — attributes are sometimes set elsewhere) for
// `self.attr = ClassName(...)` assignments, returning attr name -> class
// name. This is the Python analog of the field-type tracking the C#
// analyzer does for `private readonly IFoo _foo;` — a receiver type known
// to the analyzer is what lets the graph builder route a call precisely
// instead of guessing among every same-named method in the repo.
func collectSelfAttrTypes(classBody *sitter.Node, src []byte) map[string]string {
	hints := map[string]string{}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "assignment" {
			left := n.ChildByFieldName("left")
			right := n.ChildByFieldName("right")
			if left != nil && right != nil && left.Type() == "attribute" && right.Type() == "call" {
				obj := left.ChildByFieldName("object")
				attr := left.ChildByFieldName("attribute")
				fn := right.ChildByFieldName("function")
				if obj != nil && attr != nil && fn != nil && obj.Type() == "identifier" && obj.Content(src) == "self" && fn.Type() == "identifier" {
					hints[attr.Content(src)] = fn.Content(src)
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(classBody)
	return hints
}

// collectLocalVarTypes extends hints (in place) with a function body's
// local `x = ClassName(...)` assignments — the Python analog of the C#
// analyzer's `var x = new Foo(...)` inference. Only a direct constructor
// call on the right-hand side counts; `x = some_call()` can't be
// attributed a type from syntax alone and is left unhinted.
func collectLocalVarTypes(body *sitter.Node, src []byte, hints map[string]string) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "assignment" {
			left := n.ChildByFieldName("left")
			right := n.ChildByFieldName("right")
			if left != nil && right != nil && left.Type() == "identifier" && right.Type() == "call" {
				if fn := right.ChildByFieldName("function"); fn != nil && fn.Type() == "identifier" {
					hints[left.Content(src)] = fn.Content(src)
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
}
