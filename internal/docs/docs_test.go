package docs

import (
	"testing"

	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
)

func TestSplitAndPrivateURL(t *testing.T) {
	sections := split("https://example.com/guide.md", []byte("# One\nalpha\n## Two\nbeta"), "text/markdown")
	if len(sections) != 2 || sections[0].heading != "One" || sections[1].heading != "Two" {
		t.Fatalf("split() = %#v", sections)
	}
	if _, err := validateURL("http://127.0.0.1/private"); err == nil {
		t.Fatal("validateURL accepted a loopback address")
	}
}

func TestRemoteDocumentSearchAndContext(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replace(tx, "https://example.com/guide.md", "remote", []byte("# Guide\nremote sentinel"), "text/markdown", store.IndexedAt()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	results, err := query.Search(db, "remote sentinel", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("Search() = %#v, %v", results, err)
	}
	context, err := query.Context(db, root, "remote sentinel", 5, 12_000)
	if err != nil || len(context.Files) != 1 || context.Files[0].Path != "https://example.com/guide.md" {
		t.Fatalf("Context() = %#v, %v", context, err)
	}
}
