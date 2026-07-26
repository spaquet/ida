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
