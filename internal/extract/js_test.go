package extract

import "testing"

func nodeByKind(nodes []Node, kind string) []Node {
	var out []Node
	for _, n := range nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func TestStimulusControllerExtraction(t *testing.T) {
	content := []byte(`import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["output"]
  static values = { url: String }

  connect() {}

  greet() {}
}
`)
	nodes := jsts("app/javascript/controllers/hello_controller.js", content)

	if got := nodeByKind(nodes, "stimulus_controller"); len(got) != 1 || got[0].Name != "hello" {
		t.Fatalf("stimulus_controller = %#v; want one node named hello", got)
	}
	if got := nodeByKind(nodes, "stimulus_target"); len(got) != 1 || got[0].Name != "output" {
		t.Fatalf("stimulus_target = %#v", got)
	}
	if got := nodeByKind(nodes, "stimulus_value"); len(got) != 1 || got[0].Name != "url" {
		t.Fatalf("stimulus_value = %#v", got)
	}
	methods := nodeByKind(nodes, "method")
	if len(methods) != 2 {
		t.Fatalf("method nodes = %#v; want connect and greet", methods)
	}
}

func TestStimulusNestedControllerIdentifier(t *testing.T) {
	content := []byte(`import { Controller } from "@hotwired/stimulus"
export default class extends Controller {}
`)
	nodes := jsts("app/javascript/controllers/nested/date_picker_controller.js", content)
	got := nodeByKind(nodes, "stimulus_controller")
	if len(got) != 1 || got[0].Name != "nested--date-picker" {
		t.Fatalf("stimulus_controller = %#v; want nested--date-picker", got)
	}
}

func TestReactComponentAndJSXUse(t *testing.T) {
	content := []byte(`import React from "react"
import Greeting from "./Greeting"

export default function App() {
  return <div><Greeting name="x" /></div>
}
`)
	nodes := jsts("app/javascript/components/App.jsx", content)

	if got := nodeByKind(nodes, "js_component"); len(got) != 1 || got[0].Name != "App" {
		t.Fatalf("js_component = %#v; want App", got)
	}
	if got := nodeByKind(nodes, "jsx_use"); len(got) != 1 || got[0].Name != "Greeting" {
		t.Fatalf("jsx_use = %#v; want Greeting", got)
	}
	imports := nodeByKind(nodes, "js_import")
	if len(imports) != 2 {
		t.Fatalf("js_import = %#v; want react and ./Greeting", imports)
	}
}

func TestReactHookHeuristic(t *testing.T) {
	content := []byte(`export function useToggle() {
  return [false, () => {}]
}

export function helper() {
  return 1
}
`)
	nodes := jsts("app/javascript/hooks/useToggle.js", content)
	if got := nodeByKind(nodes, "js_hook"); len(got) != 1 || got[0].Name != "useToggle" {
		t.Fatalf("js_hook = %#v; want useToggle", got)
	}
	if got := nodeByKind(nodes, "js_export"); len(got) != 1 || got[0].Name != "helper" {
		t.Fatalf("js_export = %#v; want helper", got)
	}
}

func TestNonControllerClassIsNotStimulus(t *testing.T) {
	content := []byte(`export default class extends Something {}
`)
	nodes := jsts("app/javascript/controllers/plain_controller.js", content)
	if got := nodeByKind(nodes, "stimulus_controller"); len(got) != 0 {
		t.Fatalf("stimulus_controller = %#v; want none (does not extend Controller)", got)
	}
}
