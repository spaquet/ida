package extract

import "testing"

func TestTemplateStimulusAndTurboAttrs(t *testing.T) {
	content := []byte(`<h1 class="text-primary">Articles</h1>
<div data-controller="hello nested--date-picker">
  <button data-action="click->hello#greet">Greet</button>
</div>
<%= turbo_frame_tag "article_list" do %>
<% end %>
<turbo-frame id="comments">
</turbo-frame>
<%= turbo_stream_from "articles" %>
<%= react_component("Greeting") %>
`)
	nodes := template("app/views/articles/index.html.erb", content)

	controllerUses := nodeByKind(nodes, "stimulus_controller_use")
	if len(controllerUses) != 2 {
		t.Fatalf("stimulus_controller_use = %#v; want hello and nested--date-picker", controllerUses)
	}
	actionUses := nodeByKind(nodes, "stimulus_action_use")
	if len(actionUses) != 1 || actionUses[0].QualifiedName != "hello#greet" {
		t.Fatalf("stimulus_action_use = %#v; want hello#greet", actionUses)
	}
	frames := nodeByKind(nodes, "turbo_frame")
	if len(frames) != 2 {
		t.Fatalf("turbo_frame = %#v; want article_list and comments", frames)
	}
	streams := nodeByKind(nodes, "turbo_stream_from")
	if len(streams) != 1 || streams[0].Name != "articles" {
		t.Fatalf("turbo_stream_from = %#v; want articles", streams)
	}
	mounts := nodeByKind(nodes, "react_mount")
	if len(mounts) != 1 || mounts[0].Name != "Greeting" {
		t.Fatalf("react_mount = %#v; want Greeting", mounts)
	}
	classUse := nodeByKind(nodes, "class_attr_use")
	if len(classUse) != 1 || classUse[0].Name != "text-primary" {
		t.Fatalf("class_attr_use = %#v; want a single node containing text-primary", classUse)
	}
}

func TestTemplateRenderPartialAndComponent(t *testing.T) {
	content := []byte(`<%= render "form" %>
<%= render partial: "shared/flash" %>
<%= render(SubmitButtonComponent.new(label: "Go")) %>
`)
	nodes := template("app/views/articles/index.html.erb", content)

	partials := nodeByKind(nodes, "partial_use")
	if len(partials) != 2 || partials[0].Name != "form" || partials[1].Name != "shared/flash" {
		t.Fatalf("partial_use = %#v; want form and shared/flash", partials)
	}
	components := nodeByKind(nodes, "view_component_use")
	if len(components) != 1 || components[0].Name != "SubmitButtonComponent" {
		t.Fatalf("view_component_use = %#v; want SubmitButtonComponent", components)
	}
}

func TestTemplateDynamicClassIsSkipped(t *testing.T) {
	content := []byte(`<div class="btn <%= 'active' if x %>"></div>
<div className="static-only"></div>
`)
	nodes := template("app/views/things/show.html.erb", content)
	classUse := nodeByKind(nodes, "class_attr_use")
	if len(classUse) != 1 || classUse[0].Name != "static-only" {
		t.Fatalf("class_attr_use = %#v; want only the static className value", classUse)
	}
}
