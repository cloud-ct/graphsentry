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

	"github.com/cloud-ct/graphsentry/internal/parser"
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

	for name, typeName := range collectModuleVarTypes(root, src) {
		fa.ModuleVars = append(fa.ModuleVars, parser.ModuleVar{Name: name, TypeName: typeName})
	}

	return fa, nil
}

// collectModuleVarTypes scans a module's top-level statements (root's
// direct children only — deliberately not recursive, so an assignment
// inside a function or class body isn't mistaken for a module-scope one)
// for `name = ClassName(...)` assignments. These are how Python code
// commonly wires up singleton services (`chat_service = ChatService()` at
// the bottom of chat_service.py, then `from chat_service import
// chat_service` and `chat_service.create_assistant()` elsewhere) — a
// cross-file pattern a single file's analyzer pass can't resolve on its
// own, so it's surfaced here for the graph builder to reconcile once it's
// seen every file (see parser.CallRef.ReceiverVar).
func collectModuleVarTypes(root *sitter.Node, src []byte) map[string]string {
	hints := map[string]string{}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		stmt := root.NamedChild(i)
		if stmt.Type() != "expression_statement" {
			continue
		}
		assignment := findChildOfType(stmt, "assignment")
		if assignment == nil {
			continue
		}
		left := assignment.ChildByFieldName("left")
		right := assignment.ChildByFieldName("right")
		if left == nil || right == nil || left.Type() != "identifier" {
			continue
		}
		if className, ok := constructedClassName(right, src); ok {
			hints[left.Content(src)] = className
		}
	}
	return hints
}

// constructedClassName reports the class being constructed by a call
// expression, covering both forms Python code uses for this:
// `ClassName(...)` (function is a bare identifier) and
// `module_alias.ClassName(...)` (function is an attribute access reaching
// through an imported module — common when a module is imported directly,
// e.g. `from pkg import chat_service` importing the *module*
// chat_service.py, then `chat_service.ChatService()` constructing from
// it). The attribute form is only trusted when the accessed name is
// PascalCase (PEP 8: classes are PascalCase, everything else isn't) —
// otherwise this would misfire on an ordinary method call like
// `x.get_service()`.
func constructedClassName(call *sitter.Node, src []byte) (string, bool) {
	if call.Type() != "call" {
		return "", false
	}
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return "", false
	}
	switch fn.Type() {
	case "identifier":
		return fn.Content(src), true
	case "attribute":
		if attr := fn.ChildByFieldName("attribute"); attr != nil && isPascalCase(attr.Content(src)) {
			return attr.Content(src), true
		}
	}
	return "", false
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
				name, receiverType, receiverVar, ok := callTargetWithType(fn, src, className, selfAttrs, localHints)
				if ok {
					key := receiverType + "\x00" + receiverVar + "\x00" + name
					if name != "" && !seen[key] {
						seen[key] = true
						calls = append(calls, parser.CallRef{Name: name, ReceiverType: receiverType, ReceiverVar: receiverVar, Line: int(n.StartPoint().Row) + 1})
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

// callTargetWithType extracts a call's method/function name and, when
// possible, information about its receiver — a definite ReceiverType, a
// ReceiverVar for the builder to try resolving cross-file (see
// parser.CallRef.ReceiverVar), or neither for a receiver-less call.
// ok=false tells extractCalls to drop the call entirely: Python has no
// static typing to fall back on, so a bare-name "exactly one candidate in
// the repo" shortcut has nothing stopping it from matching a call through
// a completely unrelated, unresolvable receiver if it isn't refused here.
// Concretely:
//   - `foo()` (no receiver at all): ok=true, no type/var — the builder's
//     same-file-preferring bare-name heuristic is reasonably safe here,
//     since there's no receiver to be wrong about.
//   - `self.method()`: ok=true, ReceiverType=className — calling another
//     method of the same class, which we always know precisely.
//   - `self.attr.method()` / `local.method()` where the receiver was seen
//     constructed as `= ClassName(...)`: ok=true, ReceiverType=ClassName.
//   - `local.method()` where local's type isn't locally inferable (e.g. a
//     module-level singleton imported from elsewhere, or a constructor-
//     injected attribute): ok=true, ReceiverVar=the raw identifier name —
//     the builder tries resolving it against every file's module-level
//     vars (parser.FileAnalysis.ModuleVars), which a single file's
//     analyzer pass can't see.
//   - `module.ClassName(...)` — attribute name is PascalCase, strongly
//     suggesting a class constructor reached through an imported module
//     rather than a method call: ok=true, no type/var, deferring to the
//     bare-name heuristic keyed on the *class* name — much lower collision
//     risk repo-wide than an arbitrary method name.
//   - any chain two or more attributes deep (`client.beta.threads.create()`
//     — the type of the intermediate `client.beta.threads` isn't
//     something we can know without a real type checker, even though
//     `client` itself might be tracked): ok=false. Regression case:
//     ChatService.chat_message called client.beta.threads.create() (an
//     OpenAI SDK object, external to the repo) and it wrongly resolved to
//     the repo's own, unrelated ChatController.create — the only other
//     symbol named "create" — because this used to fall through to the
//     bare-name heuristic with no receiver information at all.
func callTargetWithType(fn *sitter.Node, src []byte, className string, selfAttrs, localHints map[string]string) (name, receiverType, receiverVar string, ok bool) {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src), "", "", true
	case "attribute":
		attrNode := fn.ChildByFieldName("attribute")
		obj := fn.ChildByFieldName("object")
		if attrNode == nil || obj == nil {
			return "", "", "", false
		}
		name = attrNode.Content(src)

		if obj.Type() == "identifier" {
			objName := obj.Content(src)
			if objName == "self" {
				return name, className, "", true
			}
			if t, found := localHints[objName]; found {
				return name, t, "", true
			}
			if isPascalCase(name) {
				// `module_alias.ClassName(...)`: constructing a class
				// reached through an imported module, not calling a
				// method — defer to the bare-name heuristic on the class
				// name itself, which collides far less often than an
				// arbitrary method name would.
				return name, "", "", true
			}
			return name, "", objName, true // untyped receiver — let the builder try resolving objName as a module-level singleton
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
					return name, t, "", true
				}
			}
		}
		return "", "", "", false
	}
	return "", "", "", false
}

// isPascalCase reports whether s looks like a Python class name by
// convention (PEP 8): starts with an uppercase letter. Functions,
// methods, and variables are conventionally snake_case, so this is a
// cheap, reasonably reliable signal for "this attribute access is
// constructing a class, not calling a method".
func isPascalCase(s string) bool {
	if s == "" {
		return false
	}
	r := s[0]
	return r >= 'A' && r <= 'Z'
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
			if left != nil && right != nil && left.Type() == "attribute" {
				obj := left.ChildByFieldName("object")
				attr := left.ChildByFieldName("attribute")
				if obj != nil && attr != nil && obj.Type() == "identifier" && obj.Content(src) == "self" {
					if className, ok := constructedClassName(right, src); ok {
						hints[attr.Content(src)] = className
					}
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
			if left != nil && right != nil && left.Type() == "identifier" {
				if className, ok := constructedClassName(right, src); ok {
					hints[left.Content(src)] = className
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
}
