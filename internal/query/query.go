package query

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	Files         []ContextFile  `json:"files"`
	Relationships []Relationship `json:"relationships,omitempty"`
	Stale         bool           `json:"stale"`
	Truncated     bool           `json:"truncated"`
}

type NodeResult struct {
	store.SearchResult
	Incoming []Relationship `json:"incoming"`
	Outgoing []Relationship `json:"outgoing"`
}

type Relationship struct {
	Kind       string `json:"kind"`
	Confidence string `json:"confidence"`
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	Evidence   string `json:"evidence"`
}

type PathResult struct {
	Nodes []store.SearchResult `json:"nodes"`
	Edges []Relationship       `json:"edges"`
}

type pathPrior struct {
	id   string
	edge Relationship
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
WITH candidates AS (
  SELECT n.id, n.kind, n.name, n.qualified_name, f.path, n.start_line, n.end_line,
         f.content_hash, n.confidence, n.extractor, '' AS search_text
  FROM nodes n JOIN files f ON f.id = n.file_id
  WHERE n.kind <> 'document_section'
  UNION ALL
  SELECT s.id, 'document_section', s.heading_path, d.source || '#' || s.heading_path,
         d.source, s.start_line, s.end_line, d.content_hash, 'exact', 'document-sections-v1', s.body
  FROM document_sections s JOIN documents d ON d.id = s.document_id
)
SELECT id, kind, name, qualified_name, path, start_line, end_line,
       content_hash, confidence, extractor
FROM candidates
WHERE lower(name) LIKE ? ESCAPE '\' OR lower(qualified_name) LIKE ? ESCAPE '\'
   OR lower(path) LIKE ? ESCAPE '\' OR lower(search_text) LIKE ? ESCAPE '\'
ORDER BY CASE
  WHEN lower(qualified_name) = lower(?) THEN 0
  WHEN lower(name) = lower(?) THEN 1
  WHEN lower(name) LIKE ? ESCAPE '\' THEN 2
  ELSE 3 END, path, start_line
LIMIT ?`, like, like, like, like, term, term, store.EscapeLike(strings.ToLower(term))+"%", limit)
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
	results, err := contextSearch(db, task)
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
		if strings.HasPrefix(result.Path, "http://") || strings.HasPrefix(result.Path, "https://") {
			body, truncated, err := remoteSection(db, result.ID, remaining)
			if err != nil {
				return output, err
			}
			output.Truncated = output.Truncated || truncated
			remaining -= len(body)
			output.Files = append(output.Files, ContextFile{
				Path: result.Path, StartLine: result.StartLine, EndLine: result.EndLine,
				Excerpt: body, Confidence: result.Confidence,
			})
			continue
		}
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
	seenRelationships := make(map[string]bool)
	for _, result := range results {
		relationships, err := relationships(db, result.ID, "", 20-len(output.Relationships))
		if err != nil {
			return output, err
		}
		for _, relationship := range relationships {
			key := relationship.SourceID + "\x00" + relationship.Kind + "\x00" + relationship.TargetID
			if !seenRelationships[key] {
				seenRelationships[key] = true
				output.Relationships = append(output.Relationships, relationship)
				if len(output.Relationships) == 20 {
					output.Truncated = true
					break
				}
			}
		}
	}
	return output, nil
}

func remoteSection(db *store.DB, id string, byteLimit int) (string, bool, error) {
	var body string
	var start int
	if err := db.QueryRow("SELECT body, start_line FROM document_sections WHERE id = ?", id).Scan(&body, &start); err != nil {
		return "", false, err
	}
	var excerpt strings.Builder
	for i, line := range strings.Split(body, "\n") {
		fmt.Fprintf(&excerpt, "%4d | %s\n", start+i, line)
	}
	text := excerpt.String()
	truncated := len(text) > byteLimit
	if len(text) > byteLimit {
		text = text[:byteLimit]
	}
	return text, truncated, nil
}

func Node(db *store.DB, nameOrID string) (NodeResult, error) {
	rows, err := db.Query(`
SELECT n.id, n.kind, n.name, n.qualified_name, f.path, n.start_line, n.end_line,
       f.content_hash, n.confidence, n.extractor
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE n.id = ? OR n.name = ? OR n.qualified_name = ?
ORDER BY CASE WHEN n.id = ? THEN 0 WHEN n.qualified_name = ? THEN 1 ELSE 2 END
LIMIT 2`, nameOrID, nameOrID, nameOrID, nameOrID, nameOrID)
	if err != nil {
		return NodeResult{}, err
	}
	defer rows.Close()
	var matches []store.SearchResult
	for rows.Next() {
		var result store.SearchResult
		if err := rows.Scan(&result.ID, &result.Kind, &result.Name, &result.QualifiedName, &result.Path,
			&result.StartLine, &result.EndLine, &result.ContentHash, &result.Confidence, &result.Extractor); err != nil {
			return NodeResult{}, err
		}
		matches = append(matches, result)
	}
	if len(matches) == 0 {
		return NodeResult{}, errors.New("node not found")
	}
	if len(matches) > 1 && matches[0].ID != nameOrID && matches[0].QualifiedName != nameOrID {
		return NodeResult{}, errors.New("node name is ambiguous; use its ID or qualified name")
	}
	result := NodeResult{SearchResult: matches[0]}
	result.Incoming, err = relationships(db, result.ID, "incoming", 100)
	if err == nil {
		result.Outgoing, err = relationships(db, result.ID, "outgoing", 100)
	}
	return result, err
}

func Path(db *store.DB, from, to string, maxDepth int) (PathResult, error) {
	if maxDepth < 1 || maxDepth > 6 {
		return PathResult{}, errors.New("depth must be between 1 and 6")
	}
	start, err := Node(db, from)
	if err != nil {
		return PathResult{}, err
	}
	end, err := Node(db, to)
	if err != nil {
		return PathResult{}, err
	}
	queue := []string{start.ID}
	seen := map[string]pathPrior{start.ID: {}}
	depth := map[string]int{start.ID: 0}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == end.ID {
			return buildPath(db, start.ID, end.ID, seen)
		}
		if depth[current] == maxDepth {
			continue
		}
		next, err := relationships(db, current, "outgoing", 100)
		if err != nil {
			return PathResult{}, err
		}
		for _, edge := range next {
			if _, ok := seen[edge.TargetID]; ok {
				continue
			}
			seen[edge.TargetID] = pathPrior{id: current, edge: edge}
			depth[edge.TargetID] = depth[current] + 1
			queue = append(queue, edge.TargetID)
		}
	}
	return PathResult{}, errors.New("no relationship path found")
}

func Impact(db *store.DB, nameOrID string, depth, limit int) ([]Relationship, error) {
	if depth < 1 || depth > 4 || limit < 1 || limit > 100 {
		return nil, errors.New("impact limits out of range")
	}
	start, err := Node(db, nameOrID)
	if err != nil {
		return nil, err
	}
	queue := []string{start.ID}
	seen := map[string]bool{start.ID: true}
	seenEdges := make(map[string]bool)
	var result []Relationship
	for level := 0; level < depth && len(queue) > 0 && len(result) < limit; level++ {
		current := queue
		queue = nil
		for _, id := range current {
			edges, err := relationships(db, id, "", limit-len(result))
			if err != nil {
				return nil, err
			}
			for _, edge := range edges {
				key := edge.SourceID + "\x00" + edge.Kind + "\x00" + edge.TargetID
				if !seenEdges[key] {
					seenEdges[key] = true
					result = append(result, edge)
				}
				for _, adjacent := range []string{edge.SourceID, edge.TargetID} {
					if !seen[adjacent] {
						seen[adjacent] = true
						queue = append(queue, adjacent)
					}
				}
			}
		}
	}
	return result, nil
}

func contextSearch(db *store.DB, task string) ([]store.SearchResult, error) {
	results, err := Search(db, task, 100)
	if err != nil || len(results) > 0 {
		return results, err
	}
	seen := make(map[string]bool)
	for _, token := range strings.FieldsFunc(task, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_!?=:#/", r))
	}) {
		if len(token) < 3 {
			continue
		}
		matches, err := Search(db, token, 20)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			if !seen[match.ID] {
				seen[match.ID] = true
				results = append(results, match)
			}
		}
	}
	return results, nil
}

func relationships(db *store.DB, id, direction string, limit int) ([]Relationship, error) {
	if limit <= 0 {
		return nil, nil
	}
	where := "(e.source_id = ? OR e.target_id = ?)"
	args := []any{id, id, limit}
	if direction == "incoming" {
		where = "e.target_id = ?"
		args = []any{id, limit}
	} else if direction == "outgoing" {
		where = "e.source_id = ?"
		args = []any{id, limit}
	}
	rows, err := db.Query(`
SELECT e.kind, e.confidence, e.source_id, s.name, e.target_id, t.name, e.evidence
FROM edges e JOIN nodes s ON s.id = e.source_id JOIN nodes t ON t.id = e.target_id
WHERE `+where+` ORDER BY e.kind, s.name, t.name LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Relationship
	for rows.Next() {
		var item Relationship
		if err := rows.Scan(&item.Kind, &item.Confidence, &item.SourceID, &item.SourceName, &item.TargetID, &item.TargetName, &item.Evidence); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func buildPath(db *store.DB, start, end string, seen map[string]pathPrior) (PathResult, error) {
	var ids []string
	var edges []Relationship
	for id := end; ; {
		ids = append(ids, id)
		if id == start {
			break
		}
		item := seen[id]
		edges = append(edges, item.edge)
		id = item.id
	}
	slices.Reverse(ids)
	slices.Reverse(edges)
	result := PathResult{Edges: edges}
	for _, id := range ids {
		node, err := Node(db, id)
		if err != nil {
			return PathResult{}, err
		}
		result.Nodes = append(result.Nodes, node.SearchResult)
	}
	return result, nil
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
