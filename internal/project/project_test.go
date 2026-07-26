package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryAndScope(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"Gemfile", "config/application.rb", "config/routes.rb", "app/models/user.rb", "app/assets/builds/app.js", ".env", "docs/guide.md", "test/models/user_test.rb", "spec/models/user_spec.rb"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "app", "models")
	discovered, err := Discover(nested)
	resolvedRoot, resolveErr := filepath.EvalSymlinks(root)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || discovered != resolvedRoot {
		t.Fatalf("Discover() = %q, %v; want %q", discovered, err, resolvedRoot)
	}
	scope, err := LoadScope(root)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"app/models/user.rb":       true,
		"docs/guide.md":            true,
		"app/assets/builds/app.js": false,
		".env":                     false,
		"test/models/user_test.rb": false,
		"spec/models/user_spec.rb": false,
	} {
		if got := scope.Decide(path).Included; got != want {
			t.Errorf("Decide(%q).Included = %v; want %v", path, got, want)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "ida.json"), []byte(`{"include":["test/**","spec/**"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	scope, err = LoadScope(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"test/models/user_test.rb", "spec/models/user_spec.rb"} {
		if decision := scope.Decide(path); !decision.Included || decision.Reason != "ida.json include" {
			t.Errorf("Decide(%q) = %#v; want ida.json include", path, decision)
		}
	}
}

func TestAddDocumentSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handbook")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := AddDocumentSource(root, path)
	if err != nil || source != "handbook/**" {
		t.Fatalf("AddDocumentSource() = %q, %v", source, err)
	}
	config, err := LoadConfig(root)
	if err != nil || len(config.Docs) != 1 || config.Docs[0] != source {
		t.Fatalf("LoadConfig() = %#v, %v", config, err)
	}
	if _, err := AddDocumentSource(root, filepath.Dir(root)); err == nil {
		t.Fatal("AddDocumentSource accepted a path outside the project")
	}
}
