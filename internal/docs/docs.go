package docs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spaquet/ida/internal/store"
)

const maxRemoteBytes = 2 << 20

type Result struct {
	Source   string `json:"source"`
	Type     string `json:"type"`
	Sections int    `json:"sections"`
}

type section struct {
	heading string
	body    string
	start   int
	end     int
}

var (
	markdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	asciidocHeading = regexp.MustCompile(`^(={1,6})\s+(.+?)\s*$`)
	htmlHeading     = regexp.MustCompile(`(?is)<h[1-6][^>]*>(.*?)</h[1-6]>`)
	htmlTag         = regexp.MustCompile(`(?s)<[^>]*>`)
)

func AddRemote(ctx context.Context, root, source string) (Result, error) {
	parsed, err := validateURL(source)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, err
	}
	response, err := remoteClient().Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("documentation request returned %s", response.Status)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "" && !strings.HasPrefix(mediaType, "text/") &&
		mediaType != "application/xhtml+xml" && mediaType != "application/octet-stream" {
		return Result{}, errors.New("remote documentation must be text")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(content) > maxRemoteBytes {
		return Result{}, fmt.Errorf("remote documentation exceeds %d bytes", maxRemoteBytes)
	}
	db, err := store.Open(root)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	count, err := replace(tx, parsed.String(), "remote", content, contentType, store.IndexedAt())
	if err == nil {
		err = tx.Commit()
	}
	return Result{Source: parsed.String(), Type: "remote", Sections: count}, err
}

func ReplaceLocal(tx *sql.Tx, root string, paths []string) error {
	if _, err := tx.Exec("DELETE FROM document_sections WHERE document_id IN (SELECT id FROM documents WHERE source_type = 'local')"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM documents WHERE source_type = 'local'"); err != nil {
		return err
	}
	for _, path := range paths {
		if !isDocument(path) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if _, err := replace(tx, path, "local", content, mime.TypeByExtension(filepath.Ext(path)), ""); err != nil {
			return err
		}
	}
	return nil
}

func LocalResult(db *store.DB, source string) (Result, error) {
	prefix := strings.TrimSuffix(source, "/**")
	like := store.EscapeLike(prefix) + "/%"
	var count int
	err := db.QueryRow(`
SELECT count(*) FROM document_sections s JOIN documents d ON d.id = s.document_id
WHERE d.source_type = 'local' AND (d.source = ? OR d.source LIKE ? ESCAPE '\')`,
		prefix, like).Scan(&count)
	return Result{Source: source, Type: "local", Sections: count}, err
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func replace(db sqlExecutor, source, sourceType string, content []byte, contentType, fetchedAt string) (int, error) {
	sum := sha256.Sum256(content)
	documentID := stableID("document", source)
	parts := split(source, content, contentType)
	title := source
	if len(parts) > 0 && parts[0].heading != "" {
		title = parts[0].heading
	}
	if _, err := db.Exec("DELETE FROM document_sections WHERE document_id IN (SELECT id FROM documents WHERE source = ?)", source); err != nil {
		return 0, err
	}
	if _, err := db.Exec("DELETE FROM documents WHERE source = ?", source); err != nil {
		return 0, err
	}
	if _, err := db.Exec(`
INSERT INTO documents(id, source, source_type, content_hash, fetched_at, title)
VALUES (?, ?, ?, ?, ?, ?)`, documentID, source, sourceType, hex.EncodeToString(sum[:]), fetchedAt, title); err != nil {
		return 0, err
	}
	for i, part := range parts {
		if _, err := db.Exec(`
INSERT INTO document_sections(id, document_id, heading_path, body, start_line, end_line)
VALUES (?, ?, ?, ?, ?, ?)`,
			stableID(documentID, fmt.Sprintf("%d", i), part.heading), documentID, part.heading,
			part.body, part.start, part.end); err != nil {
			return 0, err
		}
	}
	return len(parts), nil
}

func split(source string, content []byte, contentType string) []section {
	text := string(content)
	if strings.Contains(strings.ToLower(contentType), "html") || strings.EqualFold(filepath.Ext(urlPath(source)), ".html") {
		return splitHTML(source, text)
	}
	heading := markdownHeading
	ext := strings.ToLower(filepath.Ext(urlPath(source)))
	if ext == ".adoc" || ext == ".asciidoc" {
		heading = asciidocHeading
	}
	lines := strings.Split(text, "\n")
	var result []section
	start := 0
	title := fallbackTitle(source)
	for i, line := range lines {
		match := heading.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if start < i {
			result = append(result, section{heading: title, body: strings.Join(lines[start:i], "\n"), start: start + 1, end: i})
		}
		title = strings.TrimSpace(match[2])
		start = i
	}
	if start < len(lines) {
		result = append(result, section{heading: title, body: strings.Join(lines[start:], "\n"), start: start + 1, end: len(lines)})
	}
	if len(result) == 0 {
		result = append(result, section{heading: title, body: text, start: 1, end: max(1, len(lines))})
	}
	return result
}

func splitHTML(source, content string) []section {
	matches := htmlHeading.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return []section{{heading: fallbackTitle(source), body: content, start: 1, end: lineAt(content, len(content))}}
	}
	var result []section
	for i, match := range matches {
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		heading := html.UnescapeString(strings.TrimSpace(htmlTag.ReplaceAllString(content[match[2]:match[3]], "")))
		result = append(result, section{
			heading: heading,
			body:    content[match[0]:end],
			start:   lineAt(content, match[0]),
			end:     lineAt(content, end),
		})
	}
	return result
}

func remoteClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, address := range addresses {
				if forbiddenIP(address.IP) {
					return nil, errors.New("remote documentation resolves to a private or local address")
				}
			}
			if len(addresses) == 0 {
				return nil, errors.New("remote documentation host did not resolve")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many documentation redirects")
			}
			_, err := validateURL(request.URL.String())
			return err
		},
	}
}

func validateURL(source string) (*url.URL, error) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("documentation URL must be an HTTP(S) URL without credentials")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && forbiddenIP(ip) {
		return nil, errors.New("remote documentation URL uses a private or local address")
	}
	return parsed, nil
}

func forbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func isDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".adoc", ".asciidoc", ".html", ".txt":
		return true
	}
	return false
}

func fallbackTitle(source string) string {
	base := filepath.Base(urlPath(source))
	if base == "." || base == "/" || base == "" {
		return source
	}
	return base
}

func urlPath(source string) string {
	if parsed, err := url.Parse(source); err == nil && parsed.Path != "" {
		return parsed.Path
	}
	return source
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func lineAt(content string, offset int) int {
	return strings.Count(content[:offset], "\n") + 1
}
