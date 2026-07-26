package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/mcp"
)

func TestProtocolStdout(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"Gemfile":               "",
		"config/application.rb": "",
		"config/routes.rb":      "",
		"app/models/user.rb":    "class User\nend\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := index.Sync(root); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"ida_search\",\"arguments\":{\"query\":\"User\"}}}\n")
	var stdout, stderr bytes.Buffer
	if err := mcp.Serve(context.Background(), root, input, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, line := range lines {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil || response["jsonrpc"] != "2.0" {
			t.Fatalf("invalid protocol line %q: %v", line, err)
		}
	}
}
