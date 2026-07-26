package query

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spaquet/ida/internal/store"
)

type ContextFile struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Excerpt    string `json:"excerpt"`
	Confidence string `json:"confidence"`
}

type ContextResult struct {
	Files     []ContextFile `json:"files"`
	Stale     bool          `json:"stale"`
	Truncated bool          `json:"truncated"`
}

func Search(db *store.DB, term string, limit int) ([]store.SearchResult, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, errors.New("query must not be empty")
	}
	if len(term) > 1_000 {
		return nil, errors.New("query exceeds 1000 bytes")
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	like := "%" + store.EscapeLike(strings.ToLower(term)) + "%"
	rows, err := db.Query(`
SELECT n.id, n.kind, n.name, n.qualified_name, f.path, n.start_line, n.end_line,
       f.content_hash, n.confidence, n.extractor
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE lower(n.name) LIKE ? ESCAPE '\' OR lower(n.qualified_name) LIKE ? ESCAPE '\' OR lower(f.path) LIKE ? ESCAPE '\'
ORDER BY CASE
  WHEN lower(n.qualified_name) = lower(?) THEN 0
  WHEN lower(n.name) = lower(?) THEN 1
  WHEN lower(n.name) LIKE ? ESCAPE '\' THEN 2
  ELSE 3 END, f.path, n.start_line
LIMIT ?`, like, like, like, term, term, store.EscapeLike(strings.ToLower(term))+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []store.SearchResult
	for rows.Next() {
		var result store.SearchResult
		if err := rows.Scan(&result.ID, &result.Kind, &result.Name, &result.QualifiedName, &result.Path,
			&result.StartLine, &result.EndLine, &result.ContentHash, &result.Confidence, &result.Extractor); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func Context(db *store.DB, root, task string, fileLimit, byteLimit int) (ContextResult, error) {
	if fileLimit < 1 || fileLimit > 20 || byteLimit < 1 || byteLimit > 20_000 {
		return ContextResult{}, errors.New("context limits out of range")
	}
	results, err := Search(db, task, 100)
	if err != nil {
		return ContextResult{}, err
	}
	var output ContextResult
	seen := make(map[string]bool)
	remaining := byteLimit
	for _, result := range results {
		if seen[result.Path] {
			continue
		}
		if len(output.Files) == fileLimit || remaining == 0 {
			output.Truncated = true
			break
		}
		seen[result.Path] = true
		content, err := readInside(root, result.Path)
		if err != nil {
			return output, err
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != result.ContentHash {
			output.Stale = true
		}
		lines := strings.Split(string(content), "\n")
		start := max(1, result.StartLine-4)
		end := min(len(lines), result.EndLine+8)
		var excerpt strings.Builder
		for line := start; line <= end; line++ {
			fmt.Fprintf(&excerpt, "%4d | %s\n", line, lines[line-1])
		}
		text := excerpt.String()
		if len(text) > remaining {
			text = text[:remaining]
			output.Truncated = true
		}
		remaining -= len(text)
		output.Files = append(output.Files, ContextFile{
			Path: result.Path, StartLine: start, EndLine: end, Excerpt: text, Confidence: result.Confidence,
		})
	}
	return output, nil
}

func readInside(root, relative string) ([]byte, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("indexed path escaped project root")
	}
	return os.ReadFile(resolved)
}
