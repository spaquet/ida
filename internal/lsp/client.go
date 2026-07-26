package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Location is a definition/reference target, translated from LSP's URI +
// zero-based line/character range into a project-relative-friendly shape.
type Location struct {
	Path      string
	Line      int
	Character int
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pending struct {
	result json.RawMessage
	err    error
}

// Client speaks JSON-RPC over stdio to a single LSP server process. It is
// transport-agnostic (wraps an io.Reader/io.Writer pair) so it can be driven
// in tests without spawning a real server.
type Client struct {
	w       io.Writer
	writeMu sync.Mutex
	proc    *exec.Cmd

	mu       sync.Mutex
	nextID   int
	waiters  map[int]chan pending
	closed   bool
	closeErr error
}

// Start spawns command (with root as its working directory) and returns a
// Client wired to its stdin/stdout. Diagnostics on the server's stderr are
// discarded here; server misbehavior surfaces as request errors/timeouts to
// the caller instead.
func Start(ctx context.Context, root string, command []string) (*Client, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("lsp: empty command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return newClient(stdout, stdin, cmd), nil
}

// NewClient wraps an already-connected reader/writer pair (e.g. an
// io.Pipe() driven by a fake server, or any other transport) as a Client
// with no process lifecycle of its own — Close() will not wait on/kill
// anything. Intended for tests and for embedding a Client over a transport
// this package doesn't manage directly.
func NewClient(r io.Reader, w io.Writer) *Client {
	return newClient(r, w, nil)
}

// newClient is the transport-agnostic constructor: tests call it directly
// with an io.Pipe() pair and a fake server goroutine, bypassing exec.
func newClient(r io.Reader, w io.Writer, proc *exec.Cmd) *Client {
	c := &Client{
		w:       w,
		proc:    proc,
		waiters: make(map[int]chan pending),
	}
	go c.readLoop(bufio.NewReader(r))
	return c
}

// readLoop dispatches responses (a message with an id and no method) to
// their waiting request by id. Notifications and server-initiated requests
// are discarded — servers interleave notifications (e.g. window/logMessage)
// between a request and its response, so dispatch must be id-based, not
// assume in-order request/response pairing.
func (c *Client) readLoop(r *bufio.Reader) {
	for {
		msg, err := readMessage(r)
		if err != nil {
			c.failAll(err)
			return
		}
		if msg.ID != nil && msg.Method == "" {
			c.dispatch(*msg.ID, msg)
		}
	}
}

func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (c *Client) dispatch(id int, msg *rpcMessage) {
	c.mu.Lock()
	ch, ok := c.waiters[id]
	if ok {
		delete(c.waiters, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if msg.Error != nil {
		ch <- pending{err: fmt.Errorf("lsp: %s (code %d)", msg.Error.Message, msg.Error.Code)}
		return
	}
	ch <- pending{result: msg.Result}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.closeErr = err
	for id, ch := range c.waiters {
		ch <- pending{err: err}
		delete(c.waiters, id)
	}
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, fmt.Errorf("lsp: client closed: %w", err)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan pending, 1)
	c.waiters[id] = ch
	c.mu.Unlock()

	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: &id, Method: method, Params: marshal(params)}); err != nil {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		return res.result, res.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: marshal(params)})
}

func marshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func (c *Client) write(msg rpcMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// Initialize performs the initialize/initialized handshake.
func (c *Client) Initialize(ctx context.Context, rootURI string) error {
	params := map[string]any{
		"processId":    os.Getpid(),
		"rootUri":      rootURI,
		"capabilities": map[string]any{},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// DidOpen tells the server about a file's current content so definition/
// reference requests against it are meaningful.
func (c *Client) DidOpen(uri, languageID, text string) error {
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": uri, "languageId": languageID, "version": 1, "text": text,
		},
	}
	return c.notify("textDocument/didOpen", params)
}

// Definition issues textDocument/definition at a zero-based line/character
// position and normalizes the null/Location/Location[]/LocationLink[]
// response shapes the spec allows into a single Location slice.
func (c *Client) Definition(ctx context.Context, uri string, line, character int) ([]Location, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	}
	raw, err := c.request(ctx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}
	return parseDefinitionResult(raw)
}

// Close sends shutdown+exit and waits for the process to exit, killing it
// after a short grace period if it doesn't.
func (c *Client) Close(ctx context.Context) error {
	_, _ = c.request(ctx, "shutdown", nil)
	_ = c.notify("exit", nil)
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.proc == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- c.proc.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		if c.proc.Process != nil {
			_ = c.proc.Process.Kill()
		}
		<-done
	}
	return nil
}

type rawLocation struct {
	URI                  string    `json:"uri"`
	TargetURI            string    `json:"targetUri"`
	Range                *rawRange `json:"range"`
	TargetRange          *rawRange `json:"targetRange"`
	TargetSelectionRange *rawRange `json:"targetSelectionRange"`
}

type rawRange struct {
	Start rawPosition `json:"start"`
}

type rawPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func parseDefinitionResult(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var arr []rawLocation
	if err := json.Unmarshal(raw, &arr); err == nil {
		return toLocations(arr), nil
	}
	var single rawLocation
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return toLocations([]rawLocation{single}), nil
}

func toLocations(items []rawLocation) []Location {
	var out []Location
	for _, item := range items {
		uri := item.URI
		if uri == "" {
			uri = item.TargetURI
		}
		r := item.Range
		if r == nil {
			r = item.TargetSelectionRange
		}
		if r == nil {
			r = item.TargetRange
		}
		if uri == "" || r == nil {
			continue
		}
		out = append(out, Location{Path: URIToPath(uri), Line: r.Start.Line, Character: r.Start.Character})
	}
	return out
}

// PathToURI converts an absolute filesystem path to a file:// URI. On
// Windows, path uses backslashes and may start with a drive letter (e.g.
// `C:\foo\bar`); the URI form needs forward slashes and a leading slash
// before the drive letter (file:///C:/foo/bar).
func PathToURI(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

// URIToPath converts a file:// URI back to a filesystem path, kept in
// slash form: Go's filepath functions (Rel, Clean, Join, ...) accept
// forward slashes as separators on Windows too, and the rest of the
// codebase's path handling assumes slash-separated paths throughout. This
// is a simplifying ASCII-only implementation (no percent-decoding),
// consistent with the rest of the codebase's project-relative-path
// assumptions.
func URIToPath(uri string) string {
	path := strings.TrimPrefix(uri, "file://")
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return path
}
