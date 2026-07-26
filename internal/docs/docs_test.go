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

func TestExtractLinksAndMentions(t *testing.T) {
	body := "See [the guide](https://example.com/guide) and <a href=\"handbook.md\">handbook</a>.\n" +
		"Use `ArticlesController` to render `app/models/article.rb`, not a `full sentence`."
	links := extractLinks(body)
	if len(links) != 2 || links[0] != "handbook.md" || links[1] != "https://example.com/guide" {
		t.Fatalf("extractLinks() = %#v", links)
	}
	mentions := extractMentions(body)
	if len(mentions) != 2 || mentions[0] != "ArticlesController" || mentions[1] != "app/models/article.rb" {
		t.Fatalf("extractMentions() = %#v", mentions)
	}
}

func TestRemoteDocumentSearchAndContext(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
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
