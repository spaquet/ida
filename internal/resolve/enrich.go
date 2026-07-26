package resolve

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spaquet/ida/internal/lsp"
)

const (
	lspStartTimeout   = 30 * time.Second
	lspRequestTimeout = 5 * time.Second
	// lspCloseTimeout bounds the shutdown handshake so a server that never
	// replies to "shutdown" (e.g. one that already failed initialize)
	// can't hang the caller forever — Close falls back to killing the
	// process once this expires.
	lspCloseTimeout = 5 * time.Second
)

// lspIndexSettleDelay is a pragmatic wait after initialize before issuing
// definition requests: servers (confirmed against a real ruby-lsp) index
// the workspace asynchronously in the background, and a definition request
// issued immediately after initialize reliably comes back empty even for a
// symbol the server can resolve moments later. A real fix would watch the
// server's $/progress indexing-complete notification instead of a fixed
// delay; deferred as hardening, not needed to make v1 useful. Tests set
// this to 0 since a fake server has no indexing delay to wait out.
var lspIndexSettleDelay = 2 * time.Second

// startClient is a seam over lsp.Start so tests can substitute a fake
// pipe-backed server (via lsp.NewClient) instead of spawning a real
// ruby-lsp/typescript-language-server process — real servers are slow,
// may hit the network (ruby-lsp bootstraps its own Bundler environment),
// and may or may not be installed in any given environment.
var startClient = lsp.Start

// Enrich fills gaps deterministic resolution left unresolved (never
// re-decides an already-resolved edge) using whatever LSP servers
// lsp.Detect finds available. It is strictly additive and best-effort: a
// missing server, a failed request, a timeout, or an ambiguous result never
// fails the run — only a genuine SQL error does. Diagnostics for
// skipped/degraded enrichment go to stderr (AGENTS.md: diagnostics never go
// to stdout).
//
// v1 only makes a single attempt per server per run (no restart-on-failure
// retry loop); a server that fails partway through is abandoned for the
// rest of this Enrich call, and whatever it already resolved stays.
func Enrich(tx *sql.Tx, root string, generation int64) error {
	servers, err := lsp.Detect(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ida: lsp detect: %v\n", err)
		return nil
	}
	for _, server := range servers {
		if server.Status != "available" {
			continue
		}
		switch server.Name {
		case "ruby":
			if err := enrichRuby(tx, root, server.Command, generation); err != nil {
				return err
			}
		case "typescript":
			if err := enrichTypeScript(tx, root, server.Command, generation); err != nil {
				return err
			}
		}
	}
	return nil
}

type unresolvedRow struct {
	id, value, path string
	fileID          int64
	line            int
}

// unresolvedNodes returns nodes of kind with no outgoing edge at all —
// the set deterministic resolution left untouched.
func unresolvedNodes(tx *sql.Tx, kind string) ([]unresolvedRow, error) {
	rows, err := tx.Query(`
SELECT n.id, n.qualified_name, n.file_id, n.start_line, f.path
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE n.kind = ? AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = n.id)`, kind)
	if err != nil {
		return nil, err
	}
	var items []unresolvedRow
	for rows.Next() {
		var it unresolvedRow
		if err := rows.Scan(&it.id, &it.value, &it.fileID, &it.line, &it.path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

// enrichRuby asks ruby-lsp for a definition on each unresolved association's
// target symbol (e.g. `has_many :comments`), and adds an edge at confidence
// "lsp" when it resolves to exactly one already-indexed class/module.
func enrichRuby(tx *sql.Tx, root string, command []string, generation int64) error {
	items, err := unresolvedNodes(tx, "association")
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	startCtx, cancel := context.WithTimeout(context.Background(), lspStartTimeout)
	defer cancel()
	client, err := startClient(startCtx, root, command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ida: lsp ruby unavailable, skipping enrichment: %v\n", err)
		return nil
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), lspCloseTimeout)
		defer cancel()
		_ = client.Close(closeCtx)
	}()
	if err := client.Initialize(startCtx, lsp.PathToURI(root)); err != nil {
		fmt.Fprintf(os.Stderr, "ida: lsp ruby unavailable, skipping enrichment: %v\n", err)
		return nil
	}
	time.Sleep(lspIndexSettleDelay)

	opened := make(map[string]bool)
	for _, item := range items {
		owner, rest, ok := strings.Cut(item.value, "#")
		if !ok || owner == "" {
			continue
		}
		macro, name, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}

		uri, content, ok := openSourceFile(client, opened, root, item.path, "ruby")
		if !ok {
			continue
		}
		lines := strings.Split(content, "\n")
		if item.line-1 < 0 || item.line-1 >= len(lines) {
			continue
		}
		col := columnOf(lines[item.line-1], name)
		if col < 0 {
			continue
		}

		locations, err := requestDefinition(client, uri, item.line-1, col)
		if err != nil || len(locations) != 1 {
			continue
		}
		targetID, err := nodeAtLocation(tx, root, locations[0], []string{"class", "module"})
		if err != nil {
			return err
		}
		if targetID == "" {
			continue
		}
		if err := insertEdge(tx, item.id, targetID, macro, 100, item.fileID, item.line, rest, generation); err != nil {
			return err
		}
	}
	return nil
}

// enrichTypeScript asks typescript-language-server for a definition on each
// unresolved js_import specifier (bare packages our relative-path prober
// skips, tsconfig path aliases, etc.) and each unresolved jsx_use component
// reference, adding imports/jsx_renders edges at confidence "lsp".
func enrichTypeScript(tx *sql.Tx, root string, command []string, generation int64) error {
	imports, err := unresolvedNodes(tx, "js_import")
	if err != nil {
		return err
	}
	jsxUses, err := unresolvedNodes(tx, "jsx_use")
	if err != nil {
		return err
	}
	if len(imports) == 0 && len(jsxUses) == 0 {
		return nil
	}

	startCtx, cancel := context.WithTimeout(context.Background(), lspStartTimeout)
	defer cancel()
	client, err := startClient(startCtx, root, command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ida: lsp typescript unavailable, skipping enrichment: %v\n", err)
		return nil
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), lspCloseTimeout)
		defer cancel()
		_ = client.Close(closeCtx)
	}()
	if err := client.Initialize(startCtx, lsp.PathToURI(root)); err != nil {
		fmt.Fprintf(os.Stderr, "ida: lsp typescript unavailable, skipping enrichment: %v\n", err)
		return nil
	}
	time.Sleep(lspIndexSettleDelay)

	opened := make(map[string]bool)
	for _, item := range imports {
		uri, content, ok := openSourceFile(client, opened, root, item.path, jsLanguageID(item.path))
		if !ok {
			continue
		}
		lines := strings.Split(content, "\n")
		if item.line-1 < 0 || item.line-1 >= len(lines) {
			continue
		}
		col := columnOf(lines[item.line-1], item.value)
		if col < 0 {
			continue
		}
		locations, err := requestDefinition(client, uri, item.line-1, col)
		if err != nil || len(locations) != 1 {
			continue
		}
		targetID, err := nodeAtLocation(tx, root, locations[0], []string{"file"})
		if err != nil {
			return err
		}
		if targetID == "" {
			continue
		}
		if err := insertEdge(tx, item.id, targetID, "imports", 100, item.fileID, item.line, item.value, generation); err != nil {
			return err
		}
	}

	for _, item := range jsxUses {
		uri, content, ok := openSourceFile(client, opened, root, item.path, jsLanguageID(item.path))
		if !ok {
			continue
		}
		lines := strings.Split(content, "\n")
		if item.line-1 < 0 || item.line-1 >= len(lines) {
			continue
		}
		line := lines[item.line-1]
		col := columnOf(line, "<"+item.value)
		if col >= 0 {
			col++ // land past "<", on the identifier itself
		} else {
			col = columnOf(line, item.value)
		}
		if col < 0 {
			continue
		}
		locations, err := requestDefinition(client, uri, item.line-1, col)
		if err != nil || len(locations) != 1 {
			continue
		}
		targetID, err := nodeAtLocation(tx, root, locations[0], []string{"js_component", "js_export"})
		if err != nil {
			return err
		}
		if targetID == "" {
			continue
		}
		if err := insertEdge(tx, item.id, targetID, "jsx_renders", 100, item.fileID, item.line, item.value, generation); err != nil {
			return err
		}
	}
	return nil
}

func jsLanguageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	default:
		return "javascript"
	}
}

// openSourceFile reads path from disk and, the first time it's seen, tells
// the server about it via didOpen. Returns the file's URI and content.
func openSourceFile(client *lsp.Client, opened map[string]bool, root, path, languageID string) (uri, content string, ok bool) {
	full := filepath.Join(root, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", "", false
	}
	uri = lsp.PathToURI(full)
	content = string(data)
	if opened[path] {
		return uri, content, true
	}
	if err := client.DidOpen(uri, languageID, content); err != nil {
		fmt.Fprintf(os.Stderr, "ida: lsp didOpen %s: %v\n", path, err)
		return "", "", false
	}
	opened[path] = true
	return uri, content, true
}

func requestDefinition(client *lsp.Client, uri string, line, character int) ([]lsp.Location, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lspRequestTimeout)
	defer cancel()
	return client.Definition(ctx, uri, line, character)
}

// columnOf finds needle's byte offset on line. This assumes ASCII source,
// the same simplifying assumption the rest of extract.go's line-scanning
// makes, since LSP character offsets are UTF-16 code units that coincide
// with byte offsets for ASCII text.
func columnOf(line, needle string) int {
	if needle == "" {
		return -1
	}
	return strings.Index(line, needle)
}

// nodeAtLocation matches an LSP definition location back to an already
// indexed node of one of kinds. Zero matches: nothing to link. One match:
// use it. Multiple matches in the same file: only resolve if exactly one
// starts on the location's line, otherwise leave it unresolved rather than
// guess.
func nodeAtLocation(tx *sql.Tx, root string, loc lsp.Location, kinds []string) (string, error) {
	relPath, ok := relativize(root, loc.Path)
	if !ok {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	args := make([]any, 0, len(kinds)+1)
	args = append(args, relPath)
	for _, k := range kinds {
		args = append(args, k)
	}
	rows, err := tx.Query(`
SELECT n.id, n.start_line FROM nodes n JOIN files f ON f.id = n.file_id
WHERE f.path = ? AND n.kind IN (`+placeholders+`)`, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	type candidate struct {
		id   string
		line int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.line); err != nil {
			return "", err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(candidates) {
	case 0:
		return "", nil
	case 1:
		return candidates[0].id, nil
	}
	var exact []candidate
	for _, c := range candidates {
		if c.line == loc.Line+1 {
			exact = append(exact, c)
		}
	}
	if len(exact) == 1 {
		return exact[0].id, nil
	}
	return "", nil
}

// relativize converts an absolute definition-location path into the
// project-relative slash path node records are keyed by.
func relativize(root, absPath string) (string, bool) {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
