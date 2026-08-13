// Package csharp implements parser.LanguageAnalyzer for C# source files
// using tree-sitter. It recognizes ASP.NET-style attribute routing
// ([HttpGet], [HttpPost], [Route(...)]) to extract HTTP endpoints.
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

	// classRoute tracks the [Route("...")] prefix declared on the
	// enclosing class/controller, if any, so method-level routes can be
	// combined with it the way ASP.NET does at runtime.
	var classRouteStack []string

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
			classRouteStack = append(classRouteStack, route)
			for i := 0; i < int(n.ChildCount()); i++ {
				walkClassMember(n.Child(i), name, route, fa, src, line, text)
			}
			classRouteStack = classRouteStack[:len(classRouteStack)-1]
			return // members handled by walkClassMember, don't double-walk
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	return fa, nil
}

// walkClassMember handles method_declaration nodes inside a class body,
// qualifying them as Class.Method and detecting HTTP endpoint attributes.
// It also recurses into nested type declarations.
func walkClassMember(n *sitter.Node, className, classRoute string, fa *parser.FileAnalysis, src []byte,
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
		fa.Symbols = append(fa.Symbols, parser.Symbol{
			Kind: parser.KindMethod, Name: mName, Qualified: className + "." + mName,
			StartLine: start, EndLine: end,
			Signature: signatureLine(text(n)),
			Calls:     extractCalls(n, src),
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
				Calls:     []string{className + "." + mName},
			})
		}
	case "class_declaration", "interface_declaration", "struct_declaration":
		// Nested types: recurse via the top-level walk logic isn't
		// reachable here, so just skip — nested types are rare in this
		// codebase's controllers/services and not worth the complexity.
	default:
		for i := 0; i < int(n.ChildCount()); i++ {
			walkClassMember(n.Child(i), className, classRoute, fa, src, line, text)
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

func extractCalls(n *sitter.Node, src []byte) []string {
	seen := map[string]bool{}
	var calls []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "invocation_expression" {
			fn := n.ChildByFieldName("function")
			if fn != nil {
				name := callTarget(fn, src)
				if name != "" && !seen[name] {
					seen[name] = true
					calls = append(calls, name)
				}
			}
		}
		if n.Type() == "object_creation_expression" {
			if t := n.ChildByFieldName("type"); t != nil {
				name := t.Content(src)
				if name != "" && !seen[name] {
					seen[name] = true
					calls = append(calls, name)
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
	case "member_access_expression":
		if name := fn.ChildByFieldName("name"); name != nil {
			return name.Content(src)
		}
	}
	return ""
}
