package index

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spaquet/ida/internal/extract"
	"github.com/spaquet/ida/internal/project"
	"github.com/spaquet/ida/internal/resolve"
	"github.com/spaquet/ida/internal/store"
)

type Result struct {
	Generation int64 `json:"generation"`
	Files      int   `json:"files"`
	Nodes      int   `json:"nodes"`
}

func Sync(root string) (result Result, err error) {
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	scope, err := project.LoadScope(root)
	if err != nil {
		return result, err
	}
	paths, err := scope.Files()
	if err != nil {
		return result, err
	}
	db, err := store.Open(root)
	if err != nil {
		return result, err
	}
	defer db.Close()
	defer func() {
		if err != nil {
			db.MarkFailed(err.Error())
		}
	}()
	status, err := db.Status()
	if err != nil {
		return result, err
	}
	result.Generation = status.Generation + 1
	tx, err := db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM nodes"); err != nil {
		return result, err
	}
	if _, err = tx.Exec("DELETE FROM files"); err != nil {
		return result, err
	}
	for _, path := range paths {
		nodes, insertErr := insertFile(tx, root, path, result.Generation)
		if insertErr != nil {
			return result, insertErr
		}
		result.Files++
		result.Nodes += nodes
	}
	if err = resolve.All(tx, result.Generation); err != nil {
		return result, err
	}
	if _, err = tx.Exec("UPDATE projects SET generation = ?, state = 'complete', indexed_at = ?, error = '' WHERE id = 1", result.Generation, store.IndexedAt()); err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}

func Refresh(root string, changed []string) (result Result, err error) {
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	scope, err := project.LoadScope(root)
	if err != nil {
		return result, err
	}
	paths, err := expandPaths(root, scope, changed)
	if err != nil {
		return result, err
	}
	db, err := store.Open(root)
	if err != nil {
		return result, err
	}
	defer db.Close()
	defer func() {
		if err != nil {
			db.MarkFailed(err.Error())
		}
	}()
	status, err := db.Status()
	if err != nil {
		return result, err
	}
	result.Generation = status.Generation + 1
	tx, err := db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM edges"); err != nil {
		return result, err
	}
	for _, path := range paths {
		prefix := store.EscapeLike(strings.TrimSuffix(path, "/")) + "/%"
		if _, err = tx.Exec("DELETE FROM nodes WHERE file_id IN (SELECT id FROM files WHERE path = ? OR path LIKE ? ESCAPE '\\')", path, prefix); err != nil {
			return result, err
		}
		if _, err = tx.Exec("DELETE FROM files WHERE path = ? OR path LIKE ? ESCAPE '\\'", path, prefix); err != nil {
			return result, err
		}
		full := filepath.Join(root, filepath.FromSlash(path))
		info, statErr := os.Lstat(full)
		if errors.Is(statErr, os.ErrNotExist) || statErr == nil && (info.IsDir() || info.Mode()&os.ModeSymlink != 0) || !scope.Decide(path).Included {
			continue
		}
		if statErr != nil {
			return result, statErr
		}
		nodes, insertErr := insertFile(tx, root, path, result.Generation)
		if insertErr != nil {
			return result, insertErr
		}
		result.Files++
		result.Nodes += nodes
	}
	if err = resolve.All(tx, result.Generation); err != nil {
		return result, err
	}
	if _, err = tx.Exec("UPDATE projects SET generation = ?, state = 'complete', indexed_at = ?, error = '' WHERE id = 1", result.Generation, store.IndexedAt()); err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}

func Reconcile(root string) (Result, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, err
	}
	scope, err := project.LoadScope(root)
	if err != nil {
		return Result{}, err
	}
	files, err := scope.Files()
	if err != nil {
		return Result{}, err
	}
	db, err := store.OpenExisting(root)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()
	type indexedFile struct {
		hash  string
		size  int64
		mtime int64
	}
	indexed := make(map[string]indexedFile)
	rows, err := db.Query("SELECT path, content_hash, size, mtime FROM files")
	if err != nil {
		return Result{}, err
	}
	for rows.Next() {
		var path string
		var file indexedFile
		if err := rows.Scan(&path, &file.hash, &file.size, &file.mtime); err != nil {
			rows.Close()
			return Result{}, err
		}
		indexed[path] = file
	}
	if err := rows.Close(); err != nil {
		return Result{}, err
	}
	var changed []string
	for _, path := range files {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return Result{}, err
		}
		old, found := indexed[path]
		delete(indexed, path)
		if !found {
			changed = append(changed, path)
			continue
		}
		if old.size == info.Size() && old.mtime == info.ModTime().UnixNano() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return Result{}, err
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != old.hash {
			changed = append(changed, path)
		} else {
			_, _ = db.Exec("UPDATE files SET size = ?, mtime = ? WHERE path = ?", info.Size(), info.ModTime().UnixNano(), path)
		}
	}
	for path := range indexed {
		changed = append(changed, path)
	}
	if len(changed) > 0 {
		return Refresh(root, changed)
	}
	status, err := db.Status()
	return Result{Generation: status.Generation, Files: status.Files, Nodes: status.Nodes}, err
}

func insertFile(tx *sql.Tx, root, path string, generation int64) (int, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	content, err := os.ReadFile(full)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(content)
	fileID, err := store.InsertFile(tx, path, store.Kind(path), hex.EncodeToString(sum[:]), info.Size(), info.ModTime().UnixNano(), generation)
	if err != nil {
		return 0, err
	}
	nodes := extract.File(path, content)
	for _, node := range nodes {
		_, err = tx.Exec(`
INSERT INTO nodes(id, file_id, kind, name, qualified_name, start_line, end_line, extractor, confidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			node.ID, fileID, node.Kind, node.Name, node.QualifiedName, node.StartLine, node.EndLine,
			node.Extractor, node.Confidence, generation)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
	}
	return len(nodes), nil
}

func expandPaths(root string, scope *project.Scope, changed []string) ([]string, error) {
	seen := make(map[string]bool)
	for _, path := range changed {
		if filepath.IsAbs(path) {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return nil, err
			}
			path = relative
		}
		path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
		if path == "" || path == "." || path == ".." || strings.HasPrefix(path, "../") {
			return nil, errors.New("changed path is outside project root")
		}
		seen[path] = true
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err == nil && info.IsDir() {
			files, scanErr := scope.Files()
			if scanErr != nil {
				return nil, scanErr
			}
			prefix := strings.TrimSuffix(path, "/") + "/"
			for _, file := range files {
				if strings.HasPrefix(file, prefix) {
					seen[file] = true
				}
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}
