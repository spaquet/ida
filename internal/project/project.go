package project

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var markers = []string{"Gemfile", "config/application.rb", "config/routes.rb"}

type Config struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
	Docs    []string `json:"docs"`
}

type Scope struct {
	root      string
	config    Config
	gitignore []string
}

type Decision struct {
	Path     string `json:"path"`
	Included bool   `json:"included"`
	Reason   string `json:"reason"`
}

func Discover(start string) (string, error) {
	path, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	for {
		if hasMarkers(path) {
			return filepath.EvalSymlinks(path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", errors.New("no Rails root found")
		}
		path = parent
	}
}

func hasMarkers(root string) bool {
	for _, marker := range markers {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(marker)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func LoadScope(root string) (*Scope, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	scope := &Scope{root: root}
	if data, err := os.ReadFile(filepath.Join(root, "ida.json")); err == nil {
		if err := json.Unmarshal(data, &scope.config); err != nil {
			return nil, errors.New("invalid ida.json: " + err.Error())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		for line := range strings.Lines(string(data)) {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "!") {
				scope.gitignore = append(scope.gitignore, strings.TrimPrefix(line, "/"))
			}
		}
	}
	return scope, nil
}

func (s *Scope) Decide(path string) Decision {
	absolute := path
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(s.root, filepath.FromSlash(path))
	}
	absolute, err := filepath.Abs(absolute)
	if err != nil {
		return Decision{Path: path, Reason: "invalid path"}
	}
	relative, err := filepath.Rel(s.root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Decision{Path: filepath.ToSlash(relative), Reason: "outside project root"}
	}
	relative = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relative)), "./")
	decision := Decision{Path: relative}
	if relative == "" || relative == "." {
		decision.Reason = "project root"
		return decision
	}
	if reason := hardExcluded(relative); reason != "" {
		decision.Reason = reason
		return decision
	}
	if matchesAny(relative, s.config.Exclude) {
		decision.Reason = "ida.json exclude"
		return decision
	}
	if matchesAny(relative, s.config.Include) {
		decision.Included, decision.Reason = true, "ida.json include"
		return decision
	}
	if matchesAny(relative, s.gitignore) {
		decision.Reason = ".gitignore"
		return decision
	}
	if defaultIncluded(relative) || matchesAny(relative, s.config.Docs) {
		decision.Included, decision.Reason = true, "Rails default"
		return decision
	}
	decision.Reason = "not in default scope"
	return decision
}

func (s *Scope) Files() ([]string, error) {
	var files []string
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && hardExcluded(relative+"/x") != "" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if s.Decide(relative).Included {
			files = append(files, relative)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

func hardExcluded(path string) string {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		switch part {
		case ".git", ".ida", "log", "tmp", "storage", "coverage", "node_modules", "dist", "build":
			return "hard safety exclusion"
		}
	}
	if strings.HasPrefix(path, "vendor/bundle/") || strings.HasPrefix(path, "public/assets/") ||
		strings.HasPrefix(path, "public/packs/") || strings.HasPrefix(path, "public/vite/") ||
		strings.HasPrefix(path, "app/assets/builds/") {
		return "generated or dependency content"
	}
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.Contains(base, "credentials") ||
		strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem") ||
		strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".sqlite") ||
		strings.HasSuffix(base, ".sqlite3") || strings.HasSuffix(base, ".map") ||
		strings.Contains(base, ".min.") {
		return "secret, database, or generated content"
	}
	return ""
}

func defaultIncluded(path string) bool {
	if slices.Contains([]string{"Gemfile", "Gemfile.lock", "package.json", "package-lock.json", "yarn.lock", "tailwind.config.js", "tailwind.config.ts", "config/routes.rb", "db/schema.rb", "db/structure.sql"}, path) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	if strings.HasPrefix(path, "app/") {
		return slices.Contains([]string{".rb", ".erb", ".haml", ".slim", ".js", ".jsx", ".ts", ".tsx", ".css", ".scss", ".sass"}, ext)
	}
	if strings.HasPrefix(path, "config/") {
		return ext == ".rb" || ext == ".yml" || ext == ".yaml"
	}
	if strings.HasPrefix(path, "lib/") || strings.HasPrefix(path, "db/migrate/") {
		return true
	}
	return slices.Contains([]string{".md", ".markdown", ".adoc", ".asciidoc", ".html", ".txt"}, ext)
}

func matchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(path, pattern) {
			return true
		}
		if strings.Contains(pattern, "**") {
			prefix, suffix, _ := strings.Cut(pattern, "**")
			if strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")) && strings.HasSuffix(path, strings.TrimPrefix(suffix, "/")) {
				return true
			}
			continue
		}
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
		if !strings.Contains(pattern, "/") {
			for _, part := range strings.Split(path, "/") {
				if matched, _ := filepath.Match(pattern, part); matched {
					return true
				}
			}
		}
	}
	return false
}
