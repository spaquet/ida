package extract

import (
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_css "github.com/tree-sitter/tree-sitter-css/bindings/go"
)

var cssLanguage = sitter.NewLanguage(ts_css.Language())

// css parses an authored CSS/SCSS file with tree-sitter and records a
// css_class node for each rule using a Tailwind `@apply` directive, plus a
// class_attr_use node aggregating the utility tokens named in those
// directives so resolveTailwind can connect custom theme tokens to their
// uses, matching the "no per-utility-class node" constraint in
// product-requirements.md.
func css(path string, content []byte) []Node {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(cssLanguage); err != nil {
		return nil
	}
	tree := parser.Parse(content, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()

	var nodes []Node
	var tokens []string
	firstLine := 0

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "rule_set" {
			if applied, ok := applyTokens(n, content); ok {
				selector := strings.TrimSpace(childOfKind(n, "selectors").Utf8Text(content))
				line := int(n.StartPosition().Row) + 1
				nodes = append(nodes, node(path, "css_class", selector, selector, line, line, "css-apply-v1"))
				if firstLine == 0 {
					firstLine = line
				}
				tokens = append(tokens, applied...)
			}
		}
		cursor := n.Walk()
		defer cursor.Close()
		for _, child := range n.NamedChildren(cursor) {
			child := child
			walk(&child)
		}
	}
	walk(tree.RootNode())

	if len(tokens) > 0 {
		blob := strings.Join(dedupe(tokens), " ")
		nodes = append(nodes, node(path, "class_attr_use", blob, blob, firstLine, firstLine, "css-apply-v1"))
	}
	return nodes
}

// applyTokens returns the utility-class tokens named in a rule's @apply
// directive, if it has one.
func applyTokens(ruleSet *sitter.Node, src []byte) ([]string, bool) {
	block := childOfKind(ruleSet, "block")
	if block == nil {
		return nil, false
	}
	var tokens []string
	found := false
	cursor := block.Walk()
	defer cursor.Close()
	for _, stmt := range block.NamedChildren(cursor) {
		if stmt.Kind() != "postcss_statement" {
			continue
		}
		kw := childOfKind(&stmt, "at_keyword")
		if kw == nil || strings.TrimSpace(kw.Utf8Text(src)) != "@apply" {
			continue
		}
		found = true
		sc := stmt.Walk()
		for _, part := range stmt.NamedChildren(sc) {
			if part.Kind() != "plain_value" {
				continue
			}
			tokens = append(tokens, strings.Fields(part.Utf8Text(src))...)
		}
		sc.Close()
	}
	return tokens, found
}

func childOfKind(n *sitter.Node, kind string) *sitter.Node {
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.Children(cursor) {
		if child.Kind() == kind {
			c := child
			return &c
		}
	}
	return nil
}

// tailwindConfig parses tailwind.config.js/.ts and records a tailwind_token
// node for each key under theme.extend.<category>, e.g. theme.extend.colors
// = { primary: ... } becomes token "colors.primary".
func tailwindConfig(path string, content []byte) []Node {
	lang := jsLanguage
	if strings.ToLower(filepath.Ext(path)) == ".ts" {
		lang = tsLanguage
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

	var nodes []Node
	var findExtend func(n *sitter.Node)
	findExtend = func(n *sitter.Node) {
		if n.Kind() == "pair" {
			key := n.ChildByFieldName("key")
			value := n.ChildByFieldName("value")
			if key != nil && value != nil && unquote(key.Utf8Text(content)) == "extend" && value.Kind() == "object" {
				nodes = append(nodes, tailwindTokens(path, value, content)...)
				return
			}
		}
		cursor := n.Walk()
		defer cursor.Close()
		for _, child := range n.NamedChildren(cursor) {
			child := child
			findExtend(&child)
		}
	}
	findExtend(tree.RootNode())
	return nodes
}

func tailwindTokens(path string, extend *sitter.Node, src []byte) []Node {
	var nodes []Node
	cursor := extend.Walk()
	defer cursor.Close()
	for _, catPair := range extend.NamedChildren(cursor) {
		if catPair.Kind() != "pair" {
			continue
		}
		catKey := catPair.ChildByFieldName("key")
		catValue := catPair.ChildByFieldName("value")
		if catKey == nil || catValue == nil || catValue.Kind() != "object" {
			continue
		}
		category := unquote(catKey.Utf8Text(src))
		tc := catValue.Walk()
		for _, tokenPair := range catValue.NamedChildren(tc) {
			if tokenPair.Kind() != "pair" {
				continue
			}
			tk := tokenPair.ChildByFieldName("key")
			if tk == nil {
				continue
			}
			name := unquote(tk.Utf8Text(src))
			if name == "" {
				continue
			}
			qualified := category + "." + name
			line := int(tokenPair.StartPosition().Row) + 1
			nodes = append(nodes, node(path, "tailwind_token", name, qualified, line, line, "tailwind-config-v1"))
		}
		tc.Close()
	}
	return nodes
}
