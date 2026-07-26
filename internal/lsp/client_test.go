package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func writeFramed(t *testing.T, w io.Writer, msg rpcMessage) {
	t.Helper()
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
}

// TestPathURIRoundTrip covers the native-OS path shape (e.g.
// `C:\foo\bar` on Windows, `/foo/bar` elsewhere). URIToPath always returns
// slash-separated paths, matching the rest of the codebase's convention,
// so the round trip is compared against the slash form of the input.
func TestPathURIRoundTrip(t *testing.T) {
	path := filepath.Join(string(filepath.Separator), "foo", "bar", "baz.rb")
	uri := PathToURI(path)
	if got, want := URIToPath(uri), filepath.ToSlash(path); got != want {
		t.Fatalf("URIToPath(PathToURI(%q)) = %q, want %q", path, got, want)
	}
}

// TestURIToPathStripsWindowsDriveLeadingSlash covers a Windows-shaped
// file:// URI (file:///C:/foo/bar), which must lose its leading slash
// before the drive letter regardless of the OS running the test.
func TestURIToPathStripsWindowsDriveLeadingSlash(t *testing.T) {
	got := URIToPath("file:///C:/foo/bar")
	if len(got) == 0 || got[0] == '/' {
		t.Fatalf("URIToPath(%q) = %q, want no leading slash before the drive letter", "file:///C:/foo/bar", got)
	}
}

// TestInitializeIgnoresInterleavedNotification reproduces the real gotcha
// found spiking ruby-lsp: the server writes a notification (e.g.
// window/logMessage) before the actual response to an in-flight request.
// A client that assumes strict request/response ordering would misread the
// notification as the response; dispatch must be id-based.
func TestInitializeIgnoresInterleavedNotification(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	client := newClient(s2cR, c2sW, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(c2sR)
		msg, err := readMessage(reader)
		if err != nil || msg.Method != "initialize" || msg.ID == nil {
			t.Errorf("expected initialize request, got %#v, err=%v", msg, err)
			return
		}

		// Interleave a notification before the response, as ruby-lsp does.
		writeFramed(t, s2cW, rpcMessage{JSONRPC: "2.0", Method: "window/logMessage", Params: json.RawMessage(`{"type":4,"message":"hi"}`)})
		writeFramed(t, s2cW, rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"capabilities":{}}`)})

		msg2, err := readMessage(reader)
		if err != nil || msg2.Method != "initialized" {
			t.Errorf("expected initialized notification, got %#v, err=%v", msg2, err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Initialize(ctx, "file:///tmp/project"); err != nil {
		t.Fatalf("Initialize() = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fake server goroutine did not finish")
	}
}

func TestDefinitionResponseShapes(t *testing.T) {
	cases := []struct {
		name       string
		resultJSON string
		want       []Location
	}{
		{"null", "null", nil},
		{"single object", `{"uri":"file:///project/app/models/comment.rb","range":{"start":{"line":3,"character":5}}}`,
			[]Location{{Path: "/project/app/models/comment.rb", Line: 3, Character: 5}}},
		{"array", `[{"uri":"file:///project/app/models/comment.rb","range":{"start":{"line":3,"character":5}}}]`,
			[]Location{{Path: "/project/app/models/comment.rb", Line: 3, Character: 5}}},
		{"location link", `[{"targetUri":"file:///project/app/models/comment.rb","targetSelectionRange":{"start":{"line":3,"character":5}}}]`,
			[]Location{{Path: "/project/app/models/comment.rb", Line: 3, Character: 5}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c2sR, c2sW := io.Pipe()
			s2cR, s2cW := io.Pipe()
			client := newClient(s2cR, c2sW, nil)

			go func() {
				reader := bufio.NewReader(c2sR)
				msg, err := readMessage(reader)
				if err != nil || msg.Method != "textDocument/definition" {
					return
				}
				writeFramed(t, s2cW, rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(tc.resultJSON)})
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := client.Definition(ctx, "file:///project/app/models/article.rb", 1, 12)
			if err != nil {
				t.Fatalf("Definition() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Definition() = %#v; want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Definition()[%d] = %#v; want %#v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestRequestTimesOutWithoutResponse(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, _ := io.Pipe()
	client := newClient(s2cR, c2sW, nil)
	go func() {
		reader := bufio.NewReader(c2sR)
		_, _ = readMessage(reader) // drain, never respond
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Definition(ctx, "file:///x.rb", 0, 0); err == nil {
		t.Fatal("Definition() = nil error; want timeout")
	}
}
