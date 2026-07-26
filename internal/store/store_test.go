package store

import "testing"

func TestEnsureColumnIsIdempotent(t *testing.T) {
	root := t.TempDir()
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := db.ensureColumn("document_sections", "links", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		t.Fatalf("ensureColumn on an already-migrated column failed: %v", err)
	}
	if err := db.ensureColumn("document_sections", "mentions", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		t.Fatalf("ensureColumn on an already-migrated column failed: %v", err)
	}
	if err := db.ensureColumn("document_sections", "new_column", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("ensureColumn on a new column failed: %v", err)
	}
}
