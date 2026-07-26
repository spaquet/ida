package extract

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Node struct {
	ID            string
	Kind          string
	Name          string
	QualifiedName string
	StartLine     int
	EndLine       int
	Extractor     string
	Confidence    string
}

var (
	rubyType   = regexp.MustCompile(`^\s*(class|module)\s+([A-Z][A-Za-z0-9_:]*)`)
	rubyMethod = regexp.MustCompile(`^\s*def\s+(self\.)?([A-Za-z_][A-Za-z0-9_!?=]*)`)
	heading    = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	adocHead   = regexp.MustCompile(`^(={1,6})\s+(.+?)\s*$`)
)

func File(path string, content []byte) []Node {
	nodes := []Node{node(path, "file", path, path, 1, lineCount(content), "file-v1")}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".rb" {
		nodes = append(nodes, ruby(path, content)...)
	}
	if ext == ".md" || ext == ".markdown" || ext == ".adoc" || ext == ".asciidoc" {
		nodes = append(nodes, headings(path, content, ext)...)
	}
	return nodes
}

func ruby(path string, content []byte) []Node {
	var nodes []Node
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if match := rubyType.FindStringSubmatch(text); match != nil {
			name := match[2]
			nodes = append(nodes, node(path, match[1], lastPart(name), name, line, line, "ruby-declarations-v1"))
			continue
		}
		if match := rubyMethod.FindStringSubmatch(text); match != nil {
			name := match[2]
			qualified := name
			if match[1] != "" {
				qualified = "self." + name
			}
			nodes = append(nodes, node(path, "method", name, qualified, line, line, "ruby-declarations-v1"))
		}
	}
	return nodes
}

func headings(path string, content []byte, ext string) []Node {
	var nodes []Node
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for line := 1; scanner.Scan(); line++ {
		match := heading.FindStringSubmatch(scanner.Text())
		if ext == ".adoc" || ext == ".asciidoc" {
			match = adocHead.FindStringSubmatch(scanner.Text())
		}
		if match != nil {
			name := strings.TrimSpace(match[2])
			nodes = append(nodes, node(path, "document_section", name, path+"#"+name, line, line, "document-headings-v1"))
		}
	}
	return nodes
}

func node(path, kind, name, qualified string, start, end int, extractor string) Node {
	sum := sha256.Sum256([]byte(path + "\x00" + kind + "\x00" + qualified + "\x00" + strconv.Itoa(start)))
	return Node{
		ID: hex.EncodeToString(sum[:]), Kind: kind, Name: name, QualifiedName: qualified,
		StartLine: start, EndLine: end, Extractor: extractor, Confidence: "exact",
	}
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 1
	}
	return strings.Count(string(content), "\n") + 1
}

func lastPart(name string) string {
	parts := strings.Split(name, "::")
	return parts[len(parts)-1]
}
