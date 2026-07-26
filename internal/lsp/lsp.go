package lsp

import (
	"bufio"
	"context"
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
		detect(root, "typescript", config.LSP["typescript"], []string{"typescript-language-server", "--stdio"}, typescriptInstall(root)),
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
			fmt.Fprintf(output, "%s: already available (%s)\n", server.Name, strings.Join(server.Command, " "))
			continue
		}
		if len(server.InstallCommand) == 0 {
			fmt.Fprintf(output, "%s: configured command is unavailable\n", server.Name)
			continue
		}
		fmt.Fprintf(output, "%s: not found\nRun %q? [y/N] ", server.Name, strings.Join(server.InstallCommand, " "))
		answer, _ := reader.ReadString('\n')
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
			fmt.Fprintln(output, "skipped")
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
	if len(command) > 0 && executable(root, command[0]) {
		server.Status = "available"
		server.InstallCommand = nil
	}
	return server
}

func executable(root, name string) bool {
	if filepath.IsAbs(name) {
		info, err := os.Stat(name)
		return err == nil && !info.IsDir()
	}
	if strings.ContainsAny(name, `/\`) {
		info, err := os.Stat(filepath.Join(root, name))
		return err == nil && !info.IsDir()
	}
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	local := filepath.Join(root, "node_modules", ".bin", name)
	if runtime.GOOS == "windows" {
		local += ".cmd"
	}
	info, err := os.Stat(local)
	return err == nil && !info.IsDir()
}

func typescriptInstall(root string) []string {
	if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
		return []string{"npm", "install", "--save-dev", "typescript-language-server", "typescript"}
	}
	return nil
}
