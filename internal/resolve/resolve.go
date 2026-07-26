package resolve

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
)

func All(tx *sql.Tx, generation int64) error {
	if _, err := tx.Exec("DELETE FROM edges"); err != nil {
		return err
	}
	if err := resolveRoutes(tx, generation); err != nil {
		return err
	}
	if err := resolveAssociations(tx, generation); err != nil {
		return err
	}
	if err := resolveMentions(tx, generation); err != nil {
		return err
	}
	if err := resolveStimulus(tx, generation); err != nil {
		return err
	}
	if err := resolveImports(tx, generation); err != nil {
		return err
	}
	if err := resolveJSX(tx, generation); err != nil {
		return err
	}
	if err := resolveReactMounts(tx, generation); err != nil {
		return err
	}
	return resolveTailwind(tx, generation)
}

func resolveRoutes(tx *sql.Tx, generation int64) error {
	rows, err := tx.Query(`
SELECT n.id, n.qualified_name, n.start_line, n.file_id
FROM nodes n WHERE n.kind = 'route'`)
	if err != nil {
		return err
	}
	type route struct {
		id, target string
		line       int
		fileID     int64
	}
	var routes []route
	for rows.Next() {
		var item route
		if err := rows.Scan(&item.id, &item.target, &item.line, &item.fileID); err != nil {
			_ = rows.Close()
			return err
		}
		routes = append(routes, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, route := range routes {
		controller, action, ok := strings.Cut(route.target, "#")
		if !ok || controller == "" || action == "" {
			continue
		}
		controllerPath := "app/controllers/" + controller + "_controller.rb"
		actionID, actionFileID, actionLine, count, err := uniqueNode(tx, controllerPath, "method", action)
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			continue
		}
		if err := insertEdge(tx, route.id, actionID, "routes_to", "convention", route.fileID, route.line, route.target, generation); err != nil {
			return err
		}
		viewID, count, err := uniqueView(tx, "app/views/"+controller+"/"+action+".")
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			continue
		}
		if err := insertEdge(tx, actionID, viewID, "renders", "convention", actionFileID, actionLine, "unique implicit Rails view", generation); err != nil {
			return err
		}
	}
	return nil
}

// resolveAssociations turns has_many/has_one/belongs_to/habtm declarations
// into edges targeting the conventionally named model class, e.g. Article
// has_many :comments -> an edge to the Comment class node.
func resolveAssociations(tx *sql.Tx, generation int64) error {
	rows, err := tx.Query(`
SELECT n.id, n.qualified_name, n.file_id, n.start_line
FROM nodes n WHERE n.kind = 'association'`)
	if err != nil {
		return err
	}
	type assoc struct {
		id, qualified string
		fileID        int64
		line          int
	}
	var items []assoc
	for rows.Next() {
		var item assoc
		if err := rows.Scan(&item.id, &item.qualified, &item.fileID, &item.line); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		owner, rest, ok := strings.Cut(item.qualified, "#")
		if !ok {
			continue
		}
		macro, name, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		ownerID, count, err := uniqueNodeByName(tx, "class", owner)
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			continue
		}
		targetID, count, err := uniqueNodeByName(tx, "class", associationTarget(macro, name))
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			continue
		}
		if err := insertEdge(tx, ownerID, targetID, macro, "convention", item.fileID, item.line, rest, generation); err != nil {
			return err
		}
	}
	return nil
}

// resolveMentions links a local document section to a code node when the
// section explicitly mentions its exact name or qualified name in a code
// span, e.g. a backtick-quoted `ArticlesController`.
func resolveMentions(tx *sql.Tx, generation int64) error {
	rows, err := tx.Query(`
SELECT n.id, n.file_id, n.start_line, s.mentions
FROM nodes n
JOIN files f ON f.id = n.file_id
JOIN documents d ON d.source = f.path AND d.source_type = 'local'
JOIN document_sections s ON s.document_id = d.id
  AND s.heading_path = substr(n.qualified_name, length(f.path) + 2)
WHERE n.kind = 'document_section'`)
	if err != nil {
		return err
	}
	type mentionRow struct {
		id       string
		fileID   int64
		line     int
		mentions string
	}
	var items []mentionRow
	for rows.Next() {
		var item mentionRow
		if err := rows.Scan(&item.id, &item.fileID, &item.line, &item.mentions); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		var mentions []string
		if err := json.Unmarshal([]byte(item.mentions), &mentions); err != nil || len(mentions) == 0 {
			continue
		}
		for _, mention := range mentions {
			targetID, count, err := uniqueMentionTarget(tx, mention)
			if err != nil || count != 1 {
				if err != nil {
					return err
				}
				continue
			}
			if err := insertEdge(tx, item.id, targetID, "mentions", "convention", item.fileID, item.line, mention, generation); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueMentionTarget(tx *sql.Tx, symbol string) (string, int, error) {
	rows, err := tx.Query(`
SELECT id FROM nodes WHERE kind <> 'document_section' AND (name = ? OR qualified_name = ?) LIMIT 2`, symbol, symbol)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

// associationTarget derives the conventional target class name for an
// association macro, e.g. has_many :comments -> Comment.
func associationTarget(macro, name string) string {
	base := name
	if macro == "has_many" || macro == "has_and_belongs_to_many" {
		base = singularize(name)
	}
	return camelize(base)
}

func singularize(name string) string {
	switch {
	case strings.HasSuffix(name, "ies"):
		return strings.TrimSuffix(name, "ies") + "y"
	case strings.HasSuffix(name, "ses"), strings.HasSuffix(name, "xes"), strings.HasSuffix(name, "ches"), strings.HasSuffix(name, "shes"):
		return strings.TrimSuffix(name, "es")
	case strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss"):
		return strings.TrimSuffix(name, "s")
	default:
		return name
	}
}

func camelize(name string) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

func uniqueNodeByName(tx *sql.Tx, kind, name string) (string, int, error) {
	rows, err := tx.Query(`SELECT id FROM nodes WHERE kind = ? AND name = ? LIMIT 2`, kind, name)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

func uniqueNode(tx *sql.Tx, path, kind, name string) (string, int64, int, int, error) {
	rows, err := tx.Query(`
SELECT n.id, n.file_id, n.start_line FROM nodes n JOIN files f ON f.id = n.file_id
WHERE f.path = ? AND n.kind = ? AND n.name = ? LIMIT 2`, path, kind, name)
	if err != nil {
		return "", 0, 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	var fileID int64
	var line int
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id, &fileID, &line); err != nil {
			return "", 0, 0, 0, err
		}
		count++
	}
	return id, fileID, line, count, rows.Err()
}

func uniqueView(tx *sql.Tx, prefix string) (string, int, error) {
	rows, err := tx.Query(`
SELECT n.id FROM nodes n JOIN files f ON f.id = n.file_id
WHERE n.kind = 'file' AND f.path LIKE ? ESCAPE '\' LIMIT 2`, escapeLike(prefix)+"%")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

func insertEdge(tx *sql.Tx, source, target, kind, confidence string, fileID int64, line int, evidence string, generation int64) error {
	sum := sha256.Sum256([]byte(source + "\x00" + target + "\x00" + kind))
	_, err := tx.Exec(`
INSERT OR IGNORE INTO edges(id, source_id, target_id, kind, confidence, file_id, start_line, evidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hex.EncodeToString(sum[:]), source, target, kind, confidence, fileID, line, evidence, generation)
	return err
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

type sourceRow struct {
	id, value string
	fileID    int64
	line      int
}

func sourceRows(tx *sql.Tx, kind string) ([]sourceRow, error) {
	rows, err := tx.Query(`
SELECT id, qualified_name, file_id, start_line FROM nodes WHERE kind = ?`, kind)
	if err != nil {
		return nil, err
	}
	var items []sourceRow
	for rows.Next() {
		var item sourceRow
		if err := rows.Scan(&item.id, &item.value, &item.fileID, &item.line); err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func nameRows(tx *sql.Tx, kind string) ([]sourceRow, error) {
	rows, err := tx.Query(`
SELECT id, name, file_id, start_line FROM nodes WHERE kind = ?`, kind)
	if err != nil {
		return nil, err
	}
	var items []sourceRow
	for rows.Next() {
		var item sourceRow
		if err := rows.Scan(&item.id, &item.value, &item.fileID, &item.line); err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func uniqueNodeByQualifiedName(tx *sql.Tx, kind, qualified string) (string, int, error) {
	rows, err := tx.Query(`SELECT id FROM nodes WHERE kind = ? AND qualified_name = ? LIMIT 2`, kind, qualified)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

// resolveStimulus links data-controller="x" and data-action="x#y" template
// attribute uses to the Stimulus controller module (and its action method)
// declared with matching identifier, e.g. controllers/hello_controller.js
// registering identifier "hello".
func resolveStimulus(tx *sql.Tx, generation int64) error {
	uses, err := sourceRows(tx, "stimulus_controller_use")
	if err != nil {
		return err
	}
	for _, use := range uses {
		targetID, count, err := uniqueNodeByName(tx, "stimulus_controller", use.value)
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			continue
		}
		if err := insertEdge(tx, use.id, targetID, "stimulus_controller", "convention", use.fileID, use.line, use.value, generation); err != nil {
			return err
		}
	}

	actions, err := sourceRows(tx, "stimulus_action_use")
	if err != nil {
		return err
	}
	for _, action := range actions {
		targetID, count, err := uniqueNodeByQualifiedName(tx, "method", action.value)
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			continue
		}
		if err := insertEdge(tx, action.id, targetID, "stimulus_action", "convention", action.fileID, action.line, action.value, generation); err != nil {
			return err
		}
	}
	return nil
}

// resolveImports resolves relative js_import specifiers (./x, ../x) to the
// file they reference, probing conventional JS/TS/JSX/TSX extensions and
// index files the same way Node module resolution would. Bare/package
// specifiers (no ./ or ../ prefix) are left unresolved.
func resolveImports(tx *sql.Tx, generation int64) error {
	rows, err := tx.Query(`
SELECT n.id, n.qualified_name, n.file_id, n.start_line, f.path
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE n.kind = 'js_import'`)
	if err != nil {
		return err
	}
	type importRow struct {
		id, spec, fromPath string
		fileID             int64
		line               int
	}
	var items []importRow
	for rows.Next() {
		var item importRow
		if err := rows.Scan(&item.id, &item.spec, &item.fileID, &item.line, &item.fromPath); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	exts := []string{".js", ".jsx", ".ts", ".tsx"}
	for _, item := range items {
		if !strings.HasPrefix(item.spec, "./") && !strings.HasPrefix(item.spec, "../") {
			continue
		}
		base := path.Clean(path.Join(path.Dir(item.fromPath), item.spec))
		var candidates []string
		for _, ext := range exts {
			candidates = append(candidates, base+ext)
		}
		for _, ext := range exts {
			candidates = append(candidates, base+"/index"+ext)
		}
		var targetID string
		for _, candidate := range candidates {
			id, count, err := uniqueFileNode(tx, candidate)
			if err != nil {
				return err
			}
			if count == 1 {
				targetID = id
				break
			}
		}
		if targetID == "" {
			continue
		}
		if err := insertEdge(tx, item.id, targetID, "imports", "convention", item.fileID, item.line, item.spec, generation); err != nil {
			return err
		}
	}
	return nil
}

func uniqueFileNode(tx *sql.Tx, filePath string) (string, int, error) {
	rows, err := tx.Query(`SELECT id FROM nodes WHERE kind = 'file' AND name = ? LIMIT 2`, filePath)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

// resolveJSX links a JSX component-usage site (<Foo />) to the component it
// names, preferring a same-file declaration, then a component reached via an
// already-resolved import in the same file.
func resolveJSX(tx *sql.Tx, generation int64) error {
	uses, err := sourceRows(tx, "jsx_use")
	if err != nil {
		return err
	}
	for _, use := range uses {
		targetID, count, err := uniqueComponentInFile(tx, use.value, use.fileID)
		if err != nil {
			return err
		}
		if count != 1 {
			targetID, count, err = uniqueImportedComponent(tx, use.value, use.fileID)
			if err != nil {
				return err
			}
			if count != 1 {
				continue
			}
		}
		if err := insertEdge(tx, use.id, targetID, "jsx_renders", "convention", use.fileID, use.line, use.value, generation); err != nil {
			return err
		}
	}
	return nil
}

func uniqueComponentInFile(tx *sql.Tx, name string, fileID int64) (string, int, error) {
	rows, err := tx.Query(`
SELECT id FROM nodes WHERE kind IN ('js_component', 'js_export') AND name = ? AND file_id = ? LIMIT 2`, name, fileID)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

func uniqueImportedComponent(tx *sql.Tx, name string, fileID int64) (string, int, error) {
	rows, err := tx.Query(`
SELECT DISTINCT n.id
FROM nodes n
WHERE n.kind IN ('js_component', 'js_export') AND n.name = ? AND n.file_id IN (
  SELECT tn.file_id FROM edges e
  JOIN nodes imp ON imp.id = e.source_id
  JOIN nodes tn ON tn.id = e.target_id
  WHERE e.kind = 'imports' AND imp.file_id = ?
)
LIMIT 2`, name, fileID)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

// resolveReactMounts links a react_component("Name") ERB/Ruby helper call to
// the uniquely, conventionally named component it mounts.
func resolveReactMounts(tx *sql.Tx, generation int64) error {
	mounts, err := sourceRows(tx, "react_mount")
	if err != nil {
		return err
	}
	for _, mount := range mounts {
		targetID, count, err := uniqueNodeByName(tx, "js_component", mount.value)
		if err != nil {
			return err
		}
		if count != 1 {
			targetID, count, err = uniqueNodeByName(tx, "js_export", mount.value)
			if err != nil {
				return err
			}
			if count != 1 {
				continue
			}
		}
		if err := insertEdge(tx, mount.id, targetID, "mounts", "convention", mount.fileID, mount.line, mount.value, generation); err != nil {
			return err
		}
	}
	return nil
}

// tailwindUtilitySuffixes are the Tailwind class-name segments a custom
// theme token most commonly appears after, e.g. token "primary" as
// "bg-primary" or "text-primary".
var tailwindUtilitySuffixes = []string{
	"bg-", "text-", "border-", "ring-", "from-", "via-", "to-", "fill-", "stroke-", "outline-", "divide-", "shadow-", "accent-", "caret-", "decoration-",
}

// resolveTailwind connects each custom theme token (theme.extend.* in
// tailwind.config.js/.ts, or a CSS @apply rule using it) to every
// template/component/CSS file whose static class attribute or @apply
// directive uses it. This is intentionally not a unique-target resolution
// like the other resolvers: a design token legitimately fans out to many
// files, and each match is independently verified (the literal utility
// class name is present), not a guess between competing candidates.
func resolveTailwind(tx *sql.Tx, generation int64) error {
	tokens, err := nameRows(tx, "tailwind_token")
	if err != nil {
		return err
	}
	uses, err := sourceRows(tx, "class_attr_use")
	if err != nil {
		return err
	}
	for _, token := range tokens {
		for _, use := range uses {
			match := ""
			for _, tok := range strings.Fields(use.value) {
				if tok == token.value || tailwindUsesToken(tok, token.value) {
					match = tok
					break
				}
			}
			if match == "" {
				continue
			}
			targetID, count, err := fileNodeID(tx, use.fileID)
			if err != nil || count != 1 {
				if err != nil {
					return err
				}
				continue
			}
			if err := insertEdge(tx, token.id, targetID, "tailwind_uses", "convention", use.fileID, use.line, match, generation); err != nil {
				return err
			}
		}
	}
	return nil
}

func fileNodeID(tx *sql.Tx, fileID int64) (string, int, error) {
	rows, err := tx.Query(`SELECT id FROM nodes WHERE kind = 'file' AND file_id = ? LIMIT 2`, fileID)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", 0, err
		}
		count++
	}
	return id, count, rows.Err()
}

func tailwindUsesToken(class, token string) bool {
	for _, prefix := range tailwindUtilitySuffixes {
		if class == prefix+token {
			return true
		}
	}
	return false
}
