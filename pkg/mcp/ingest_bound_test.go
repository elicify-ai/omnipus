package mcp

// ingest_bound_test.go — ADR-066 D10 (FR-038, B-46, DS-7 #1–#3): the MCP
// transport is bounded at ingest_bound_bytes per JSON-RPC message.
//
//   - stdio: the sandboxedStdioConn reader aborts the read on the transport
//     once a single message exceeds the bound; the call is a tool failure
//     naming the bound; the payload is never fully buffered.
//   - HTTP/SSE: http.MaxBytesReader on the POST response body; same outcome.
//   - Exactly the bound (8,000,000) and 2 MiB are accepted; nothing is
//     truncated at ingest.
//
// Test 23 in the spec's test table.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	ingestTestBound    = 8_000_000
	ingestTestTwoMiB   = 2 * 1024 * 1024
	ingestTestOverflow = ingestTestBound + 1
)

// blobText returns the text content of a stub.blob result.
func blobText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %+v", res)
	}
	tc, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// assertIngestBoundFailure asserts the error is the typed ingest-bound
// failure AND that its text names the bound (the operator-facing contract).
func assertIngestBoundFailure(t *testing.T, err error, bound int) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a tool failure, got nil")
	}
	var ibe *IngestBoundError
	if !errors.As(err, &ibe) {
		t.Fatalf("expected *IngestBoundError in chain, got %T: %v", err, err)
	}
	if ibe.Bound != int64(bound) {
		t.Fatalf("IngestBoundError.Bound = %d, want %d", ibe.Bound, bound)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", bound)) ||
		!strings.Contains(err.Error(), "ingest_bound_bytes") {
		t.Fatalf("error must name the bound and the setting; got: %v", err)
	}
}

func TestIngestBound_MCPTransport(t *testing.T) {
	binPath := buildStubServer(t)

	t.Run("stdio", func(t *testing.T) {
		cases := []struct {
			name   string
			bytes  int
			accept bool
		}{
			{"DS-7#1 2MiB accepted", ingestTestTwoMiB, true},
			{"DS-7#1 exactly-bound accepted", ingestTestBound, true},
			{"DS-7#2 bound+1 aborted on transport", ingestTestOverflow, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				mgr := NewManager()
				mgr.SetIngestBoundBytes(ingestTestBound)
				t.Cleanup(func() { _ = mgr.Close() })

				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := mgr.ConnectServer(ctx, "stub", config.MCPServerConfig{
					Command: binPath, Enabled: true,
				}); err != nil {
					t.Fatalf("ConnectServer: %v", err)
				}

				res, err := mgr.CallTool(ctx, "stub", "stub.blob",
					map[string]any{"bytes": tc.bytes})
				if tc.accept {
					if err != nil {
						t.Fatalf("expected %d-byte message accepted, got error: %v", tc.bytes, err)
					}
					// Nothing truncated: the payload length is the full
					// message minus the fixed envelope — assert it is
					// large and ends intact.
					text := blobText(t, res)
					if len(text) < tc.bytes-200 || !strings.HasSuffix(text, "aaaa") {
						t.Fatalf("payload truncated: len=%d", len(text))
					}
					return
				}
				assertIngestBoundFailure(t, err, ingestTestBound)
				// Never fully buffered: the guard fired on the transport.
				if !mgr.servers["stub"].ingest.exceeded.Load() {
					t.Fatal("transport guard did not record the abort")
				}
			})
		}
	})

	t.Run("http", func(t *testing.T) {
		cases := []struct {
			name   string
			bytes  int
			accept bool
		}{
			{"2MiB accepted", ingestTestTwoMiB, true},
			{"exactly-bound accepted", ingestTestBound, true},
			{"DS-7#3 bound+1 MaxBytesReader failure", ingestTestOverflow, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(fakeStreamableHTTPServer(t))
				t.Cleanup(srv.Close)

				mgr := NewManager()
				mgr.SetIngestBoundBytes(ingestTestBound)
				t.Cleanup(func() { _ = mgr.Close() })

				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := mgr.ConnectServer(ctx, "fake", config.MCPServerConfig{
					Type: "http", URL: srv.URL, Enabled: true,
					Headers: map[string]string{"X-Test": "1"},
				}); err != nil {
					t.Fatalf("ConnectServer: %v", err)
				}

				res, err := mgr.CallTool(ctx, "fake", "stub.blob",
					map[string]any{"bytes": tc.bytes})
				if tc.accept {
					if err != nil {
						t.Fatalf("expected %d-byte body accepted, got error: %v", tc.bytes, err)
					}
					text := blobText(t, res)
					if len(text) < tc.bytes-200 || !strings.HasSuffix(text, "aaaa") {
						t.Fatalf("payload truncated: len=%d", len(text))
					}
					return
				}
				assertIngestBoundFailure(t, err, ingestTestBound)
			})
		}
	})
}

// fakeStreamableHTTPServer is a minimal MCP Streamable-HTTP server: every
// POST gets a single application/json JSON-RPC response (notifications get
// 202), GET (the standalone notification stream) is 405. tools/call
// stub.blob answers with a body of EXACTLY 'bytes' bytes.
func fakeStreamableHTTPServer(t *testing.T) http.Handler {
	t.Helper()
	type rpcReq struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	result := func(id json.RawMessage, res any) []byte {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
		return b
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-Test") != "1" {
			t.Errorf("custom header not forwarded")
		}
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "fake-session")
			_, _ = w.Write(result(req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake-http", "version": "0"},
			}))
		case "tools/list":
			_, _ = w.Write(result(req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "stub.blob",
					"description": "blob",
					"inputSchema": map[string]any{"type": "object"},
				}},
			}))
		case "tools/call":
			want, _ := req.Params.Arguments["bytes"].(float64)
			build := func(text string) []byte {
				return result(req.ID, map[string]any{
					"content": []map[string]any{{"type": "text", "text": text}},
				})
			}
			overhead := len(build(""))
			line := build(strings.Repeat("a", int(want)-overhead))
			if len(line) != int(want) {
				t.Errorf("fake http: body size %d != %d", len(line), int(want))
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(line)))
			_, _ = w.Write(line)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}
