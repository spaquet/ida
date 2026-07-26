package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spaquet/ida/internal/index"
	"github.com/spaquet/ida/internal/query"
	"github.com/spaquet/ida/internal/store"
	"github.com/spaquet/ida/internal/watch"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var tools = []tool{
	{Name: "ida_status", Description: "Report index freshness and counts.", InputSchema: object(nil, nil)},
	{Name: "ida_search", Description: "Search indexed files and symbols.", InputSchema: object(map[string]any{"query": stringSchema(), "limit": integerSchema(1, 100)}, []string{"query"})},
	{Name: "ida_context", Description: "Return bounded line-numbered source excerpts and relationships.", InputSchema: object(map[string]any{"task": stringSchema(), "file_limit": integerSchema(1, 20), "byte_limit": integerSchema(1, 20000)}, []string{"task"})},
	{Name: "ida_node", Description: "Explain one node and its direct relationships.", InputSchema: object(map[string]any{"name": stringSchema()}, []string{"name"})},
	{Name: "ida_path", Description: "Find a short directed relationship path.", InputSchema: object(map[string]any{"from": stringSchema(), "to": stringSchema(), "depth": integerSchema(1, 6)}, []string{"from", "to"})},
	{Name: "ida_impact", Description: "Show bounded likely upstream and downstream effects.", InputSchema: object(map[string]any{"name": stringSchema(), "depth": integerSchema(1, 4), "limit": integerSchema(1, 100)}, []string{"name"})},
	{Name: "ida_refresh", Description: "Refresh changed paths or reconcile the full index.", InputSchema: object(map[string]any{"paths": map[string]any{"type": "array", "items": stringSchema(), "maxItems": 1000}}, nil)},
}

func Serve(parent context.Context, root string, input io.Reader, output, diagnostics io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	updates := make(chan watch.Update, 8)
	go func() {
		if err := watch.Run(ctx, root, updates); err != nil {
			fmt.Fprintln(diagnostics, "ida mcp watcher:", err)
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updates:
				if update.Err != nil {
					fmt.Fprintln(diagnostics, "ida mcp watcher:", update.Err)
				}
			}
		}
	}()

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if err := encoder.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		result, rpcErr := handle(root, request)
		if len(request.ID) == 0 {
			continue
		}
		if err := encoder.Encode(response{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcErr}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handle(root string, request request) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "ida", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := decode(request.Params, &call); err != nil {
			return nil, invalid(err)
		}
		value, err := callTool(root, call.Name, call.Arguments)
		if err != nil {
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, internal(err)
		}
		return map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": string(data)}},
			"structuredContent": value,
		}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func callTool(root, name string, arguments json.RawMessage) (any, error) {
	if name == "ida_refresh" {
		var input struct {
			Paths []string `json:"paths"`
		}
		if err := decode(arguments, &input); err != nil {
			return nil, err
		}
		if len(input.Paths) == 0 {
			return index.Sync(root)
		}
		if len(input.Paths) > 1000 {
			return nil, errors.New("too many paths")
		}
		return index.Refresh(root, input.Paths)
	}
	db, err := store.OpenExisting(root)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	switch name {
	case "ida_status":
		if err := decode(arguments, &struct{}{}); err != nil {
			return nil, err
		}
		return db.Status()
	case "ida_search":
		var input struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := decode(arguments, &input); err != nil {
			return nil, err
		}
		if input.Limit == 0 {
			input.Limit = 20
		}
		return query.Search(db, input.Query, input.Limit)
	case "ida_context":
		var input struct {
			Task      string `json:"task"`
			FileLimit int    `json:"file_limit"`
			ByteLimit int    `json:"byte_limit"`
		}
		if err := decode(arguments, &input); err != nil {
			return nil, err
		}
		if input.FileLimit == 0 {
			input.FileLimit = 5
		}
		if input.ByteLimit == 0 {
			input.ByteLimit = 12_000
		}
		return query.Context(db, root, input.Task, input.FileLimit, input.ByteLimit)
	case "ida_node":
		var input struct {
			Name string `json:"name"`
		}
		if err := decode(arguments, &input); err != nil {
			return nil, err
		}
		return query.Node(db, input.Name)
	case "ida_path":
		var input struct {
			From  string `json:"from"`
			To    string `json:"to"`
			Depth int    `json:"depth"`
		}
		if err := decode(arguments, &input); err != nil {
			return nil, err
		}
		if input.Depth == 0 {
			input.Depth = 4
		}
		return query.Path(db, input.From, input.To, input.Depth)
	case "ida_impact":
		var input struct {
			Name  string `json:"name"`
			Depth int    `json:"depth"`
			Limit int    `json:"limit"`
		}
		if err := decode(arguments, &input); err != nil {
			return nil, err
		}
		if input.Depth == 0 {
			input.Depth = 2
		}
		if input.Limit == 0 {
			input.Limit = 50
		}
		return query.Impact(db, input.Name, input.Depth, input.Limit)
	default:
		return nil, errors.New("unknown tool")
	}
}

func decode(data json.RawMessage, target any) error {
	if len(data) == 0 || string(data) == "null" {
		data = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func object(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 1000}
}

func integerSchema(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}

func invalid(err error) *rpcError {
	return &rpcError{Code: -32602, Message: err.Error()}
}

func internal(err error) *rpcError {
	return &rpcError{Code: -32603, Message: err.Error()}
}
