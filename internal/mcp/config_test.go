package mcp_test

import (
	"strings"
	"testing"

	"github.com/spaquet/ida/internal/mcp"
)

func TestConfigSnippetsDefaultsToAllAgents(t *testing.T) {
	configs, err := mcp.ConfigSnippets("/bin/ida", "/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != len(mcp.SupportedAgents) {
		t.Fatalf("got %d configs, want %d", len(configs), len(mcp.SupportedAgents))
	}
	for i, config := range configs {
		if config.Agent != mcp.SupportedAgents[i] {
			t.Fatalf("configs[%d].Agent = %q, want %q", i, config.Agent, mcp.SupportedAgents[i])
		}
		if !strings.Contains(config.Snippet, "/bin/ida") || !strings.Contains(config.Snippet, "/app") {
			t.Fatalf("snippet for %s missing binary/root: %q", config.Agent, config.Snippet)
		}
	}
}

func TestConfigSnippetsRejectsUnsupportedAgent(t *testing.T) {
	if _, err := mcp.ConfigSnippets("/bin/ida", "/app", []string{"bogus"}); err == nil {
		t.Fatal("expected error for unsupported agent")
	}
}
