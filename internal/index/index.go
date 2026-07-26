package index

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spaquet/ida/internal/extract"
	"github.com/spaquet/ida/internal/project"
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
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			return result, readErr
		}
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if statErr != nil {
			return result, statErr
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		fileID, insertErr := store.InsertFile(tx, path, store.Kind(path), hash, info.Size(), info.ModTime().UnixNano(), result.Generation)
		if insertErr != nil {
			return result, insertErr
		}
		nodes := extract.File(path, content)
		for _, node := range nodes {
			_, insertErr = tx.Exec(`
INSERT INTO nodes(id, file_id, kind, name, qualified_name, start_line, end_line, extractor, confidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				node.ID, fileID, node.Kind, node.Name, node.QualifiedName, node.StartLine, node.EndLine,
				node.Extractor, node.Confidence, result.Generation)
			if insertErr != nil {
				return result, fmt.Errorf("%s: %w", path, insertErr)
			}
		}
		result.Files++
		result.Nodes += len(nodes)
	}
	if _, err = tx.Exec("UPDATE projects SET generation = ?, state = 'complete', indexed_at = ?, error = '' WHERE id = 1", result.Generation, store.IndexedAt()); err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}
