package query_test

import (
	"database/sql"
	"testing"

	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
)

func open(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertFile(t *testing.T, tx *sql.Tx, path string) int64 {
	t.Helper()
	id, err := store.InsertFile(tx, path, "code", "sum", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertNode(t *testing.T, tx *sql.Tx, id string, fileID int64, kind, name, qualified string, line int) {
	t.Helper()
	_, err := tx.Exec(`
INSERT INTO nodes(id, file_id, kind, name, qualified_name, start_line, end_line, extractor, confidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, 'test', 'exact', 1)`, id, fileID, kind, name, qualified, line, line)
	if err != nil {
		t.Fatal(err)
	}
}

func insertEdge(t *testing.T, tx *sql.Tx, id, source, target, kind string, fileID int64) {
	t.Helper()
	_, err := tx.Exec(`
INSERT INTO edges(id, source_id, target_id, kind, confidence, file_id, start_line, evidence, generation)
VALUES (?, ?, ?, ?, 'convention', ?, 1, '', 1)`, id, source, target, kind, fileID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnusedPartials(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	formFile := insertFile(t, tx, "app/views/articles/_form.html.erb")
	sidebarFile := insertFile(t, tx, "app/views/articles/_sidebar.html.erb")
	viewFile := insertFile(t, tx, "app/views/articles/index.html.erb")
	insertNode(t, tx, "form", formFile, "partial", "form", "articles/form", 1)
	insertNode(t, tx, "sidebar", sidebarFile, "partial", "sidebar", "articles/sidebar", 1)
	insertNode(t, tx, "use1", viewFile, "partial_use", "form", "form", 1)
	insertEdge(t, tx, "e1", "use1", "form", "renders_partial", viewFile)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	results, err := query.Unused(db, "partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "sidebar" {
		t.Fatalf("Unused(partial) = %#v; want only sidebar", results)
	}
}

func TestUnusedUnsupportedKind(t *testing.T) {
	db := open(t)
	if _, err := query.Unused(db, "bogus"); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}
