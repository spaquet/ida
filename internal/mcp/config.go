package mcp

import "fmt"

// AgentConfig is a printable MCP configuration snippet for one supported
// agent. Ida never writes these itself; it only prints them, per the
// project's rule against silently editing agent configuration.
type AgentConfig struct {
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Snippet     string `json:"snippet"`
}

// SupportedAgents lists agent keys accepted by ConfigSnippets, in the order
// they should be presented.
var SupportedAgents = []string{"claude-code", "cursor", "codex", "pi", "opencode"}

// ConfigSnippets returns the MCP configuration snippet for each requested
// agent key. An empty agents list returns all supported agents. binary is
// the absolute path to the ida executable and root is the absolute project
// path, both substituted into the snippet.
func ConfigSnippets(binary, root string, agents []string) ([]AgentConfig, error) {
	if len(agents) == 0 {
		agents = SupportedAgents
	}
	configs := make([]AgentConfig, 0, len(agents))
	for _, agent := range agents {
		config, err := configSnippet(agent, binary, root)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, nil
}

func configSnippet(agent, binary, root string) (AgentConfig, error) {
	mcpServersJSON := fmt.Sprintf(`{
  "mcpServers": {
    "ida": {
      "command": %q,
      "args": ["mcp", %q]
    }
  }
}`, binary, root)

	switch agent {
	case "claude-code":
		return AgentConfig{
			Agent:       agent,
			Description: "Claude Code: project-scoped MCP server",
			Path:        "<project>/.mcp.json",
			Snippet:     mcpServersJSON,
		}, nil
	case "cursor":
		return AgentConfig{
			Agent:       agent,
			Description: "Cursor: project-scoped MCP server",
			Path:        "<project>/.cursor/mcp.json",
			Snippet:     mcpServersJSON,
		}, nil
	case "codex":
		return AgentConfig{
			Agent:       agent,
			Description: "Codex CLI: global MCP server entry",
			Path:        "~/.codex/config.toml",
			Snippet: fmt.Sprintf(`[mcp_servers.ida]
command = %q
args = ["mcp", %q]`, binary, root),
		}, nil
	case "pi", "opencode":
		return AgentConfig{
			Agent:       agent,
			Description: "Generic MCP client using the common mcpServers shape; check the client's current docs for its exact config file and location",
			Path:        "client-specific",
			Snippet:     mcpServersJSON,
		}, nil
	default:
		return AgentConfig{}, fmt.Errorf("unsupported agent %q; supported: %v", agent, SupportedAgents)
	}
}
