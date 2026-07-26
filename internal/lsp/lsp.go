package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spaquet/ida/internal/project"
)

type Server struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Command        []string `json:"command,omitempty"`
	InstallCommand []string `json:"install_command,omitempty"`
}

func Detect(root string) ([]Server, error) {
	config, err := project.LoadConfig(root)
	if err != nil {
		return nil, err
	}
	servers := []Server{
		detect(root, "ruby", config.LSP["ruby"], []string{"ruby-lsp"}, []string{"gem", "install", "ruby-lsp"}),
		detect(root, "typescript", config.LSP["typescript"], typescriptFallback(root), typescriptInstall(root)),
	}
	for name, command := range config.LSP {
		if name != "ruby" && name != "typescript" {
			servers = append(servers, detect(root, name, command, nil, nil))
		}
	}
	return servers, nil
}

func InstallMissing(ctx context.Context, root string, input io.Reader, output io.Writer) ([]Server, error) {
	servers, err := Detect(root)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(input)
	for _, server := range servers {
		if server.Status == "available" {
			_, _ = fmt.Fprintf(output, "%s: already available (%s)\n", server.Name, strings.Join(server.Command, " "))
			continue
		}
		if len(server.InstallCommand) == 0 {
			_, _ = fmt.Fprintf(output, "%s: configured command is unavailable\n", server.Name)
			continue
		}
		_, _ = fmt.Fprintf(output, "%s: not found\nRun %q? [y/N] ", server.Name, strings.Join(server.InstallCommand, " "))
		answer, _ := reader.ReadString('\n')
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
			_, _ = fmt.Fprintln(output, "skipped")
			continue
		}
		command := exec.CommandContext(ctx, server.InstallCommand[0], server.InstallCommand[1:]...)
		command.Dir = root
		command.Stdin = reader
		command.Stdout = output
		command.Stderr = output
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("install %s: %w", server.Name, err)
		}
	}
	return Detect(root)
}

func detect(root, name string, configured, fallback, install []string) Server {
	command := configured
	if len(command) == 0 {
		command = fallback
	}
	server := Server{Name: name, Status: "missing", Command: command, InstallCommand: install}
	if len(command) > 0 {
		if resolved, ok := executable(root, command[0]); ok {
			server.Status = "available"
			server.InstallCommand = nil
			server.Command = append([]string{resolved}, command[1:]...)
		}
	}
	return server
}

// executable resolves name to a runnable path, checking (in order) an
// absolute path, a root-relative path, $PATH, and the project's
// node_modules/.bin — since a project-local LSP server is never on $PATH.
func executable(root, name string) (string, bool) {
	if filepath.IsAbs(name) {
		info, err := os.Stat(name)
		return name, err == nil && !info.IsDir()
	}
	if strings.ContainsAny(name, `/\`) {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		return path, err == nil && !info.IsDir()
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	local := filepath.Join(root, "node_modules", ".bin", name)
	if runtime.GOOS == "windows" {
		local += ".cmd"
	}
	info, err := os.Stat(local)
	return local, err == nil && !info.IsDir()
}

// typescriptFallback picks the default typescript LSP command when the
// project hasn't configured one explicitly. TypeScript 7 ships tsgo
// (@typescript/native-preview), a Go-based compiler/server that speaks LSP
// directly via `tsgo --lsp`; the older typescript-language-server wrapper
// can't load a TS7 installation (it depends on internal APIs TS7 dropped),
// so a TS7 project needs tsgo instead. TypeScript <7 keeps using
// typescript-language-server, which tsgo doesn't yet replace feature-for-
// feature. When the version can't be determined, default to
// typescript-language-server (today's established, working default).
func typescriptFallback(root string) []string {
	if major, ok := typescriptMajorVersion(root); ok && major >= 7 {
		return []string{"tsgo", "--lsp", "--stdio"}
	}
	return []string{"typescript-language-server", "--stdio"}
}

func typescriptInstall(root string) []string {
	if _, err := os.Stat(filepath.Join(root, "package.json")); err != nil {
		return nil
	}
	if major, ok := typescriptMajorVersion(root); ok && major >= 7 {
		return []string{"npm", "install", "--save-dev", "@typescript/native-preview", "typescript"}
	}
	return []string{"npm", "install", "--save-dev", "typescript-language-server", "typescript"}
}

// typescriptMajorVersion resolves the project's TypeScript major version:
// the actually-installed package under node_modules/typescript (most
// accurate — reflects what's really there) if present, else the version
// range declared in package.json's dependencies/devDependencies.
func typescriptMajorVersion(root string) (int, bool) {
	if major, ok := packageJSONVersion(filepath.Join(root, "node_modules", "typescript", "package.json")); ok {
		return major, true
	}
	return declaredTypescriptMajor(filepath.Join(root, "package.json"))
}

func packageJSONVersion(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return 0, false
	}
	return parseMajorVersion(pkg.Version)
}

func declaredTypescriptMajor(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return 0, false
	}
	if v, ok := pkg.DevDependencies["typescript"]; ok {
		if major, ok := parseMajorVersion(v); ok {
			return major, true
		}
	}
	if v, ok := pkg.Dependencies["typescript"]; ok {
		return parseMajorVersion(v)
	}
	return 0, false
}

// parseMajorVersion extracts the leading major version number from a
// semver-ish string like "^7.0.0", "~7.0.0-beta", "7", or "v7.0.0".
func parseMajorVersion(v string) (int, bool) {
	start := -1
	for i, r := range v {
		if r >= '0' && r <= '9' {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, false
	}
	end := start
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	var major int
	if _, err := fmt.Sscanf(v[start:end], "%d", &major); err != nil {
		return 0, false
	}
	return major, true
}
