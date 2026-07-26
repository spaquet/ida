package extract

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Node struct {
	ID            string
	Kind          string
	Name          string
	QualifiedName string
	StartLine     int
	EndLine       int
	Extractor     string
	Confidence    string
}

var (
	rubyType           = regexp.MustCompile(`^\s*(class|module)\s+([A-Z][A-Za-z0-9_:]*)`)
	viewComponentClass = regexp.MustCompile(`^\s*class\s+[A-Z][A-Za-z0-9_:]*\s*<\s*(?:::)?(?:ViewComponent::Base|ApplicationComponent)\b`)
	rubyMethod         = regexp.MustCompile(`^\s*def\s+(self\.)?([A-Za-z_][A-Za-z0-9_!?=]*)`)
	association        = regexp.MustCompile(`^\s*(has_many|has_one|belongs_to|has_and_belongs_to_many)\s+:([a-zA-Z_][a-zA-Z0-9_]*)`)
	validates          = regexp.MustCompile(`^\s*(validates?)\b(.*)$`)
	scopeDecl          = regexp.MustCompile(`^\s*scope\s+:([a-zA-Z_][a-zA-Z0-9_]*)`)
	broadcasts         = regexp.MustCompile(`^\s*(broadcasts_to|broadcasts_refreshes|broadcasts_refreshes_to|broadcasts|broadcast_append_to|broadcast_prepend_to|broadcast_replace_to|broadcast_remove_to|broadcast_refresh_to|broadcast_refresh_later_to)\b\s*(.*)$`)
	firstSymbol        = regexp.MustCompile(`:([a-zA-Z_][a-zA-Z0-9_]*)`)
	keywordArg         = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*:\s`)

	routeSingle    = regexp.MustCompile(`^\s*(get|post|put|patch|delete)\s+["']([^"']+)["'](?:\s*,?\s*(?:to:|=>)\s*["']([^"'#]+)#([^"']+)["'])`)
	routeRoot      = regexp.MustCompile(`^\s*root\s+(?:to:\s*)?["']([^"'#]+)#([^"']+)["']`)
	routeNamespace = regexp.MustCompile(`^\s*namespace\s+:([a-zA-Z_][a-zA-Z0-9_]*)`)
	routeResource  = regexp.MustCompile(`^\s*(resources|resource)\s+:([a-zA-Z_][a-zA-Z0-9_]*)(.*)$`)
	opensBlock     = regexp.MustCompile(`\bdo(\s*\|[^|]*\|)?\s*$`)
	onlyOption     = regexp.MustCompile(`only:\s*(\[[^\]]*\]|:[a-zA-Z_]+)`)
	exceptOption   = regexp.MustCompile(`except:\s*(\[[^\]]*\]|:[a-zA-Z_]+)`)
	controllerOpt  = regexp.MustCompile(`controller:\s*["']?([a-zA-Z0-9_/]+)["']?`)

	heading  = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	adocHead = regexp.MustCompile(`^(={1,6})\s+(.+?)\s*$`)
)

type resourceAction struct {
	name       string
	method     string
	pathSuffix string
}

var pluralActions = []resourceAction{
	{"index", "GET", ""},
	{"create", "POST", ""},
	{"new", "GET", "/new"},
	{"show", "GET", "/:id"},
	{"edit", "GET", "/:id/edit"},
	{"update", "PATCH", "/:id"},
	{"destroy", "DELETE", "/:id"},
}

var singularActions = []resourceAction{
	{"new", "GET", "/new"},
	{"create", "POST", ""},
	{"show", "GET", ""},
	{"edit", "GET", "/edit"},
	{"update", "PATCH", ""},
	{"destroy", "DELETE", ""},
}

func File(path string, content []byte) []Node {
	nodes := []Node{node(path, "file", path, path, 1, lineCount(content), "file-v1")}
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))
	if ext == ".rb" {
		nodes = append(nodes, ruby(path, content)...)
	}
	if path == "config/routes.rb" {
		nodes = append(nodes, routes(path, content)...)
	}
	if ext == ".md" || ext == ".markdown" || ext == ".adoc" || ext == ".asciidoc" {
		nodes = append(nodes, headings(path, content, ext)...)
	}
	isTailwindConfig := base == "tailwind.config.js" || base == "tailwind.config.ts"
	if ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" {
		if isTailwindConfig {
			nodes = append(nodes, tailwindConfig(path, content)...)
		} else {
			nodes = append(nodes, jsts(path, content)...)
		}
	}
	if ext == ".css" || ext == ".scss" {
		nodes = append(nodes, css(path, content)...)
	}
	if isTemplatePath(path, ext) {
		nodes = append(nodes, template(path, content)...)
	}
	if dir, name, ok := partialName(path); ok {
		qualified := name
		if dir != "" {
			qualified = dir + "/" + name
		}
		nodes = append(nodes, node(path, "partial", name, qualified, 1, lineCount(content), "partials-v1"))
	}
	return nodes
}

// partialName reports the conventional lookup name of a Rails partial file
// such as app/views/articles/_form.html.erb: dir "articles", name "form".
func partialName(path string) (dir, name string, ok bool) {
	idx := strings.Index(path, "app/views/")
	if idx == -1 {
		return "", "", false
	}
	rel := path[idx+len("app/views/"):]
	d, base := filepath.Split(rel)
	if !strings.HasPrefix(base, "_") {
		return "", "", false
	}
	base = strings.TrimPrefix(base, "_")
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return "", "", false
	}
	return strings.TrimSuffix(d, "/"), base, true
}

// isTemplatePath reports whether a file should be scanned for Stimulus
// attribute uses, Turbo helper calls, react_component mounts, and static
// class/className attributes: ERB/HTML templates and JSX/TSX components.
func isTemplatePath(path, ext string) bool {
	if strings.HasSuffix(path, ".erb") || ext == ".html" || ext == ".htm" {
		return true
	}
	return ext == ".jsx" || ext == ".tsx"
}

// routeFrame tracks the path and controller-module prefix contributed by
// enclosing namespace/resources blocks in config/routes.rb.
type routeFrame struct {
	path   []string
	module []string
}

func routes(path string, content []byte) []Node {
	var nodes []Node
	var stack []routeFrame
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		var top routeFrame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}

		if trimmed == "end" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if match := routeNamespace.FindStringSubmatch(text); match != nil {
			name := match[1]
			stack = append(stack, routeFrame{path: appendCopy(top.path, name), module: appendCopy(top.module, name)})
			continue
		}
		if match := routeResource.FindStringSubmatch(text); match != nil {
			kind, name, rest := match[1], match[2], match[3]
			nodes = append(nodes, resourceRoutes(path, line, kind, name, rest, top)...)
			if opensBlock.MatchString(trimmed) {
				stack = append(stack, routeFrame{path: appendCopy(top.path, name), module: top.module})
			}
			continue
		}
		if match := routeRoot.FindStringSubmatch(text); match != nil {
			target := joinController(top.module, match[1]) + "#" + match[2]
			nodes = append(nodes, node(path, "route", "ROOT /", target, line, line, "rails-routes-v2"))
			continue
		}
		if match := routeSingle.FindStringSubmatch(text); match != nil {
			name := strings.ToUpper(match[1]) + " " + joinPath(top.path, match[2])
			target := joinController(top.module, match[3]) + "#" + match[4]
			nodes = append(nodes, node(path, "route", name, target, line, line, "rails-routes-v2"))
			continue
		}
		if opensBlock.MatchString(trimmed) {
			stack = append(stack, routeFrame{path: top.path, module: top.module})
		}
	}
	return nodes
}

func resourceRoutes(path string, line int, kind, name, rest string, ctx routeFrame) []Node {
	only, except, controllerOverride := parseResourceOptions(rest)
	actions := pluralActions
	controllerName := name
	if kind == "resource" {
		actions = singularActions
		controllerName = pluralize(name)
	}
	if controllerOverride != "" {
		controllerName = controllerOverride
	}
	actions = filterActions(actions, only, except)
	controllerPath := joinController(ctx.module, controllerName)
	urlBase := joinPath(ctx.path, name)
	var nodes []Node
	for _, action := range actions {
		routeName := action.method + " " + urlBase + action.pathSuffix
		nodes = append(nodes, node(path, "route", routeName, controllerPath+"#"+action.name, line, line, "rails-routes-v2"))
	}
	return nodes
}

func parseResourceOptions(rest string) (only, except []string, controller string) {
	if m := onlyOption.FindStringSubmatch(rest); m != nil {
		only = symbols(m[1])
	}
	if m := exceptOption.FindStringSubmatch(rest); m != nil {
		except = symbols(m[1])
	}
	if m := controllerOpt.FindStringSubmatch(rest); m != nil {
		controller = m[1]
	}
	return
}

func symbols(text string) []string {
	var result []string
	for _, m := range firstSymbol.FindAllStringSubmatch(text, -1) {
		result = append(result, m[1])
	}
	return result
}

// validationFields returns the positional symbol arguments of a validates
// call, ignoring any :symbol appearing inside keyword-option values (e.g.
// `scope: :organization_id`, `if: :confirmed?`) and de-duplicating repeats.
func validationFields(text string) []string {
	if loc := keywordArg.FindStringIndex(text); loc != nil {
		text = text[:loc[0]]
	}
	seen := make(map[string]bool)
	var result []string
	for _, field := range symbols(text) {
		if seen[field] {
			continue
		}
		seen[field] = true
		result = append(result, field)
	}
	return result
}

func filterActions(actions []resourceAction, only, except []string) []resourceAction {
	if len(only) == 0 && len(except) == 0 {
		return actions
	}
	var result []resourceAction
	for _, a := range actions {
		if len(only) > 0 && !slices.Contains(only, a.name) {
			continue
		}
		if len(except) > 0 && slices.Contains(except, a.name) {
			continue
		}
		result = append(result, a)
	}
	return result
}

func appendCopy(base []string, extra ...string) []string {
	return append(append([]string{}, base...), extra...)
}

func joinPath(prefix []string, raw string) string {
	raw = strings.Trim(raw, "/")
	full := appendCopy(prefix)
	if raw != "" {
		full = append(full, raw)
	}
	if len(full) == 0 {
		return "/"
	}
	return "/" + strings.Join(full, "/")
}

func joinController(prefix []string, name string) string {
	return strings.Join(appendCopy(prefix, name), "/")
}

type classFrame struct {
	indent  int
	name    string
	isClass bool
}

// ruby walks a Ruby file line by line, tracking an indentation-based stack of
// enclosing class/module declarations so associations, validations, and
// scopes can be attributed to the class that declares them.
func ruby(path string, content []byte) []Node {
	var nodes []Node
	var stack []classFrame
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		indent := len(text) - len(strings.TrimLeft(text, " \t"))

		nodes = append(nodes, renderUses(path, text, line, false)...)

		if trimmed == "end" {
			if len(stack) > 0 && indent <= stack[len(stack)-1].indent {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if match := rubyType.FindStringSubmatch(text); match != nil {
			name := match[2]
			nodes = append(nodes, node(path, match[1], lastPart(name), name, line, line, "ruby-declarations-v1"))
			stack = append(stack, classFrame{indent: indent, name: lastPart(name), isClass: match[1] == "class"})
			if vc := viewComponentClass.FindStringSubmatch(text); vc != nil {
				nodes = append(nodes, node(path, "view_component", lastPart(name), name, line, line, "view-components-v1"))
			}
			continue
		}
		if match := rubyMethod.FindStringSubmatch(text); match != nil {
			name := match[2]
			qualified := name
			if match[1] != "" {
				qualified = "self." + name
			}
			nodes = append(nodes, node(path, "method", name, qualified, line, line, "ruby-declarations-v1"))
			continue
		}
		owner := classOwner(stack)
		if owner == "" {
			continue
		}
		if match := association.FindStringSubmatch(text); match != nil {
			macro, name := match[1], match[2]
			nodes = append(nodes, node(path, "association", name, owner+"#"+macro+":"+name, line, line, "ruby-associations-v1"))
			continue
		}
		if match := scopeDecl.FindStringSubmatch(text); match != nil {
			name := match[1]
			nodes = append(nodes, node(path, "scope", name, owner+"#scope:"+name, line, line, "ruby-associations-v1"))
			continue
		}
		if match := broadcasts.FindStringSubmatch(text); match != nil {
			macro := match[1]
			nodes = append(nodes, node(path, "turbo_broadcast", macro, owner+"#"+macro, line, line, "turbo-broadcasts-v1"))
			continue
		}
		if match := validates.FindStringSubmatch(text); match != nil {
			fields := validationFields(match[2])
			if len(fields) == 0 {
				continue
			}
			for _, field := range fields {
				nodes = append(nodes, node(path, "validation", field, owner+"#validates:"+field, line, line, "ruby-associations-v1"))
			}
			continue
		}
	}
	return nodes
}

func classOwner(stack []classFrame) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].isClass {
			return stack[i].name
		}
	}
	return ""
}

func headings(path string, content []byte, ext string) []Node {
	var nodes []Node
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for line := 1; scanner.Scan(); line++ {
		match := heading.FindStringSubmatch(scanner.Text())
		if ext == ".adoc" || ext == ".asciidoc" {
			match = adocHead.FindStringSubmatch(scanner.Text())
		}
		if match != nil {
			name := strings.TrimSpace(match[2])
			nodes = append(nodes, node(path, "document_section", name, path+"#"+name, line, line, "document-headings-v1"))
		}
	}
	return nodes
}

func node(path, kind, name, qualified string, start, end int, extractor string) Node {
	sum := sha256.Sum256([]byte(path + "\x00" + kind + "\x00" + qualified + "\x00" + strconv.Itoa(start)))
	return Node{
		ID: hex.EncodeToString(sum[:]), Kind: kind, Name: name, QualifiedName: qualified,
		StartLine: start, EndLine: end, Extractor: extractor, Confidence: "exact",
	}
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 1
	}
	return strings.Count(string(content), "\n") + 1
}

func lastPart(name string) string {
	parts := strings.Split(name, "::")
	return parts[len(parts)-1]
}

// pluralize applies a best-effort English pluralization matching common
// Rails resource naming, e.g. resource :profile -> ProfilesController.
func pluralize(name string) string {
	switch {
	case strings.HasSuffix(name, "y") && len(name) > 1 && !isVowel(name[len(name)-2]):
		return strings.TrimSuffix(name, "y") + "ies"
	case strings.HasSuffix(name, "s"), strings.HasSuffix(name, "x"), strings.HasSuffix(name, "z"),
		strings.HasSuffix(name, "ch"), strings.HasSuffix(name, "sh"):
		return name + "es"
	default:
		return name + "s"
	}
}

func isVowel(b byte) bool {
	return strings.ContainsRune("aeiouAEIOU", rune(b))
}
