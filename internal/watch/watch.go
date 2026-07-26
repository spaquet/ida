package watch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/project"
	"github.com/spaquet/ida/internal/store"
)

type Update struct {
	Paths []string
	Err   error
}

func Run(ctx context.Context, root string, updates chan<- Update) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	scope, err := project.LoadScope(root)
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := addInitialDirs(watcher, root, scope); err != nil {
		return err
	}
	send(updates, Update{})

	pending := make(map[string]bool)
	var timer *time.Timer
	var timerC <-chan time.Time
	scan := time.NewTicker(30 * time.Second)
	defer scan.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			relative, err := filepath.Rel(root, event.Name)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			relative = filepath.ToSlash(relative)
			if event.Has(fsnotify.Create) {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					_ = addTree(watcher, event.Name)
				}
			}
			if scope.Decide(relative).Included || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				pending[relative] = true
				if timer == nil {
					timer = time.NewTimer(150 * time.Millisecond)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(150 * time.Millisecond)
				}
			}
		case <-timerC:
			paths := keys(pending)
			pending = make(map[string]bool)
			timerC = nil
			timer = nil
			_, err := index.Refresh(root, paths)
			send(updates, Update{Paths: paths, Err: err})
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			markDegraded(root, err)
			send(updates, Update{Err: err})
		case <-scan.C:
			_, err := index.Reconcile(root)
			send(updates, Update{Err: err})
		}
	}
}

func addInitialDirs(watcher *fsnotify.Watcher, root string, scope *project.Scope) error {
	if err := watcher.Add(root); err != nil {
		return err
	}
	files, err := scope.Files()
	if err != nil {
		return err
	}
	added := map[string]bool{root: true}
	for _, file := range files {
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(file)))
		for dir != root && !added[dir] {
			if err := watcher.Add(dir); err != nil {
				return err
			}
			added[dir] = true
			dir = filepath.Dir(dir)
		}
	}
	return nil
}

func addTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}

func send(updates chan<- Update, update Update) {
	if updates == nil {
		return
	}
	select {
	case updates <- update:
	default:
	}
}

func markDegraded(root string, err error) {
	db, openErr := store.Open(root)
	if openErr == nil {
		defer db.Close()
		db.MarkFailed(errors.New("watcher: " + err.Error()).Error())
	}
}
