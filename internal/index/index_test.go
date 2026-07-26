package index_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
)

func TestSyncSearchAndContext(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("testdata/rails")); err != nil {
		t.Fatal(err)
	}
	result, err := index.Sync(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 4 || result.Nodes != 9 {
		t.Fatalf("Sync() = %#v; want 4 files and 9 nodes", result)
	}
	db, err := store.OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := query.Search(db, "Publishable", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("Search() = %#v, %v; want one result", results, err)
	}
	context, err := query.Context(db, root, "publish", 5, 12_000)
	if err != nil || len(context.Files) != 1 || context.Files[0].Path != "app/models/article.rb" {
		t.Fatalf("Context() = %#v, %v", context, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ida", "ida.db")); err != nil {
		t.Fatal(err)
	}
}
