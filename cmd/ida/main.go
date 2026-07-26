package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spaquet/ida/internal/docs"
	"github.com/spaquet/ida/internal/doctor"
	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/lsp"
	"github.com/spaquet/ida/internal/mcp"
	"github.com/spaquet/ida/internal/project"
	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
	"github.com/spaquet/ida/internal/watch"
)

const (
	version = "0.4.0"
	help    = `Ida is a local-first knowledge graph for Rails applications.

Usage:
  ida [--json] <command> [arguments]
  ida --help
  ida --version

Commands:
  init [path] [--install-lsp]
                       Configure and build the first index
  sync [path] [--rebuild]
                       Reconcile the index with disk, or force a full rebuild
  watch [path]         Keep the index current
  status [path]        Report index and watcher health
  doctor [path]        Check Rails, index, watcher, and LSP health
  scope <path>         Explain whether a path is indexed
  search <query>       Search files and symbols
  context <task>       Return bounded source context
  node <name-or-id>    Explain one graph node
  path <from> <to>     Find a relationship path
  impact <name-or-id>  Show likely change effects
  unused <partial|view_component>
                       List partials or view components with no resolved render
  duplicates <method|stimulus_controller>
                       List declarations sharing one qualified name
  env                  List ENV variable reads, grouped by name
  docs add <path|url>  Add an explicit documentation source
  mcp [path]           Serve MCP over stdio
  mcp config [agent...]
                       Print MCP configuration snippets for supported agents
                       (claude-code, cursor, codex, pi, opencode)

Options:
  --json              Print structured command output
  -h, --help          Show this help
  --version           Show the Ida version
`
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ida:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Print(help)
			return nil
		case "--version", "version":
			fmt.Println("ida " + version)
			return nil
		}
	}
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			jsonOutput = true
			args = append(args[:i], args[i+1:]...)
			i--
		}
	}
	if len(args) == 0 {
		return errors.New("usage: ida [--json] <command> [arguments]; run ida --help")
	}

	switch args[0] {
	case "init":
		path, installLSP, err := initArgs(args[1:])
		if err != nil {
			return err
		}
		root, err := project.Discover(path)
		if err != nil {
			return err
		}
		result, err := index.Sync(root)
		if err != nil {
			return err
		}
		if err := printValue(result, jsonOutput); err != nil {
			return err
		}
		if installLSP {
			_, err = lsp.InstallMissing(context.Background(), root, os.Stdin, os.Stderr)
		}
		return err
	case "sync":
		path, rebuild, err := syncArgs(args[1:])
		if err != nil {
			return err
		}
		root, err := project.Discover(path)
		if err != nil {
			return err
		}
		var result index.Result
		if rebuild || !indexExists(root) {
			result, err = index.Sync(root)
		} else {
			result, err = index.Reconcile(root)
		}
		if err != nil {
			return err
		}
		return printValue(result, jsonOutput)
	case "watch":
		root, err := project.Discover(arg(args, 1, "."))
		if err != nil {
			return err
		}
		if _, err := index.Sync(root); err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		updates := make(chan watch.Update, 1)
		go func() {
			for update := range updates {
				if update.Err != nil {
					fmt.Fprintln(os.Stderr, "ida watch:", update.Err)
				} else if len(update.Paths) > 0 {
					fmt.Fprintf(os.Stderr, "ida watch: refreshed %d path(s)\n", len(update.Paths))
				}
			}
		}()
		fmt.Fprintf(os.Stderr, "ida watch: watching %s (Ctrl+C to stop)\n", root)
		return watch.Run(ctx, root, updates)
	case "status":
		root, err := project.Discover(arg(args, 1, "."))
		if err != nil {
			return err
		}
		db, err := store.Open(root)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		status, err := db.Status()
		if err != nil {
			return err
		}
		status = doctor.WithLSPIntegrations(root, status)
		return printValue(status, jsonOutput)
	case "doctor":
		root, err := project.Discover(arg(args, 1, "."))
		if err != nil {
			return err
		}
		return printValue(doctor.Run(root), jsonOutput)
	case "docs":
		if len(args) != 3 || args[1] != "add" {
			return errors.New("usage: ida docs add <path|url>")
		}
		root, err := project.Discover(".")
		if err != nil {
			return err
		}
		source := args[2]
		if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
			result, err := docs.AddRemote(context.Background(), root, source)
			if err != nil {
				return err
			}
			if _, err := project.AddDocumentSource(root, result.Source); err != nil {
				return err
			}
			return printValue(result, jsonOutput)
		}
		source, err = project.AddDocumentSource(root, source)
		if err != nil {
			return err
		}
		if _, err := index.Sync(root); err != nil {
			return err
		}
		db, err := store.OpenExisting(root)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		result, err := docs.LocalResult(db, source)
		if err != nil {
			return err
		}
		return printValue(result, jsonOutput)
	case "scope":
		if len(args) < 2 {
			return errors.New("usage: ida scope <path>")
		}
		root, err := project.Discover(".")
		if err != nil {
			return err
		}
		scope, err := project.LoadScope(root)
		if err != nil {
			return err
		}
		decision := scope.Decide(args[1])
		return printValue(decision, jsonOutput)
	case "unused":
		if len(args) != 2 {
			return errors.New("usage: ida unused <partial|view_component>")
		}
		root, err := project.Discover(".")
		if err != nil {
			return err
		}
		db, err := store.OpenExisting(root)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		results, err := query.Unused(db, args[1])
		if err != nil {
			return err
		}
		return printValue(results, jsonOutput)
	case "duplicates":
		if len(args) != 2 {
			return errors.New("usage: ida duplicates <method|stimulus_controller>")
		}
		root, err := project.Discover(".")
		if err != nil {
			return err
		}
		db, err := store.OpenExisting(root)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		results, err := query.Duplicates(db, args[1])
		if err != nil {
			return err
		}
		return printValue(results, jsonOutput)
	case "env":
		root, err := project.Discover(".")
		if err != nil {
			return err
		}
		db, err := store.OpenExisting(root)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		results, err := query.EnvVars(db)
		if err != nil {
			return err
		}
		return printValue(results, jsonOutput)
	case "search", "context", "node", "path", "impact":
		if len(args) < 2 {
			return fmt.Errorf("usage: ida %s <query>", args[0])
		}
		root, err := project.Discover(".")
		if err != nil {
			return err
		}
		db, err := store.OpenExisting(root)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		if args[0] == "search" {
			q := strings.Join(args[1:], " ")
			results, err := query.Search(db, q, 20)
			if err != nil {
				return err
			}
			return printValue(results, jsonOutput)
		}
		if args[0] == "context" {
			results, err := query.Context(db, root, strings.Join(args[1:], " "), 5, 12_000)
			if err != nil {
				return err
			}
			return printValue(results, jsonOutput)
		}
		if args[0] == "node" {
			result, err := query.Node(db, strings.Join(args[1:], " "))
			if err != nil {
				return err
			}
			return printValue(result, jsonOutput)
		}
		if args[0] == "path" {
			if len(args) != 3 {
				return errors.New("usage: ida path <from> <to>")
			}
			result, err := query.Path(db, args[1], args[2], 4)
			if err != nil {
				return err
			}
			return printValue(result, jsonOutput)
		}
		result, err := query.Impact(db, strings.Join(args[1:], " "), 2, 50)
		if err != nil {
			return err
		}
		return printValue(result, jsonOutput)
	case "mcp":
		if len(args) >= 2 && args[1] == "config" {
			root, err := filepath.Abs(".")
			if err != nil {
				return err
			}
			binary, err := os.Executable()
			if err != nil {
				return err
			}
			configs, err := mcp.ConfigSnippets(binary, root, args[2:])
			if err != nil {
				return err
			}
			return printValue(configs, jsonOutput)
		}
		root, err := project.Discover(arg(args, 1, "."))
		if err != nil {
			return err
		}
		return mcp.Serve(context.Background(), root, os.Stdin, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func arg(args []string, i int, fallback string) string {
	if i < len(args) {
		return args[i]
	}
	return fallback
}

func indexExists(root string) bool {
	db, err := store.OpenExisting(root)
	if err != nil {
		return false
	}
	_ = db.Close()
	return true
}

func syncArgs(args []string) (string, bool, error) {
	path := "."
	pathSet := false
	rebuild := false
	for _, value := range args {
		if value == "--rebuild" {
			rebuild = true
		} else if !pathSet {
			path = value
			pathSet = true
		} else {
			return "", false, errors.New("usage: ida sync [path] [--rebuild]")
		}
	}
	return path, rebuild, nil
}

func initArgs(args []string) (string, bool, error) {
	path := "."
	pathSet := false
	installLSP := false
	for _, value := range args {
		if value == "--install-lsp" {
			installLSP = true
		} else if !pathSet {
			path = value
			pathSet = true
		} else {
			return "", false, errors.New("usage: ida init [path] [--install-lsp]")
		}
	}
	return path, installLSP, nil
}

func printValue(v any, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	switch value := v.(type) {
	case index.Result:
		fmt.Printf("indexed %d files and %d nodes (generation %d)\n", value.Files, value.Nodes, value.Generation)
	case store.Status:
		fmt.Printf("state: %s\ngeneration: %d\nfiles: %d\nnodes: %d\nedges: %d\nindexed: %s\nwatcher: %s\npending: %d\n", value.State, value.Generation, value.Files, value.Nodes, value.Edges, value.IndexedAt, value.WatcherState, len(value.PendingFiles))
		if value.LastError != "" {
			fmt.Printf("last error: %s\n", value.LastError)
		}
		if value.WatcherError != "" {
			fmt.Printf("watcher error: %s\n", value.WatcherError)
		}
		for _, path := range value.PendingFiles {
			fmt.Printf("pending file: %s\n", path)
		}
		if len(value.ExtractorVersions) > 0 {
			fmt.Printf("extractors: %s\n", strings.Join(value.ExtractorVersions, ", "))
		}
		if len(value.EnabledIntegrations) > 0 {
			fmt.Printf("integrations: %s\n", strings.Join(value.EnabledIntegrations, ", "))
		}
	case doctor.Report:
		for _, check := range value.Checks {
			fmt.Printf("%s: %s (%s)\n", check.Name, check.Status, check.Detail)
		}
		for _, server := range value.LSP {
			fmt.Printf("lsp %s: %s", server.Name, server.Status)
			if len(server.Command) > 0 {
				fmt.Printf(" (%s)", strings.Join(server.Command, " "))
			}
			fmt.Println()
			if len(server.InstallCommand) > 0 {
				fmt.Printf("  install: %s\n", strings.Join(server.InstallCommand, " "))
			}
		}
	case docs.Result:
		fmt.Printf("added %s documentation %s (%d sections)\n", value.Type, value.Source, value.Sections)
	case project.Decision:
		fmt.Printf("%s: %s (%s)\n", value.Path, map[bool]string{true: "included", false: "excluded"}[value.Included], value.Reason)
	case []store.SearchResult:
		for _, result := range value {
			fmt.Printf("%s\t%s\t%s:%d\n", result.Kind, result.Name, result.Path, result.StartLine)
		}
	case query.ContextResult:
		if value.Stale {
			fmt.Println("warning: indexed content changed; run ida sync")
		}
		for _, file := range value.Files {
			fmt.Printf("\n%s\n%s\n", file.Path, file.Excerpt)
		}
		for _, relationship := range value.Relationships {
			fmt.Printf("%s --%s--> %s (%s)\n", relationship.SourceName, relationship.Kind, relationship.TargetName, relationship.Confidence)
		}
	case query.NodeResult:
		fmt.Printf("%s\t%s\t%s:%d\n", value.Kind, value.QualifiedName, value.Path, value.StartLine)
		for _, relationship := range append(value.Incoming, value.Outgoing...) {
			fmt.Printf("%s --%s--> %s (%s)\n", relationship.SourceName, relationship.Kind, relationship.TargetName, relationship.Confidence)
		}
	case query.PathResult:
		for i, node := range value.Nodes {
			fmt.Printf("%s\t%s\n", node.Kind, node.QualifiedName)
			if i < len(value.Edges) {
				fmt.Printf("  --%s (%s)-->\n", value.Edges[i].Kind, value.Edges[i].Confidence)
			}
		}
	case []mcp.AgentConfig:
		for i, config := range value {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("%s\n%s\n%s\n\n%s\n", config.Agent, config.Description, config.Path, config.Snippet)
		}
	case []query.Relationship:
		for _, relationship := range value {
			fmt.Printf("%s --%s--> %s (%s)\n", relationship.SourceName, relationship.Kind, relationship.TargetName, relationship.Confidence)
		}
	case []query.DuplicateGroup:
		for _, group := range value {
			expected := ""
			if group.Expected {
				expected = " (expected: environment/locale config)"
			}
			fmt.Printf("%s%s\n", group.QualifiedName, expected)
			for _, loc := range group.Locations {
				fmt.Printf("  %s:%d\n", loc.Path, loc.StartLine)
			}
		}
	case []query.EnvVarGroup:
		for _, group := range value {
			fmt.Println(group.Name)
			for _, use := range group.Uses {
				fmt.Printf("  [%s] %s:%d\n", use.Category, use.Path, use.Line)
			}
		}
	default:
		return errors.New("unsupported output " + strconv.Quote(fmt.Sprintf("%T", v)))
	}
	return nil
}
