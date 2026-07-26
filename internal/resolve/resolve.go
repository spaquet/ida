package resolve

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
)

func All(tx *sql.Tx, generation int64) error {
	if _, err := tx.Exec("DELETE FROM edges"); err != nil {
		return err
	}
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
