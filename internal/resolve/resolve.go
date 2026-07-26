package resolve

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	return resolveMentions(tx, generation)
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
INSERT INTO edges(id, source_id, target_id, kind, confidence, file_id, start_line, evidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hex.EncodeToString(sum[:]), source, target, kind, confidence, fileID, line, evidence, generation)
	return err
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
