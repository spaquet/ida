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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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

// unusedEdgeKindByNodeKind maps a renderable node kind to the resolved edge
// kind that, when present, proves the node is rendered from somewhere.
var unusedEdgeKindByNodeKind = map[string]string{
	"partial":        "renders_partial",
	"view_component": "renders_component",
}

// Unused returns every node of the given kind ("partial" or
// "view_component") that has no incoming resolved render edge. It only
// reports what Ida could not find a renderer for; a partial rendered via an
// object-based `render @model` call or a dynamically computed name is not
// tracked and will appear here even though it may be used.
func Unused(db *store.DB, kind string) ([]store.SearchResult, error) {
	edgeKind, ok := unusedEdgeKindByNodeKind[kind]
	if !ok {
		return nil, fmt.Errorf("unsupported unused kind %q (want partial or view_component)", kind)
	}
	rows, err := db.Query(`
SELECT n.id, n.kind, n.name, n.qualified_name, f.path, n.start_line, n.end_line,
       f.content_hash, n.confidence, n.extractor
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE n.kind = ?
  AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = n.id AND e.kind = ?)
ORDER BY f.path`, kind, edgeKind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

// DuplicateGroup is every declaration site sharing one qualified name, e.g.
// the same method name declared twice under the same owning class/module
// (Ruby's own rule is that the load-order-last definition silently wins,
// which is exactly the risk this surfaces), or the same Stimulus controller
// identifier registered by two different files (an outright conflict: only
// one of them can ever be the controller Stimulus connects).
type DuplicateGroup struct {
	QualifiedName string               `json:"qualified_name"`
	Locations     []store.SearchResult `json:"locations"`
	Expected      bool                 `json:"expected"`
}

// duplicateKinds are the node kinds Duplicates supports: "method" (Ruby
// class/module methods and Stimulus controller action/lifecycle methods,
// both already qualified as "<owner>#<name>") and "stimulus_controller"
// (qualified by its bare identifier). Duplicate detection intentionally
// does not compare view templates (ERB has no stable owner boundary to
// scope a name to) or attempt fuzzy code-similarity matching across
// unrelated classes; it only flags an identical qualified name declared
// more than once.
var duplicateKinds = map[string]bool{
	"method":              true,
	"stimulus_controller": true,
}

// expectedDuplicatePrefixes are directories where Rails' own conventions
// mean the same setting legitimately repeats across files that are never
// loaded together (each config/environments/*.rb only loads for its own
// RAILS_ENV; each config/locales/*.yml is a distinct locale) — real
// duplicates elsewhere are reported the same way, just not flagged as
// "expected".
var expectedDuplicatePrefixes = []string{"config/environments/", "config/locales/"}

// Duplicates reports every declaration of the given kind ("method" or
// "stimulus_controller") whose qualified name is shared by more than one
// declaration site.
func Duplicates(db *store.DB, kind string) ([]DuplicateGroup, error) {
	if !duplicateKinds[kind] {
		return nil, fmt.Errorf("unsupported duplicates kind %q (want method or stimulus_controller)", kind)
	}
	where := "n.kind = ?"
	args := []any{kind}
	if kind == "method" {
		where += " AND n.qualified_name LIKE '%#%' ESCAPE '\\'"
	}
	rows, err := db.Query(`
SELECT n.id, n.kind, n.name, n.qualified_name, f.path, n.start_line, n.end_line,
       f.content_hash, n.confidence, n.extractor
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE `+where+`
  AND n.qualified_name IN (
    SELECT qualified_name FROM nodes WHERE kind = ? GROUP BY qualified_name HAVING COUNT(*) > 1
  )
ORDER BY n.qualified_name, f.path, n.start_line`, append(args, kind)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var order []string
	groups := make(map[string]*DuplicateGroup)
	for rows.Next() {
		var result store.SearchResult
		if err := rows.Scan(&result.ID, &result.Kind, &result.Name, &result.QualifiedName, &result.Path,
			&result.StartLine, &result.EndLine, &result.ContentHash, &result.Confidence, &result.Extractor); err != nil {
			return nil, err
		}
		group, ok := groups[result.QualifiedName]
		if !ok {
			group = &DuplicateGroup{QualifiedName: result.QualifiedName, Expected: true}
			groups[result.QualifiedName] = group
			order = append(order, result.QualifiedName)
		}
		group.Locations = append(group.Locations, result)
		if !hasExpectedPrefix(result.Path) {
			group.Expected = false
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	results := make([]DuplicateGroup, 0, len(order))
	for _, name := range order {
		results = append(results, *groups[name])
	}
	return results, nil
}

func hasExpectedPrefix(path string) bool {
	for _, prefix := range expectedDuplicatePrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// EnvVarUse is one ENV["NAME"]/ENV.fetch("NAME", ...) read site.
type EnvVarUse struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Category string `json:"category"`
}

// EnvVarGroup is every read site for one environment variable name,
// organized by the conventional area of the app it was read from.
type EnvVarGroup struct {
	Name string      `json:"name"`
	Uses []EnvVarUse `json:"uses"`
}

// EnvVars lists every ENV variable name Ida found being read, each with
// every file/line it reads from, grouped by name and ordered alphabetically.
// A variable only ever set (not read) in code, or read through a wrapper
// like a settings gem, will not appear here.
func EnvVars(db *store.DB) ([]EnvVarGroup, error) {
	rows, err := db.Query(`
SELECT n.name, f.path, n.start_line
FROM nodes n JOIN files f ON f.id = n.file_id
WHERE n.kind = 'env_var_use'
ORDER BY n.name, f.path, n.start_line`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var order []string
	groups := make(map[string]*EnvVarGroup)
	for rows.Next() {
		var name, path string
		var line int
		if err := rows.Scan(&name, &path, &line); err != nil {
			return nil, err
		}
		group, ok := groups[name]
		if !ok {
			group = &EnvVarGroup{Name: name}
			groups[name] = group
			order = append(order, name)
		}
		group.Uses = append(group.Uses, EnvVarUse{Path: path, Line: line, Category: envVarCategory(path)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	results := make([]EnvVarGroup, 0, len(order))
	for _, name := range order {
		results = append(results, *groups[name])
	}
	return results, nil
}

// envVarCategory buckets a read site by the conventional Rails area it
// lives in, so `ida env` can group results by database/initializer/config
// instead of just a flat file list.
func envVarCategory(path string) string {
	switch {
	case path == "config/database.yml":
		return "database"
	case strings.HasPrefix(path, "config/initializers/"):
		return "initializer"
	case strings.HasPrefix(path, "config/environments/"):
		return "environment"
	case strings.HasPrefix(path, "config/"):
		return "config"
	default:
		return "app"
	}
}

func isTokenSeparator(r rune) bool {
	isWordRune := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_!?=:#/", r)
	return !isWordRune
}

func contextSearch(db *store.DB, task string) ([]store.SearchResult, error) {
	results, err := Search(db, task, 100)
	if err != nil || len(results) > 0 {
		return results, err
	}
	seen := make(map[string]bool)
	for _, token := range strings.FieldsFunc(task, isTokenSeparator) {
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
	switch direction {
	case "incoming":
		where = "e.target_id = ?"
		args = []any{id, limit}
	case "outgoing":
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
	defer func() { _ = rows.Close() }()
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
