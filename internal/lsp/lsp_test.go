package lsp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectsProjectTypeScriptServer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	servers, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if servers[1].Status != "missing" || len(servers[1].InstallCommand) == 0 {
		t.Fatalf("missing typescript server = %#v", servers[1])
	}
	name := "typescript-language-server"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	path := filepath.Join(root, "node_modules", ".bin", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	servers, err = Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if servers[1].Status != "available" || len(servers[1].InstallCommand) != 0 {
		t.Fatalf("typescript server = %#v", servers[1])
	}
}
