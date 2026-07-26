package extract

import (
	"bufio"
	"regexp"
	"strings"
)

var (
	dataControllerAttr = regexp.MustCompile(`data-controller=["']([^"']+)["']`)
	dataActionAttr     = regexp.MustCompile(`data-action=["']([^"']+)["']`)
	actionToken        = regexp.MustCompile(`^(?:[a-zA-Z0-9:.]+->)?([a-zA-Z0-9_](?:[a-zA-Z0-9_-]*[a-zA-Z0-9_])?(?:--[a-zA-Z0-9_-]+)*)#([a-zA-Z_][a-zA-Z0-9_]*)`)
	turboFrameHelper   = regexp.MustCompile(`turbo_frame_tag\s*\(?\s*["']([^"']+)["']`)
	turboFrameElement  = regexp.MustCompile(`<turbo-frame\b[^>]*\bid=["']([^"']+)["']`)
	turboStreamFrom    = regexp.MustCompile(`turbo_stream_from\s*\(?\s*["']([^"']+)["']`)
	reactComponentCall = regexp.MustCompile(`react_component\s*\(?\s*["']([^"']+)["']`)
	classAttr          = regexp.MustCompile(`\b(?:class|className)\s*=\s*["']([^"'{}<%]*)["']`)

	renderPartialExplicit = regexp.MustCompile(`\brender\b\s*\(?\s*partial:\s*["']([a-zA-Z0-9_/.-]+)["']`)
	renderPartialBare     = regexp.MustCompile(`\brender\b\s*\(?\s*["']([a-zA-Z0-9_/.-]+)["']`)
	renderComponentUse    = regexp.MustCompile(`\brender\b\s*\(?\s*((?:::)?(?:[A-Z][A-Za-z0-9_]*::)*[A-Z][A-Za-z0-9_]*Component)(?:\.with_collection)?\.new\b`)
)

// template extracts Stimulus attribute uses, Turbo frame/stream helper
// calls, react_component mount calls, and static class/className attribute
// values from ERB, HTML, and JSX/TSX source by regex/line-scan, matching
// extract.go's existing route/association scanning style rather than
// attempting a full ERB/HTML grammar.
func template(path string, content []byte) []Node {
	var nodes []Node
	var classTokens []string
	classLine := 0

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()

		for _, m := range dataControllerAttr.FindAllStringSubmatch(text, -1) {
			for _, id := range strings.Fields(m[1]) {
				nodes = append(nodes, node(path, "stimulus_controller_use", id, id, line, line, "template-attrs-v1"))
			}
		}
		for _, m := range dataActionAttr.FindAllStringSubmatch(text, -1) {
			for _, tok := range strings.Fields(m[1]) {
				am := actionToken.FindStringSubmatch(tok)
				if am == nil {
					continue
				}
				qualified := am[1] + "#" + am[2]
				nodes = append(nodes, node(path, "stimulus_action_use", qualified, qualified, line, line, "template-attrs-v1"))
			}
		}
		for _, m := range turboFrameHelper.FindAllStringSubmatch(text, -1) {
			nodes = append(nodes, node(path, "turbo_frame", m[1], m[1], line, line, "turbo-templates-v1"))
		}
		for _, m := range turboFrameElement.FindAllStringSubmatch(text, -1) {
			nodes = append(nodes, node(path, "turbo_frame", m[1], m[1], line, line, "turbo-templates-v1"))
		}
		for _, m := range turboStreamFrom.FindAllStringSubmatch(text, -1) {
			nodes = append(nodes, node(path, "turbo_stream_from", m[1], m[1], line, line, "turbo-templates-v1"))
		}
		for _, m := range reactComponentCall.FindAllStringSubmatch(text, -1) {
			nodes = append(nodes, node(path, "react_mount", m[1], m[1], line, line, "react-mounts-v1"))
		}
		nodes = append(nodes, renderUses(path, text, line, true)...)
		if !strings.HasPrefix(strings.TrimSpace(text), "<%#") {
			nodes = append(nodes, envVarUses(path, text, line)...)
		}
		for _, m := range classAttr.FindAllStringSubmatch(text, -1) {
			fields := strings.Fields(m[1])
			if len(fields) == 0 {
				continue
			}
			if classLine == 0 {
				classLine = line
			}
			classTokens = append(classTokens, fields...)
		}
	}

	if len(classTokens) > 0 {
		blob := strings.Join(dedupe(classTokens), " ")
		nodes = append(nodes, node(path, "class_attr_use", blob, blob, classLine, classLine, "class-attrs-v1"))
	}
	return nodes
}

// renderUses scans one line for `render` calls naming a partial or a
// ViewComponent. includeBare enables the bare-string partial shorthand
// (`render "form"`), which only means "render a partial" inside a view
// template; in a controller/helper the same shorthand renders a full
// template, so callers pass includeBare=false there and rely on the
// unambiguous `partial:` keyword form instead.
func renderUses(path, text string, line int, includeBare bool) []Node {
	var nodes []Node
	for _, m := range renderPartialExplicit.FindAllStringSubmatch(text, -1) {
		nodes = append(nodes, node(path, "partial_use", m[1], m[1], line, line, "partials-v1"))
	}
	if includeBare {
		for _, m := range renderPartialBare.FindAllStringSubmatch(text, -1) {
			nodes = append(nodes, node(path, "partial_use", m[1], m[1], line, line, "partials-v1"))
		}
	}
	for _, m := range renderComponentUse.FindAllStringSubmatch(text, -1) {
		nodes = append(nodes, node(path, "view_component_use", m[1], m[1], line, line, "view-components-v1"))
	}
	return nodes
}

func dedupe(items []string) []string {
	seen := make(map[string]bool, len(items))
	var out []string
	for _, it := range items {
		if seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	return out
}
