// Package csharp implements parser.LanguageAnalyzer for C# source files
// using tree-sitter. It recognizes ASP.NET-style attribute routing
// ([HttpGet], [HttpPost], [Route(...)]) to extract HTTP endpoints, and
// does lightweight, heuristic type tracking (field declarations + local
// `var x = new T()` / `T x = ...` declarations) so call resolution can
// route interface-typed calls to their implementation and refuse to guess
// when a call's receiver isn't a type declared anywhere in the repo (e.g.
// a BCL/vendor type) — see CallRef in the parser package for why this
// matters.
package csharp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"

	"github.com/huandert/repolens/internal/parser"
)

var httpVerbAttr = regexp.MustCompile(`^Http(Get|Post|Put|Delete|Patch)$`)

// Analyzer extracts classes, interfaces, methods, imports, and ASP.NET
// endpoints from C# files.
type Analyzer struct{}

// New returns a C# LanguageAnalyzer.
func New() *Analyzer { return &Analyzer{} }

func (a *Analyzer) Language() string     { return "csharp" }
func (a *Analyzer) Extensions() []string { return []string{".cs"} }

func (a *Analyzer) Analyze(path string, content []byte) (*parser.FileAnalysis, error) {
	p := sitter.NewParser()
	p.SetLanguage(csharp.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	defer tree.Close()

	fa := &parser.FileAnalysis{Path: path, Language: "csharp"}
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
		case "using_directive":
			// using_directive children include a qualified_name/identifier for the namespace.
			for i := 0; i < int(n.ChildCount()); i++ {
				c := n.Child(i)
				if c.Type() == "qualified_name" || c.Type() == "identifier" {
					startLine, _ := line(n)
					fa.Imports = append(fa.Imports, parser.Import{Path: text(c), Line: startLine})
					break
				}
			}
			return // no children worth descending into

		case "class_declaration", "interface_declaration":
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil {
				break
			}
			name := text(nameNode)
			start, end := line(n)
			kind := parser.KindClass
			if n.Type() == "interface_declaration" {
				kind = parser.KindInterface
			}
			var implements []string
			if base := findChildOfType(n, "base_list"); base != nil {
				implements = extractIdentifiers(base, src)
			}
			fa.Symbols = append(fa.Symbols, parser.Symbol{
				Kind: kind, Name: name, Qualified: name,
				StartLine: start, EndLine: end,
				Signature:  signatureLine(text(n)),
				Implements: implements,
			})

			route := attributeRoute(n, src, "Route")
			body := n.ChildByFieldName("body")
			fieldTypes := classFieldTypes(body, src)
			if body != nil {
				for i := 0; i < int(body.ChildCount()); i++ {
					walkClassMember(body.Child(i), name, route, fieldTypes, fa, src, line, text)
				}
			}
			return // members handled by walkClassMember, don't double-walk
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	return fa, nil
}

// classFieldTypes scans a class body's direct field_declaration children
// and returns a map of field name -> declared type name (generics/nullable
// markers stripped), used as type hints when resolving `_field.Method()`
// calls inside the class's methods.
func classFieldTypes(classBody *sitter.Node, src []byte) map[string]string {
	types := map[string]string{}
	if classBody == nil {
		return types
	}
	for i := 0; i < int(classBody.ChildCount()); i++ {
		c := classBody.Child(i)
		if c.Type() != "field_declaration" {
			continue
		}
		varDecl := findChildOfType(c, "variable_declaration")
		if varDecl == nil {
			continue
		}
		typeNode := varDecl.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		typeName := normalizeTypeName(typeNode.Content(src))
		for _, decl := range findChildrenOfType(varDecl, "variable_declarator") {
			if nameNode := decl.ChildByFieldName("name"); nameNode != nil {
				types[nameNode.Content(src)] = typeName
			}
		}
	}
	return types
}

// collectParameterTypes extends hints (in place) with a method's parameter
// types — `SendRawAsync(ClientWebSocket ws, ...)` means calls on `ws`
// inside the body should be scoped to ClientWebSocket, not resolved as
// bare method names. This is what catches the case a field/local-only scan
// misses: a parameter whose type is a BCL/vendor type happening to share a
// method name with something the containing class itself defines.
func collectParameterTypes(paramList *sitter.Node, src []byte, hints map[string]string) {
	for _, param := range findChildrenOfType(paramList, "parameter") {
		typeNode := param.ChildByFieldName("type")
		nameNode := param.ChildByFieldName("name")
		if typeNode == nil || nameNode == nil {
			continue
		}
		hints[nameNode.Content(src)] = normalizeTypeName(typeNode.Content(src))
	}
}

// collectLocalTypes extends hints (in place) with the declared/inferred
// types of local variables in a method body: `Foo x = ...` uses the
// explicit type; `var x = new Foo(...)` infers Foo from the initializer.
// `var x = SomeCall()` can't be inferred from syntax alone and is left
// unset — calls through x then fall back to the old bare-name heuristic
// rather than getting a (possibly wrong) type hint.
func collectLocalTypes(body *sitter.Node, src []byte, hints map[string]string) {
	if body == nil {
		return
	}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "local_declaration_statement" {
			if varDecl := findChildOfType(n, "variable_declaration"); varDecl != nil {
				typeNode := varDecl.ChildByFieldName("type")
				declaredType := ""
				if typeNode != nil && typeNode.Type() != "implicit_type" {
					declaredType = normalizeTypeName(typeNode.Content(src))
				}
				for _, decl := range findChildrenOfType(varDecl, "variable_declarator") {
					nameNode := decl.ChildByFieldName("name")
					if nameNode == nil {
						continue
					}
					resolvedType := declaredType
					if resolvedType == "" && decl.ChildCount() > 0 {
						// `var x = new Foo(...)` — infer from the
						// initializer, which tree-sitter-c-sharp places as
						// the declarator's last child (identifier, "=",
						// value). Only a direct object_creation_expression
						// counts, not one buried inside a call's
						// arguments (e.g. `var x = Foo(new Bar())` should
						// not attribute Bar's type to x).
						if last := decl.Child(int(decl.ChildCount()) - 1); last.Type() == "object_creation_expression" {
							if t := last.ChildByFieldName("type"); t != nil {
								resolvedType = normalizeTypeName(t.Content(src))
							}
						}
					}
					if resolvedType != "" {
						hints[nameNode.Content(src)] = resolvedType
					}
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
}

// walkClassMember handles method_declaration nodes inside a class body,
// qualifying them as Class.Method and detecting HTTP endpoint attributes.
func walkClassMember(n *sitter.Node, className, classRoute string, fieldTypes map[string]string, fa *parser.FileAnalysis, src []byte,
	line func(*sitter.Node) (int, int), text func(*sitter.Node) string) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "method_declaration":
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			return
		}
		mName := text(nameNode)
		start, end := line(n)

		hints := make(map[string]string, len(fieldTypes))
		for k, v := range fieldTypes {
			hints[k] = v
		}
		if params := n.ChildByFieldName("parameters"); params != nil {
			collectParameterTypes(params, src, hints)
		}
		if body := n.ChildByFieldName("body"); body != nil {
			collectLocalTypes(body, src, hints)
		}
		calls, creates := extractCallsAndCreates(n, src, hints, className)

		fa.Symbols = append(fa.Symbols, parser.Symbol{
			Kind: parser.KindMethod, Name: mName, Qualified: className + "." + mName,
			StartLine: start, EndLine: end,
			Signature: signatureLine(text(n)),
			Calls:     calls,
			Creates:   creates,
		})

		if verb, route := endpointFromAttributes(n, src); verb != "" {
			full := strings.TrimRight(classRoute, "/") + "/" + strings.TrimLeft(route, "/")
			full = strings.ReplaceAll(full, "[controller]", strings.TrimSuffix(className, "Controller"))
			full = strings.TrimSuffix(full, "/")
			if full == "" {
				full = "/"
			}
			fa.Symbols = append(fa.Symbols, parser.Symbol{
				Kind: parser.KindEndpoint, Name: verb + " " + full, Qualified: verb + " " + full,
				StartLine: start, EndLine: end,
				Signature: signatureLine(text(n)),
				Calls:     []parser.CallRef{{Name: className + "." + mName}},
			})
		}
	case "class_declaration", "interface_declaration", "struct_declaration":
		// Nested types: skip — rare in this codebase's controllers/services
		// and not worth the complexity of a separate field-type scope.
	default:
		for i := 0; i < int(n.ChildCount()); i++ {
			walkClassMember(n.Child(i), className, classRoute, fieldTypes, fa, src, line, text)
		}
	}
}

// attributeRoute finds an attribute like [Route("api/[controller]")] among
// n's preceding attribute lists and returns its string argument, or "".
func attributeRoute(n *sitter.Node, src []byte, attrName string) string {
	attrs := attributeLists(n)
	for _, al := range attrs {
		verb, arg := parseAttribute(al, src)
		if verb == attrName {
			return arg
		}
	}
	return ""
}

// endpointFromAttributes scans a method's attribute lists for an HTTP verb
// attribute ([HttpGet], [HttpPost("id")], ...) and returns the verb and
// route argument (possibly empty).
func endpointFromAttributes(n *sitter.Node, src []byte) (verb, route string) {
	for _, al := range attributeLists(n) {
		name, arg := parseAttribute(al, src)
		if m := httpVerbAttr.FindStringSubmatch(name); m != nil {
			return strings.ToUpper(m[1]), arg
		}
	}
	return "", ""
}

// attributeLists returns the attribute_list nodes attached to a
// declaration (siblings preceding it that tree-sitter-c-sharp nests inside
// the declaration node itself as leading children).
func attributeLists(n *sitter.Node) []*sitter.Node {
	var lists []*sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.Type() == "attribute_list" {
			lists = append(lists, c)
		}
	}
	return lists
}

// parseAttribute extracts an attribute's name and its first string-literal
// argument from an attribute_list node, e.g. [Route("api/users")] ->
// ("Route", "api/users"). tree-sitter-c-sharp doesn't expose "arg_list" or
// string content as named fields, so this walks by node type instead.
func parseAttribute(attrList *sitter.Node, src []byte) (name, arg string) {
	attrNode := findChildOfType(attrList, "attribute")
	if attrNode == nil {
		return "", ""
	}
	if nn := attrNode.ChildByFieldName("name"); nn != nil {
		name = nn.Content(src)
	}
	if argsNode := findChildOfType(attrNode, "attribute_argument_list"); argsNode != nil {
		if strLit := findDescendantOfType(argsNode, "string_literal"); strLit != nil {
			if content := findChildOfType(strLit, "string_literal_content"); content != nil {
				arg = content.Content(src)
			}
		}
	}
	return name, arg
}

// findChildOfType returns the first direct child of n with the given type.
func findChildOfType(n *sitter.Node, t string) *sitter.Node {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c.Type() == t {
			return c
		}
	}
	return nil
}

// findChildrenOfType returns every direct child of n with the given type
// (unlike findChildOfType, which stops at the first).
func findChildrenOfType(n *sitter.Node, t string) []*sitter.Node {
	var out []*sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c.Type() == t {
			out = append(out, c)
		}
	}
	return out
}

// findDescendantOfType does a depth-first search for the first descendant
// of the given type (unlike findChildOfType, which only checks direct
// children).
func findDescendantOfType(n *sitter.Node, t string) *sitter.Node {
	if n.Type() == t {
		return n
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if found := findDescendantOfType(n.Child(i), t); found != nil {
			return found
		}
	}
	return nil
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
	if idx := strings.Index(full, "{"); idx >= 0 {
		full = full[:idx]
	}
	full = strings.TrimSpace(strings.Join(strings.Fields(full), " "))
	if len(full) > 200 {
		full = full[:200]
	}
	return full
}

// normalizeTypeName strips generic type arguments, nullable markers, and
// array brackets so type hints compare cleanly against declared class/
// interface names: "List<Foo>" -> "List", "Foo?" -> "Foo", "Bar[]" -> "Bar".
func normalizeTypeName(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, "<"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSuffix(s, "?")
	s = strings.TrimSuffix(s, "[]")
	return strings.TrimSpace(s)
}

// extractCallsAndCreates walks a method body collecting both call targets
// (with a receiver type hint when typeHints/this-resolution can supply
// one) and the type names it instantiates via `new`.
func extractCallsAndCreates(n *sitter.Node, src []byte, typeHints map[string]string, className string) ([]parser.CallRef, []string) {
	seenCall := map[string]bool{}
	seenCreate := map[string]bool{}
	var calls []parser.CallRef
	var creates []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case "invocation_expression":
			if fn := n.ChildByFieldName("function"); fn != nil {
				name, receiverType := callTargetWithType(fn, src, typeHints, className)
				key := receiverType + "\x00" + name
				if name != "" && !seenCall[key] {
					seenCall[key] = true
					calls = append(calls, parser.CallRef{Name: name, ReceiverType: receiverType})
				}
			}
		case "object_creation_expression":
			if t := n.ChildByFieldName("type"); t != nil {
				typeName := normalizeTypeName(t.Content(src))
				if typeName != "" && !seenCreate[typeName] {
					seenCreate[typeName] = true
					creates = append(creates, typeName)
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return calls, creates
}

// callTargetWithType extracts a call's method name and, when possible, the
// declared type of its receiver: `this.Foo()` resolves to the enclosing
// class; `field.Foo()` or `local.Foo()` resolves via typeHints; anything
// else (chained access, static class calls, base.Foo()) yields no hint and
// falls back to the old bare-name heuristic in the graph builder.
func callTargetWithType(fn *sitter.Node, src []byte, typeHints map[string]string, className string) (name, receiverType string) {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src), ""
	case "member_access_expression":
		nameNode := fn.ChildByFieldName("name")
		if nameNode == nil {
			return "", ""
		}
		name = nameNode.Content(src)
		exprNode := fn.ChildByFieldName("expression")
		if exprNode == nil {
			return name, ""
		}
		switch exprNode.Type() {
		case "this_expression":
			return name, className
		case "identifier":
			if t, ok := typeHints[exprNode.Content(src)]; ok {
				return name, t
			}
		}
		return name, ""
	}
	return "", ""
}
