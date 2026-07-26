package extract

import (
	"slices"
	"testing"
)

func TestRubyDeclarations(t *testing.T) {
	content := []byte("module Admin\n  class User\n    def active?\n    end\n  end\nend\n")
	nodes := File("app/models/admin/user.rb", content)
	got := make(map[string]bool)
	for _, node := range nodes {
		got[node.QualifiedName] = true
	}
	for _, want := range []string{"Admin", "User", "active?"} {
		if !got[want] {
			t.Errorf("missing %q in %#v", want, nodes)
		}
	}
}

func TestRouteDeclaration(t *testing.T) {
	nodes := File("config/routes.rb", []byte(`get "/articles", to: "articles#index"`))
	if len(nodes) != 2 || nodes[1].Kind != "route" || nodes[1].Name != "GET /articles" || nodes[1].QualifiedName != "articles#index" {
		t.Fatalf("File() = %#v", nodes)
	}
}

func TestRouteResourcesAndNamespace(t *testing.T) {
	content := []byte(`Rails.application.routes.draw do
  namespace :admin do
    resources :articles, only: [:index, :show]
  end
  resource :profile
  root to: "pages#home"
end
`)
	nodes := File("config/routes.rb", content)
	got := make(map[string]string)
	for _, n := range nodes {
		if n.Kind == "route" {
			got[n.Name] = n.QualifiedName
		}
	}
	want := map[string]string{
		"GET /admin/articles":     "admin/articles#index",
		"GET /admin/articles/:id": "admin/articles#show",
		"GET /profile/new":        "profiles#new",
		"POST /profile":           "profiles#create",
		"GET /profile":            "profiles#show",
		"GET /profile/edit":       "profiles#edit",
		"PATCH /profile":          "profiles#update",
		"DELETE /profile":         "profiles#destroy",
		"ROOT /":                  "pages#home",
	}
	for name, qualified := range want {
		if got[name] != qualified {
			t.Errorf("route %q = %q; want %q (all: %#v)", name, got[name], qualified, got)
		}
	}
	if _, ok := got["POST /admin/articles"]; ok {
		t.Errorf("only: filter did not exclude create action: %#v", got)
	}
}

func TestTurboBroadcast(t *testing.T) {
	content := []byte(`class Article < ApplicationRecord
  broadcasts_to :article
end
`)
	nodes := File("app/models/article.rb", content)
	got := nodeByKind(nodes, "turbo_broadcast")
	if len(got) != 1 || got[0].QualifiedName != "Article#broadcasts_to" {
		t.Fatalf("turbo_broadcast = %#v; want Article#broadcasts_to", got)
	}
}

func TestPartialFileTagged(t *testing.T) {
	nodes := File("app/views/articles/_form.html.erb", []byte("<div></div>\n"))
	partials := nodeByKind(nodes, "partial")
	if len(partials) != 1 || partials[0].Name != "form" || partials[0].QualifiedName != "articles/form" {
		t.Fatalf("partial = %#v; want articles/form", partials)
	}
}

func TestRubyRenderCallsExplicitOnly(t *testing.T) {
	content := []byte(`class ArticlesController < ApplicationController
  def show
    render partial: "form"
    render "not_a_partial_in_a_controller"
    render SubmitButtonComponent.new
  end
end
`)
	nodes := File("app/controllers/articles_controller.rb", content)
	partials := nodeByKind(nodes, "partial_use")
	if len(partials) != 1 || partials[0].Name != "form" {
		t.Fatalf("partial_use = %#v; want only the explicit partial: form call", partials)
	}
	components := nodeByKind(nodes, "view_component_use")
	if len(components) != 1 || components[0].Name != "SubmitButtonComponent" {
		t.Fatalf("view_component_use = %#v; want SubmitButtonComponent", components)
	}
}

func TestViewComponentClassTagged(t *testing.T) {
	content := []byte(`class SubmitButtonComponent < ViewComponent::Base
  def initialize(label:)
    @label = label
  end
end
`)
	nodes := File("app/components/submit_button_component.rb", content)
	got := nodeByKind(nodes, "view_component")
	if len(got) != 1 || got[0].QualifiedName != "SubmitButtonComponent" {
		t.Fatalf("view_component = %#v; want SubmitButtonComponent", got)
	}
}

func TestMethodCallUses(t *testing.T) {
	content := []byte(`class ArticlesController < ApplicationController
  def create
    NotifyUserService.call(current_user)
    ExportService.new(current_user).perform
    ExportService.new
    other_helper.foo
  end
end
`)
	nodes := File("app/controllers/articles_controller.rb", content)
	calls := nodeByKind(nodes, "method_call_use")
	got := make(map[string]bool)
	for _, c := range calls {
		got[c.QualifiedName] = true
	}
	for _, want := range []string{"NotifyUserService#call", "ExportService#perform"} {
		if !got[want] {
			t.Errorf("missing %q in %#v", want, calls)
		}
	}
	if got["ExportService#new"] {
		t.Errorf("bare .new should not itself become a method_call_use: %#v", calls)
	}
	if len(calls) != 2 {
		t.Fatalf("method_call_use = %#v; want exactly 2 (call, perform)", calls)
	}
}

func TestAssociationsValidationsAndScopes(t *testing.T) {
	content := []byte(`class Article < ApplicationRecord
  belongs_to :author
  has_many :comments
  validates :title, presence: true
  scope :published, -> { where(published: true) }
end
`)
	nodes := File("app/models/article.rb", content)
	var kinds []string
	for _, n := range nodes {
		kinds = append(kinds, n.Kind+":"+n.QualifiedName)
	}
	for _, want := range []string{
		"association:Article#belongs_to:author",
		"association:Article#has_many:comments",
		"validation:Article#validates:title",
		"scope:Article#scope:published",
	} {
		if !slices.Contains(kinds, want) {
			t.Errorf("missing %q in %#v", want, kinds)
		}
	}
}
