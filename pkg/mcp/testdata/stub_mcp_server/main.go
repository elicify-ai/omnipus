// stub_mcp_server is a minimal stdio MCP server for testing.
//
// It speaks the MCP JSON-RPC 2.0 handshake over stdin/stdout and advertises
// two named tools: "stub.echo" and "stub.noop".  Used by pkg/mcp tests to
// verify that LoadFromMCPConfig can discover real tools from a subprocess MCP
// server without a real network dependency.
//
// Wire protocol:
//   - initialize request → InitializeResult (server info + capabilities)
//   - notifications/initialized notification → (ignored, no reply)
//   - tools/list request → {tools: [{name:"stub.echo",...},{name:"stub.noop",...}]}
//   - tools/call stub.echo → {content: [{type:"text",text:<echo of 'message' arg>}]}
//   - tools/call stub.noop → {content: [{type:"text",text:"noop"}]}
//   - tools/call stub.blob → a text result padded so the encoded JSON-RPC
//     response line is EXACTLY 'bytes' bytes long (excluding the trailing
//     newline). Used by the ADR-066 D10 ingest-bound tests. OPT-IN: the tool
//     is listed and callable ONLY when the process env has STUB_MCP_BLOB=1;
//     by default the server advertises exactly the two tools above, which
//     several lifecycle tests (pkg/gateway, pkg/agent) assert by count.
//   - All other methods → error -32601 (method not found)
//
// Build:
//
//	go build -o stub_mcp_server ./pkg/mcp/testdata/stub_mcp_server/
//
// The test builds it on demand via exec.Command("go", "build", ...).
//
// Traces to: Plan §9.8 — "Stub stdio MCP binary"
// License: MIT — Copyright (c) 2026 Omnipus contributors
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// jsonrpcRequest is the minimal shape of an incoming JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// jsonrpcResponse is the outbound JSON-RPC 2.0 response shape.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// blobEnabled gates the opt-in stub.blob tool (see the file header).
// Default-off keeps the advertised tool count at exactly two.
var blobEnabled = os.Getenv("STUB_MCP_BLOB") == "1"

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase the buffer limit: MCP messages can be large.
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Malformed JSON — write a parse-error response and continue.
			writeError(enc, nil, -32700, "parse error")
			continue
		}

		// Notifications (no id field) — the MCP SDK sends
		// "notifications/initialized" after the handshake.  We silently ignore
		// them (no reply is expected).
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}

		switch req.Method {
		case "initialize":
			writeResult(enc, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "stub-mcp-server",
					"version": "0.0.1",
				},
			})

		case "tools/list":
			tools := []map[string]any{
				{
					"name":        "stub.echo",
					"description": "Echoes the 'message' argument back as text.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]any{"type": "string"},
						},
						"required": []string{"message"},
					},
				},
				{
					"name":        "stub.noop",
					"description": "Returns a fixed 'noop' response.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
			}
			if blobEnabled {
				tools = append(tools, map[string]any{
					"name":        "stub.blob",
					"description": "Returns a text result whose encoded response line is exactly 'bytes' bytes.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"bytes": map[string]any{"type": "integer"},
						},
						"required": []string{"bytes"},
					},
				})
			}
			writeResult(enc, req.ID, map[string]any{"tools": tools})

		case "tools/call":
			// params shape: {"name": "stub.echo", "arguments": {...}}
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeError(enc, req.ID, -32602, fmt.Sprintf("invalid params: %v", err))
				continue
			}
			switch params.Name {
			case "stub.echo":
				msg, _ := params.Arguments["message"].(string)
				if msg == "" {
					msg = "(empty)"
				}
				writeResult(enc, req.ID, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": msg},
					},
				})
			case "stub.noop":
				writeResult(enc, req.ID, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "noop"},
					},
				})
			case "stub.blob":
				if !blobEnabled {
					writeError(enc, req.ID, -32601,
						fmt.Sprintf("tool not found: %q", params.Name))
					continue
				}
				want, _ := params.Arguments["bytes"].(float64)
				writeBlob(req.ID, int(want))
			default:
				writeError(enc, req.ID, -32601,
					fmt.Sprintf("tool not found: %q", params.Name))
			}

		default:
			writeError(enc, req.ID, -32601,
				fmt.Sprintf("method not found: %q", req.Method))
		}
	}
	// When stdin is closed by the test harness, we exit cleanly.
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stub_mcp_server: scanner error: %v\n", err)
		os.Exit(1)
	}
}

func writeResult(enc *json.Encoder, id json.RawMessage, result any) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	_ = enc.Encode(resp)
}

func writeError(enc *json.Encoder, id json.RawMessage, code int, msg string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	}
	_ = enc.Encode(resp)
}

// writeBlob writes a tools/call result whose serialized JSON-RPC line is
// exactly want bytes long (the newline delimiter is extra). The text payload
// is unescaped ASCII so its JSON length equals its byte length; the envelope
// overhead is measured once with an empty payload and subtracted.
func writeBlob(id json.RawMessage, want int) {
	build := func(text string) []byte {
		b, _ := json.Marshal(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			},
		})
		return b
	}
	overhead := len(build(""))
	if want < overhead {
		want = overhead
	}
	line := build(strings.Repeat("a", want-overhead))
	if len(line) != want {
		fmt.Fprintf(os.Stderr, "stub_mcp_server: blob size mismatch: got %d want %d\n", len(line), want)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(append(line, '\n'))
}
