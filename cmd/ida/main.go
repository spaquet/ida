package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/project"
	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ida:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			jsonOutput = true
			args = append(args[:i], args[i+1:]...)
			i--
		}
	}
	if len(args) == 0 {
		return errors.New("usage: ida <init|sync|status|scope|search|context> [arguments]")
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
	case "search", "context":
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
		q := strings.Join(args[1:], " ")
		if args[0] == "search" {
			results, err := query.Search(db, q, 20)
			if err != nil {
				return err
			}
			return printValue(results, jsonOutput)
		}
		results, err := query.Context(db, root, q, 5, 12_000)
		if err != nil {
			return err
		}
		return printValue(results, jsonOutput)
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
		fmt.Printf("state: %s\ngeneration: %d\nfiles: %d\nnodes: %d\nindexed: %s\n", value.State, value.Generation, value.Files, value.Nodes, value.IndexedAt)
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
	default:
		return errors.New("unsupported output " + strconv.Quote(fmt.Sprintf("%T", v)))
	}
	return nil
}
