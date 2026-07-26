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

func TestDetectsTsgoForTypeScript7DeclaredVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))
	pkg := `{"devDependencies":{"typescript":"^7.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	servers, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if servers[1].Status != "missing" || len(servers[1].Command) == 0 || servers[1].Command[0] != "tsgo" {
		t.Fatalf("typescript server = %#v, want fallback command starting with tsgo", servers[1])
	}
	if len(servers[1].InstallCommand) == 0 || servers[1].InstallCommand[len(servers[1].InstallCommand)-2] != "@typescript/native-preview" {
		t.Fatalf("install command = %#v, want @typescript/native-preview", servers[1].InstallCommand)
	}
}

func TestDetectsTsgoForInstalledTypeScript7(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	tsPkgDir := filepath.Join(root, "node_modules", "typescript")
	if err := os.MkdirAll(tsPkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tsPkgDir, "package.json"), []byte(`{"version":"7.1.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	name := "tsgo"
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

	servers, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if servers[1].Status != "available" || len(servers[1].Command) < 3 || servers[1].Command[1] != "--lsp" || servers[1].Command[2] != "--stdio" {
		t.Fatalf("typescript server = %#v, want available tsgo --lsp --stdio", servers[1])
	}
}
