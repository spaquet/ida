package resolve

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spaquet/ida/internal/lsp"
	"github.com/spaquet/ida/internal/store"
)

// --- minimal local JSON-RPC framer, kept independent of internal/lsp's own
// (unexported) framing so this test doesn't reach into that package's
// internals; it only needs to speak the wire protocol as a fake server.

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func readFramed(r *bufio.Reader) (*rpcMsg, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing content-length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg rpcMsg
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func writeFramed(w io.Writer, msg rpcMsg) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// runFakeServer answers initialize/shutdown generically and returns
// definitionResults[i] (JSON) for the i'th textDocument/definition request
// it receives, in order.
func runFakeServer(in io.Reader, out io.Writer, definitionResults []string) {
	reader := bufio.NewReader(in)
	idx := 0
	for {
		msg, err := readFramed(reader)
		if err != nil {
			return
		}
		switch msg.Method {
		case "initialize":
			_ = writeFramed(out, rpcMsg{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"capabilities":{}}`)})
		case "textDocument/definition":
			result := "null"
			if idx < len(definitionResults) {
				result = definitionResults[idx]
			}
			idx++
			_ = writeFramed(out, rpcMsg{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(result)})
		case "shutdown":
			_ = writeFramed(out, rpcMsg{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`null`)})
		case "exit":
			return
		default:
			if msg.ID != nil {
				_ = writeFramed(out, rpcMsg{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`null`)})
			}
		}
	}
}

func withFakeClient(t *testing.T, definitionResults []string) {
	t.Helper()
	original := startClient
	originalDelay := lspIndexSettleDelay
	lspIndexSettleDelay = 0
	t.Cleanup(func() {
		startClient = original
		lspIndexSettleDelay = originalDelay
	})
	startClient = func(ctx context.Context, root string, command []string) (*lsp.Client, error) {
		c2sR, c2sW := io.Pipe()
		s2cR, s2cW := io.Pipe()
		go runFakeServer(c2sR, s2cW, definitionResults)
		return lsp.NewClient(s2cR, c2sW), nil
	}
}

func writeSourceFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openEnrichDB(t *testing.T, root string) *store.DB {
	t.Helper()
	db, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func enrichInsertFile(t *testing.T, tx *sql.Tx, path string) int64 {
	t.Helper()
	id, err := store.InsertFile(tx, path, "code", "sum", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func enrichInsertNode(t *testing.T, tx *sql.Tx, id string, fileID int64, kind, name, qualified string, line int) {
	t.Helper()
	_, err := tx.Exec(`
INSERT INTO nodes(id, file_id, kind, name, qualified_name, start_line, end_line, extractor, confidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, 'test', 100, 1)`, id, fileID, kind, name, qualified, line, line)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnrichRubyResolvesAssociation(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "app/models/article.rb", "class Article\n  has_many :comments\nend\n")
	writeSourceFile(t, root, "app/models/legacy/comment.rb", "class Comment\nend\n")

	db := openEnrichDB(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	articleFile := enrichInsertFile(t, tx, "app/models/article.rb")
	commentFile := enrichInsertFile(t, tx, "app/models/legacy/comment.rb")
	enrichInsertNode(t, tx, "assoc1", articleFile, "association", "comments", "Article#has_many:comments", 2)
	enrichInsertNode(t, tx, "comment_class", commentFile, "class", "Comment", "Comment", 1)

	definitionResult := fmt.Sprintf(`{"uri":"%s","range":{"start":{"line":0,"character":6}}}`,
		lsp.PathToURI(filepath.Join(root, "app/models/legacy/comment.rb")))
	withFakeClient(t, []string{definitionResult})

	if err := enrichRuby(tx, root, []string{"fake-ruby-lsp"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var kind, target string
	var confidence int
	err = db.QueryRow(`SELECT kind, confidence, target_id FROM edges WHERE source_id = 'assoc1'`).Scan(&kind, &confidence, &target)
	if err != nil {
		t.Fatalf("expected an edge from assoc1: %v", err)
	}
	if kind != "has_many" || confidence != 100 || target != "comment_class" {
		t.Fatalf("edge = kind=%q confidence=%d target=%q; want has_many/100/comment_class", kind, confidence, target)
	}
}

func TestEnrichRubyAmbiguousDefinitionLeftUnresolved(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "app/models/article.rb", "class Article\n  has_many :comments\nend\n")
	writeSourceFile(t, root, "app/models/legacy/comment.rb", "class Comment\nend\n")

	db := openEnrichDB(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	articleFile := enrichInsertFile(t, tx, "app/models/article.rb")
	commentFile := enrichInsertFile(t, tx, "app/models/legacy/comment.rb")
	enrichInsertNode(t, tx, "assoc1", articleFile, "association", "comments", "Article#has_many:comments", 2)
	enrichInsertNode(t, tx, "comment_class", commentFile, "class", "Comment", "Comment", 1)

	// Two locations for one definition request: ambiguous, must stay unresolved.
	uri := lsp.PathToURI(filepath.Join(root, "app/models/legacy/comment.rb"))
	definitionResult := fmt.Sprintf(`[{"uri":"%s","range":{"start":{"line":0,"character":6}}},{"uri":"%s","range":{"start":{"line":0,"character":6}}}]`, uri, uri)
	withFakeClient(t, []string{definitionResult})

	if err := enrichRuby(tx, root, []string{"fake-ruby-lsp"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM edges WHERE source_id = 'assoc1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no edge for ambiguous LSP result, got %d", count)
	}
}

func TestEnrichTypeScriptResolvesImportAndJSX(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, root, "app/javascript/components/App.jsx",
		"import Widget from \"@ui/widget\"\n\nexport default function App() {\n  return <Widget />\n}\n")
	writeSourceFile(t, root, "app/javascript/vendor/widget.js", "export default {}\n")
	writeSourceFile(t, root, "app/javascript/components/Widget.jsx", "export default function Widget() {\n  return null\n}\n")

	db := openEnrichDB(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	appFile := enrichInsertFile(t, tx, "app/javascript/components/App.jsx")
	vendorFile := enrichInsertFile(t, tx, "app/javascript/vendor/widget.js")
	widgetFile := enrichInsertFile(t, tx, "app/javascript/components/Widget.jsx")

	enrichInsertNode(t, tx, "import1", appFile, "js_import", "@ui/widget", "@ui/widget", 1)
	enrichInsertNode(t, tx, "jsxuse1", appFile, "jsx_use", "Widget", "Widget", 4)
	enrichInsertNode(t, tx, "vendor_file_node", vendorFile, "file", "app/javascript/vendor/widget.js", "app/javascript/vendor/widget.js", 1)
	enrichInsertNode(t, tx, "widget_component", widgetFile, "js_component", "Widget", "Widget", 1)

	importDefinition := fmt.Sprintf(`{"uri":"%s","range":{"start":{"line":0,"character":0}}}`,
		lsp.PathToURI(filepath.Join(root, "app/javascript/vendor/widget.js")))
	jsxDefinition := fmt.Sprintf(`{"uri":"%s","range":{"start":{"line":0,"character":0}}}`,
		lsp.PathToURI(filepath.Join(root, "app/javascript/components/Widget.jsx")))
	withFakeClient(t, []string{importDefinition, jsxDefinition})

	if err := enrichTypeScript(tx, root, []string{"fake-tsserver"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var importKind, importTarget string
	var importConfidence int
	if err := db.QueryRow(`SELECT kind, confidence, target_id FROM edges WHERE source_id = 'import1'`).Scan(&importKind, &importConfidence, &importTarget); err != nil {
		t.Fatalf("expected an edge from import1: %v", err)
	}
	if importKind != "imports" || importConfidence != 100 || importTarget != "vendor_file_node" {
		t.Fatalf("import edge = %q/%d/%q; want imports/100/vendor_file_node", importKind, importConfidence, importTarget)
	}

	var jsxKind, jsxTarget string
	var jsxConfidence int
	if err := db.QueryRow(`SELECT kind, confidence, target_id FROM edges WHERE source_id = 'jsxuse1'`).Scan(&jsxKind, &jsxConfidence, &jsxTarget); err != nil {
		t.Fatalf("expected an edge from jsxuse1: %v", err)
	}
	if jsxKind != "jsx_renders" || jsxConfidence != 100 || jsxTarget != "widget_component" {
		t.Fatalf("jsx edge = %q/%d/%q; want jsx_renders/100/widget_component", jsxKind, jsxConfidence, jsxTarget)
	}
}
