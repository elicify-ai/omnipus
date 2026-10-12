//go:build capture1278

package openai_compat

// Throwaway capture harness for issue #1278 (never committed): sends tool-heavy
// requests to the real provider and saves the RAW response stream (no request
// headers, no key) so the stream parser can be replayed against what the
// provider actually sends.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestCapture1278(t *testing.T) {
	store := credentials.NewStore("/tmp/1278-cred/credentials.json")
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	key, err := store.Get("openrouter_API_KEY")
	if err != nil || key == "" {
		t.Fatalf("no key")
	}
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "bash", "description": "Run a shell command", "parameters": map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"}}}},
		{"type": "function", "function": map[string]any{"name": "write_file", "description": "Write a file", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}}}},
		{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read a file", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}},
	}
	sys := "You are an autonomous coding agent. Work step by step. Before each tool call write one short sentence saying what you are about to do, then call the tool. Never stop until the task is fully done."
	prompts := []string{
		"Find the three largest files under /usr/share and show their sizes, then write a summary to /tmp/report.txt.",
		"Check which ports are listening, then check disk usage, then write both results to /tmp/sys.txt.",
		"List the files in the current directory, read the first one, then write a one-line description of it to /tmp/desc.txt.",
		"Count lines in every .go file under ./pkg/providers and write the totals to /tmp/counts.txt.",
	}
	n := 40
	if v := os.Getenv("CAPTURE_N"); v != "" {
		fmt.Sscan(v, &n)
	}
	var padB strings.Builder
	if v := os.Getenv("CAPTURE_PAD"); v != "" {
		var lines int
		fmt.Sscan(v, &lines)
		for j := 0; j < lines; j++ {
			fmt.Fprintf(&padB, "-rw-r--r-- 1 user staff %6d Oct 12 10:%02d src/module_%04d/handler_%04d.go\n", 1000+j*7, j%60, j, j*3)
		}
	}
	pad := padB.String()
	client := &http.Client{Timeout: 180 * time.Second}
	for i := 0; i < n; i++ {
		msgs := []map[string]any{{"role": "system", "content": sys}, {"role": "user", "content": prompts[i%len(prompts)]}}
		// Every other call carries prior tool rounds so the turn is "tool-heavy".
		if i%2 == 1 {
			msgs = append(msgs,
				map[string]any{"role": "assistant", "content": "Let me look first.", "tool_calls": []map[string]any{{"id": "call_a1", "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"ls -la"}`}}}},
				map[string]any{"role": "tool", "tool_call_id": "call_a1", "content": "total 8\n-rw-r--r-- 1 u g 120 a.go\n-rw-r--r-- 1 u g 340 b.go\n" + pad},
				map[string]any{"role": "assistant", "content": "Now reading a.go.", "tool_calls": []map[string]any{{"id": "call_a2", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"a.go"}`}}}},
				map[string]any{"role": "tool", "tool_call_id": "call_a2", "content": "package a\n\nfunc A() {}\n"},
			)
		}
		body := map[string]any{"model": "deepseek/deepseek-v4.1-flash", "messages": msgs, "tools": tools, "stream": true,
			"stream_options": map[string]any{"include_usage": true}, "max_tokens": 4096}
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("call %d: %v", i, err)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		path := filepath.Join("/tmp/1278-capture", fmt.Sprintf("%02d.sse", i))
		if resp.StatusCode != 200 {
			t.Logf("call %d: status %d", i, resp.StatusCode)
			continue
		}
		os.WriteFile(path, data, 0o600)
		got, perr := parseStreamResponse(context.Background(), bytes.NewReader(data), nil, nil, nil)
		if perr != nil {
			t.Logf("call %02d: PARSE ERROR %v", i, perr)
			continue
		}
		tail := got.Content
		if len(tail) > 60 {
			tail = tail[len(tail)-60:]
		}
		prov := ""
		if k := bytes.Index(data, []byte(`"provider":"`)); k >= 0 {
			rest := data[k+12:]
			prov = string(rest[:bytes.IndexByte(rest, '"')])
		}
		u := ""
		if got.Usage != nil {
			u = fmt.Sprintf("%+v", *got.Usage)
		}
		t.Logf("call %02d: prov=%s finish=%s toolcalls=%d content_len=%d usage=%s tail=%q", i, prov, got.FinishReason, len(got.ToolCalls), len(got.Content), u, strings.ReplaceAll(tail, "\n", " "))
	}
}
