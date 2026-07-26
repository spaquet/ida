package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryAndScope(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"Gemfile", "config/application.rb", "config/routes.rb", "app/models/user.rb", "app/assets/builds/app.js", ".env", "docs/guide.md"} {
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
	} {
		if got := scope.Decide(path).Included; got != want {
			t.Errorf("Decide(%q).Included = %v; want %v", path, got, want)
		}
	}
}
