package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/store"
	"github.com/spaquet/ida/internal/watch"
)

func TestCreateModifyDelete(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Gemfile", "")
	write("config/application.rb", "")
	write("config/routes.rb", "")
	write("app/models/user.rb", "class User\nend\n")
	if _, err := index.Sync(root); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan watch.Update, 10)
	done := make(chan error, 1)
	go func() { done <- watch.Run(ctx, root, updates) }()
	waitUpdate(t, updates)

	path := "app/models/article.rb"
	write(path, "class Article\nend\n")
	waitUpdate(t, updates)
	assertNode(t, root, "Article", 1)

	write(path, "class Story\nend\n")
	waitUpdate(t, updates)
	assertNode(t, root, "Story", 1)
	assertNode(t, root, "Article", 0)

	if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, updates)
	assertNode(t, root, "Story", 0)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

func waitUpdate(t *testing.T, updates <-chan watch.Update) {
	t.Helper()
	select {
	case update := <-updates:
		if update.Err != nil {
			t.Fatal(update.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watcher")
	}
}

func assertNode(t *testing.T, root, name string, want int) {
	t.Helper()
	db, err := store.OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got int
	if err := db.QueryRow("SELECT count(*) FROM nodes WHERE name = ?", name).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("nodes named %q = %d; want %d", name, got, want)
	}
}
