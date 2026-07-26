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
	if result.Files != 7 || result.Nodes != 16 {
		t.Fatalf("Sync() = %#v; want 7 files and 16 nodes", result)
	}
	db, err := store.OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	results, err := query.Search(db, "Publishable", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("Search() = %#v, %v; want one result", results, err)
	}
	context, err := query.Context(db, root, "publish", 5, 12_000)
	if err != nil || len(context.Files) == 0 || context.Files[0].Path != "app/models/article.rb" {
		t.Fatalf("Context() = %#v, %v", context, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ida", "ida.db")); err != nil {
		t.Fatal(err)
	}
	var edges int
	if err := db.QueryRow("SELECT count(*) FROM edges").Scan(&edges); err != nil || edges != 2 {
		t.Fatalf("edge count = %d, %v; want 2", edges, err)
	}
	path, err := query.Path(db, "GET /articles", "app/views/articles/index.html.erb", 3)
	if err != nil || len(path.Edges) != 2 || path.Edges[0].Kind != "routes_to" || path.Edges[1].Kind != "renders" {
		t.Fatalf("Path() = %#v, %v", path, err)
	}
	impact, err := query.Impact(db, "index", 2, 10)
	if err != nil || len(impact) != 2 {
		t.Fatalf("Impact() = %#v, %v", impact, err)
	}

	articlePath := filepath.Join(root, "app", "models", "article.rb")
	if err := os.WriteFile(articlePath, []byte("class Story\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Reconcile(root); err != nil {
		t.Fatal(err)
	}
	if results, err := query.Search(db, "Story", 10); err != nil || len(results) != 1 {
		t.Fatalf("Search(Story) = %#v, %v", results, err)
	}
	if results, err := query.Search(db, "release sentinel", 10); err != nil || len(results) != 1 || results[0].Path != "docs/guide.md" {
		t.Fatalf("Search(document body) = %#v, %v", results, err)
	}
}
