package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "turso.tech/database/tursogo"
)

type DB struct {
	*sql.DB
}

type Status struct {
	State      string `json:"state"`
	Generation int64  `json:"generation"`
	Files      int    `json:"files"`
	Nodes      int    `json:"nodes"`
	IndexedAt  string `json:"indexed_at"`
	LastError  string `json:"last_error,omitempty"`
}

type SearchResult struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Path          string `json:"path"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	ContentHash   string `json:"content_hash"`
	Confidence    string `json:"confidence"`
	Extractor     string `json:"extractor"`
}

func Open(root string) (*DB, error) {
	dir := filepath.Join(root, ".ida")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("turso", filepath.Join(dir, "ida.db"))
	if err != nil {
		return nil, err
	}
	wrapped := &DB{DB: db}
	if err := wrapped.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return wrapped, nil
}

func OpenExisting(root string) (*DB, error) {
	path := filepath.Join(root, ".ida", "ida.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("index not found; run ida init")
		}
		return nil, err
	}
	return Open(root)
}

func (db *DB) migrate() error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS projects (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  generation INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'empty',
  indexed_at TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO projects (id) VALUES (1);
CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL,
  generation INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  qualified_name TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  extractor TEXT NOT NULL,
  confidence TEXT NOT NULL,
  generation INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS nodes_file_id ON nodes(file_id);
`)
	return err
}

func (db *DB) Status() (Status, error) {
	var status Status
	err := db.QueryRow(`
SELECT state, generation, indexed_at, error,
       (SELECT count(*) FROM files), (SELECT count(*) FROM nodes)
FROM projects WHERE id = 1`).Scan(&status.State, &status.Generation, &status.IndexedAt, &status.LastError, &status.Files, &status.Nodes)
	return status, err
}

func (db *DB) MarkFailed(message string) {
	_, _ = db.Exec("UPDATE projects SET state = 'degraded', error = ? WHERE id = 1", message)
}

func IndexedAt() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func Kind(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return "file"
	}
	return ext[1:]
}

func InsertFile(tx *sql.Tx, path, kind, hash string, size, mtime, generation int64) (int64, error) {
	result, err := tx.Exec("INSERT INTO files(path, kind, content_hash, size, mtime, generation) VALUES (?, ?, ?, ?, ?, ?)", path, kind, hash, size, mtime, generation)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func EscapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
