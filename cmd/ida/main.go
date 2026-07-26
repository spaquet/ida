package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/mcp"
	"github.com/spaquet/ida/internal/project"
	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
	"github.com/spaquet/ida/internal/watch"
)

const (
	version = "0.1.0"
	help    = `Ida is a local-first knowledge graph for Rails applications.

Usage:
  ida [--json] <command> [arguments]
  ida --help
  ida --version

Commands:
  init [path]          Configure and build the first index
  sync [path]          Reconcile the index with disk
  watch [path]         Keep the index current
  status [path]        Report index and watcher health
  scope <path>         Explain whether a path is indexed
  search <query>       Search files and symbols
  context <task>       Return bounded source context
  node <name-or-id>    Explain one graph node
  path <from> <to>     Find a relationship path
  impact <name-or-id>  Show likely change effects
  mcp [path]           Serve MCP over stdio

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
	case "init", "sync":
		root, err := project.Discover(arg(args, 1, "."))
		if err != nil {
			return err
		}
		result, err := index.Sync(root)
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
		defer db.Close()
		status, err := db.Status()
		if err != nil {
			return err
		}
		return printValue(status, jsonOutput)
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
		defer db.Close()
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
	case []query.Relationship:
		for _, relationship := range value {
			fmt.Printf("%s --%s--> %s (%s)\n", relationship.SourceName, relationship.Kind, relationship.TargetName, relationship.Confidence)
		}
	default:
		return errors.New("unsupported output " + strconv.Quote(fmt.Sprintf("%T", v)))
	}
	return nil
}
