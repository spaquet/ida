package extract

import (
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_js "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	ts_ts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var (
	jsLanguage  = sitter.NewLanguage(ts_js.Language())
	tsLanguage  = sitter.NewLanguage(ts_ts.LanguageTypescript())
	tsxLanguage = sitter.NewLanguage(ts_ts.LanguageTSX())

	stimulusControllerFile = regexp.MustCompile(`(?:^|/)controllers/(.+)_controller\.(?:js|jsx|ts|tsx)$`)
	stimulusStaticField    = regexp.MustCompile(`^(targets|values|classes|outlets)$`)
	hookName               = regexp.MustCompile(`^use[A-Z]`)
)

// jsts parses a JavaScript/TypeScript/JSX/TSX file with tree-sitter and
// extracts modules (imports), exports (functions/classes/consts), a
// heuristic subset of those that look like React components or hooks, JSX
// component-usage sites, and Stimulus controller declarations (identifier,
// targets/values/classes/outlets, action/lifecycle methods).
func jsts(path string, content []byte) []Node {
	lang := jsLanguage
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts":
		lang = tsLanguage
	case ".tsx":
		lang = tsxLanguage
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil
	}
	tree := parser.Parse(content, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()

	w := &jsWalker{path: path, src: content}
	if identifier, ok := stimulusIdentifier(path); ok {
		w.stimulusIdentifier = identifier
	}
	w.walk(tree.RootNode())
	return w.nodes
}

// stimulusIdentifier derives the Stimulus controller identifier for a file
// path, matching Stimulus's own naming convention: underscores become
// dashes, and nested controllers/ subdirectories join with "--", e.g.
// controllers/nested/hello_controller.js -> nested--hello.
func stimulusIdentifier(path string) (string, bool) {
	m := stimulusControllerFile.FindStringSubmatch(path)
	if m == nil {
		return "", false
	}
	parts := strings.Split(m[1], "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, "_", "-")
	}
	return strings.Join(parts, "--"), true
}

type jsWalker struct {
	path               string
	src                []byte
	stimulusIdentifier string
	nodes              []Node
}

func (w *jsWalker) text(n *sitter.Node) string {
	return n.Utf8Text(w.src)
}

func (w *jsWalker) line(n *sitter.Node) int {
	return int(n.StartPosition().Row) + 1
}

// walk visits every node in the tree once. Declarations are handled
// wherever they appear (top-level, exported, or nested) rather than only
// under export_statement, so a component defined and used only within the
// same file (never exported) is still recorded for JSX-use resolution.
func (w *jsWalker) walk(n *sitter.Node) {
	switch n.Kind() {
	case "import_statement":
		w.handleImport(n)
	case "class_declaration", "class":
		w.handleClass(n, "")
	case "function_declaration", "generator_function_declaration":
		if nameNode := n.ChildByFieldName("name"); nameNode != nil {
			w.emitFunctionLike(w.text(nameNode), n, n.ChildByFieldName("body"))
		}
	case "lexical_declaration", "variable_declaration":
		w.handleVariableDeclaration(n)
	case "jsx_opening_element", "jsx_self_closing_element":
		w.handleJSXUse(n)
	}
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.Children(cursor) {
		child := child
		w.walk(&child)
	}
}

func (w *jsWalker) handleImport(n *sitter.Node) {
	source := n.ChildByFieldName("source")
	if source == nil {
		return
	}
	spec := unquote(w.text(source))
	if spec == "" {
		return
	}
	w.nodes = append(w.nodes, node(w.path, "js_import", spec, spec, w.line(n), w.line(n), "js-modules-v1"))
}

func (w *jsWalker) handleVariableDeclaration(n *sitter.Node) {
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.NamedChildren(cursor) {
		if child.Kind() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}
		if valueNode.Kind() == "arrow_function" || valueNode.Kind() == "function_expression" {
			w.emitFunctionLike(w.text(nameNode), &child, valueNode.ChildByFieldName("body"))
		}
	}
}

// emitFunctionLike records a top-level exported function/const-arrow as a
// js_export, and further as js_component (PascalCase name, JSX in body) or
// js_hook (use* name) when the heuristic matches.
func (w *jsWalker) emitFunctionLike(name string, declLine *sitter.Node, body *sitter.Node) {
	if name == "" {
		return
	}
	line := w.line(declLine)
	kind := "js_export"
	if isPascalCase(name) && body != nil && containsJSX(body) {
		kind = "js_component"
	} else if hookName.MatchString(name) {
		kind = "js_hook"
	}
	w.nodes = append(w.nodes, node(w.path, kind, name, name, line, line, "js-modules-v1"))
}

// handleClass records a class_declaration as a js_export (js_component when
// its name is PascalCase and it extends a *Component base), and — when this
// file's path matches the Stimulus controllers/*_controller convention and
// the class extends something named *Controller — walks its body for
// targets/values/classes/outlets and action/lifecycle methods.
func (w *jsWalker) handleClass(n *sitter.Node, fallbackName string) {
	name := fallbackName
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		name = w.text(nameNode)
	}
	heritage := extendsName(n, w.src)

	if name != "" {
		kind := "js_export"
		if isPascalCase(name) && strings.Contains(heritage, "Component") {
			kind = "js_component"
		}
		line := w.line(n)
		w.nodes = append(w.nodes, node(w.path, kind, name, name, line, line, "js-modules-v1"))
	}

	if w.stimulusIdentifier == "" || !strings.Contains(heritage, "Controller") {
		return
	}
	line := w.line(n)
	w.nodes = append(w.nodes, node(w.path, "stimulus_controller", w.stimulusIdentifier, w.stimulusIdentifier, line, line, "stimulus-v1"))
	w.walkControllerBody(n.ChildByFieldName("body"))
}

func extendsName(classDecl *sitter.Node, src []byte) string {
	cursor := classDecl.Walk()
	defer cursor.Close()
	for _, child := range classDecl.Children(cursor) {
		if child.Kind() != "class_heritage" {
			continue
		}
		return child.Utf8Text(src)
	}
	return ""
}

func (w *jsWalker) walkControllerBody(body *sitter.Node) {
	if body == nil {
		return
	}
	cursor := body.Walk()
	defer cursor.Close()
	for _, member := range body.NamedChildren(cursor) {
		member := member
		switch member.Kind() {
		case "field_definition":
			w.handleControllerField(&member)
		case "method_definition":
			w.handleControllerMethod(&member)
		}
	}
}

func (w *jsWalker) handleControllerField(n *sitter.Node) {
	if !strings.HasPrefix(w.text(n), "static") {
		return
	}
	property := n.ChildByFieldName("property")
	value := n.ChildByFieldName("value")
	if property == nil || value == nil {
		return
	}
	field := w.text(property)
	if !stimulusStaticField.MatchString(field) {
		return
	}
	kind := "stimulus_" + strings.TrimSuffix(field, "s")
	line := w.line(n)
	switch value.Kind() {
	case "array":
		for _, item := range stringLiteralElements(value, w.src) {
			w.nodes = append(w.nodes, node(w.path, kind, item, w.stimulusIdentifier+"#"+item, line, line, "stimulus-v1"))
		}
	case "object":
		for _, key := range objectKeys(value, w.src) {
			w.nodes = append(w.nodes, node(w.path, kind, key, w.stimulusIdentifier+"#"+key, line, line, "stimulus-v1"))
		}
	}
}

func (w *jsWalker) handleControllerMethod(n *sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := w.text(nameNode)
	line := w.line(n)
	w.nodes = append(w.nodes, node(w.path, "method", name, w.stimulusIdentifier+"#"+name, line, line, "stimulus-v1"))
}

func (w *jsWalker) handleJSXUse(n *sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := w.text(nameNode)
	if !isPascalCase(name) {
		return
	}
	line := w.line(n)
	w.nodes = append(w.nodes, node(w.path, "jsx_use", name, name, line, line, "jsx-v1"))
}

func stringLiteralElements(array *sitter.Node, src []byte) []string {
	var result []string
	cursor := array.Walk()
	defer cursor.Close()
	for _, item := range array.NamedChildren(cursor) {
		if item.Kind() != "string" {
			continue
		}
		if s := unquote(item.Utf8Text(src)); s != "" {
			result = append(result, s)
		}
	}
	return result
}

func objectKeys(obj *sitter.Node, src []byte) []string {
	var result []string
	cursor := obj.Walk()
	defer cursor.Close()
	for _, pair := range obj.NamedChildren(cursor) {
		if pair.Kind() != "pair" {
			continue
		}
		key := pair.ChildByFieldName("key")
		if key == nil {
			continue
		}
		name := key.Utf8Text(src)
		if key.Kind() == "string" {
			name = unquote(name)
		}
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

// containsJSX reports whether n's subtree contains a JSX element, used to
// distinguish a React component from a plain helper function/const.
func containsJSX(n *sitter.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind() == "jsx_element" || n.Kind() == "jsx_self_closing_element" {
		return true
	}
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.Children(cursor) {
		if containsJSX(&child) {
			return true
		}
	}
	return false
}

func isPascalCase(name string) bool {
	if name == "" {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
