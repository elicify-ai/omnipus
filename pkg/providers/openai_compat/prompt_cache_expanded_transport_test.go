package openai_compat

// T3 — actual request capture for every new cache transport kind.
//
// Install target: pkg/providers/openai_compat/ (package openai_compat).
//
// Oracles (prompt-caching-spec.md):
//   - Provider table "What we send" column: vercel sends the session pin
//     `prompt_cache_key` and, for a Claude model id only, a manual
//     `cache_control { "type": "ephemeral" }` on the last history message
//     before the volatile tail, never `caching` (FR-028: no `caching: auto`
//     rewrite); openrouter sends a body `session_id` plus the ephemeral mark;
//     xai sends header `x-grok-conv-id`; the affinity rows
//     (baseten / cloudflare-workers-ai / sarvam) send `x-session-affinity`;
//     alibaba sends the ephemeral mark; fireworks-ai sends only
//     `prompt_cache_key`, never `perf_metrics_in_response` (D21); scaleway
//     documents `prompt_cache_key` as unsupported and receives nothing
//     (FR-012, US-4 acceptance scenario 3).
//   - FR-012: the value is the SESSION id, never the agent id, never a
//     secret.
//   - BDD "A Vercel Claude request carries a manual mark": the mark sits on
//     the last history message BEFORE the volatile tail; an implicit
//     upstream on the same row gains no mark.
//
// Boundary: only the HTTP transport is fake. buildRequestBody, Chat and
// ChatStream (including their duplicated session-header setup) run for real
// against a capture server whose URL is deliberately unrelated to every
// provider's real host — the catalog id, not the hostname, must decide what
// is sent. Existing coverage (chatWithCacheKey) selects by hostname alone
// and configures no provider id; helpers are reused, not edited.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	// D16/FR-012: the travelling value is the session id.
	expandedTransportSessionID = "sess-expanded-c3dd1e5"
	// The agent id must never travel (FR-012). Asserted absent everywhere.
	expandedTransportAgentID = "agent-expanded-must-never-travel"
	// The volatile tail sentinel: serialized after history, never marked.
	expandedTransportTailSentinel = "EXPANDED-VOLATILE-TAIL"
)

func expandedTransportMessages() []Message {
	return []Message{
		{Role: "system", Content: "EXPANDED-TRANSPORT-STABLE-INSTRUCTIONS"},
		{Role: "user", Content: "EXPANDED-TRANSPORT-HISTORY-QUESTION"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_expanded_1", Name: "expanded_transport_tool", Arguments: map[string]any{"q": "a"}},
		}},
		{Role: "tool", ToolCallID: "call_expanded_1", Content: "EXPANDED-TRANSPORT-RESULT"},
		{Role: "user", Content: "## Current Time\n2026-10-16T12:00:00Z " + expandedTransportTailSentinel, Volatile: true},
	}
}

// expandedTransportMark is one cache_control occurrence found in a captured
// body, with the JSON path of the object that carries it.
type expandedTransportMark struct {
	Path  string
	Value map[string]any
}

func expandedTransportCollectMarks(node any, path string, out *[]expandedTransportMark) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "cache_control" {
				if m, ok := child.(map[string]any); ok {
					*out = append(*out, expandedTransportMark{Path: path, Value: m})
				}
				continue
			}
			expandedTransportCollectMarks(child, path+"."+k, out)
		}
	case []any:
		for i, child := range v {
			expandedTransportCollectMarks(child, fmt.Sprintf("%s.%d", path, i), out)
		}
	}
}

// expandedTransportCapture drives one real Chat or ChatStream call against a
// capture server and returns the request headers and the decoded body. The
// server URL is the provider's apiBase: deliberately unrelated to the
// provider's documented host, so a hostname fallback cannot pass this test.
func expandedTransportCapture(t *testing.T, providerID, model string, stream bool) (http.Header, map[string]any, []byte) {
	t.Helper()

	// The capture is written by the server's handler goroutine and read by
	// this test goroutine; the mutex, not network timing, is the
	// happens-before edge (race-detector-proof plumbing; no transport
	// override that could bypass the real Chat/ChatStream header path).
	var (
		mu             sync.Mutex
		capturedHeader http.Header
		capturedRaw    []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		capturedHeader = r.Header.Clone()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		capturedRaw = raw
		w.Header().Set("Content-Type", "text/event-stream")
		if stream {
			// Minimal complete stream: one text delta, a stop, [DONE]. The
			// request capture is the target, not the response.
			_, _ = io.WriteString(w, "data: "+sseTextChunk("ok", nil)+"\n\n")
			_, _ = io.WriteString(w, "data: "+sseTextChunk("", strPtr("stop"))+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"},
			},
		})
	}))
	t.Cleanup(server.Close)

	p := mustNewProvider(t, "expanded-key", server.URL, "", WithProviderID(providerID))
	options := map[string]any{"prompt_cache_key": expandedTransportSessionID}
	if stream {
		if _, err := p.ChatStream(t.Context(), expandedTransportMessages(), nil, model, options, nil, nil); err != nil {
			t.Fatalf("ChatStream() error: %v", err)
		}
	} else {
		if _, err := p.Chat(t.Context(), expandedTransportMessages(), nil, model, options); err != nil {
			t.Fatalf("Chat() error: %v", err)
		}
	}
	mu.Lock()
	header, raw := capturedHeader, capturedRaw
	mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("no request body was captured")
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("captured request body is not JSON: %v\n%s", err, raw)
	}
	return header, body, raw
}

func TestExpandedTransport_RequestCapture_Table(t *testing.T) {
	const wantMarkPath = "body.messages.3.content.0" // the tool result, before the volatile tail at index 4

	tests := []struct {
		name              string
		providerID        string
		model             string
		stream            bool
		wantKeyInBody     bool   // prompt_cache_key expected in the body
		wantBodySessionID bool   // session_id expected in the body
		wantHeader        string // canonical session-header name; "" = none
		wantMark          bool   // exactly one ephemeral mark, before the tail
		forbiddenKeys     []string
	}{
		{
			name:       "vercel_claude_carries_manual_mark_before_volatile_tail",
			providerID: "vercel", model: "anthropic/claude-opus-4.5",
			wantKeyInBody: true,
			wantMark:      true,
			forbiddenKeys: []string{`"caching"`},
		},
		{
			name:       "vercel_implicit_upstream_carries_no_mark",
			providerID: "vercel", model: "openai/gpt-5.1",
			wantKeyInBody: true,
			forbiddenKeys: []string{`"caching"`, `"cache_control"`},
		},
		{
			name:       "openrouter_carries_mark_and_body_session_id",
			providerID: "openrouter", model: "z-ai/glm-5.2",
			wantBodySessionID: true,
			wantMark:          true,
			forbiddenKeys:     []string{`"prompt_cache_key"`},
		},
		{
			name:       "openrouter_stream_keeps_mark_and_body_session_id",
			providerID: "openrouter", model: "z-ai/glm-5.2", stream: true,
			wantBodySessionID: true,
			wantMark:          true,
			forbiddenKeys:     []string{`"prompt_cache_key"`},
		},
		{
			name:       "xai_carries_session_header_on_chat_path",
			providerID: "xai", model: "grok-5",
			wantHeader:    "X-Grok-Conv-Id",
			forbiddenKeys: []string{`"prompt_cache_key"`, `"session_id"`, `"cache_control"`},
		},
		{
			name:       "xai_carries_session_header_on_stream_path",
			providerID: "xai", model: "grok-5", stream: true,
			wantHeader:    "X-Grok-Conv-Id",
			forbiddenKeys: []string{`"prompt_cache_key"`, `"session_id"`, `"cache_control"`},
		},
		{
			name:       "baseten_carries_affinity_header_on_chat_path",
			providerID: "baseten", model: "btl-serverless-1",
			wantHeader:    "X-Session-Affinity",
			forbiddenKeys: []string{`"prompt_cache_key"`, `"session_id"`, `"cache_control"`},
		},
		{
			name:       "baseten_carries_affinity_header_on_stream_path",
			providerID: "baseten", model: "btl-serverless-1", stream: true,
			wantHeader:    "X-Session-Affinity",
			forbiddenKeys: []string{`"prompt_cache_key"`, `"session_id"`, `"cache_control"`},
		},
		{
			name:       "alibaba_carries_ephemeral_mark_only",
			providerID: "alibaba", model: "qwen3.5-plus",
			wantMark:      true,
			forbiddenKeys: []string{`"prompt_cache_key"`, `"session_id"`},
		},
		{
			name:       "fireworks_carries_standard_key_and_no_metrics_option",
			providerID: "fireworks-ai", model: "kimi-k2.5",
			wantKeyInBody: true,
			forbiddenKeys: []string{`"perf_metrics_in_response"`, `"cache_control"`},
		},
		{
			name:       "scaleway_receives_no_cache_field_at_all",
			providerID: "scaleway", model: "gpt-oss-120b",
			forbiddenKeys: []string{`"prompt_cache_key"`, `"session_id"`, `"cache_control"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header, body, raw := expandedTransportCapture(t, tt.providerID, tt.model, tt.stream)

			// The model id reaches the wire verbatim on every transport kind.
			if got, ok := body["model"].(string); !ok || got != tt.model {
				t.Errorf("model = %v, want %q verbatim", body["model"], tt.model)
			}

			// FR-012: the session id travels, the agent id never does.
			if strings.Contains(string(raw), expandedTransportAgentID) {
				t.Errorf("request body carries the agent id %q; only the session id may travel", expandedTransportAgentID)
			}
			for name, values := range header {
				for _, v := range values {
					if strings.Contains(v, expandedTransportAgentID) {
						t.Errorf("header %s carries the agent id; only the session id may travel", name)
					}
				}
			}

			if tt.wantKeyInBody {
				if got, _ := body["prompt_cache_key"].(string); got != expandedTransportSessionID {
					t.Errorf("prompt_cache_key = %v, want the session id %q", body["prompt_cache_key"], expandedTransportSessionID)
				}
			}
			if tt.wantBodySessionID {
				if got, _ := body["session_id"].(string); got != expandedTransportSessionID {
					t.Errorf("session_id = %v, want the session id %q", body["session_id"], expandedTransportSessionID)
				}
			}

			// Header transports: exact header, exact value — on BOTH paths
			// where Chat and ChatStream duplicate the header setup.
			const (
				xaiHeader      = "X-Grok-Conv-Id"
				affinityHeader = "X-Session-Affinity"
			)
			switch {
			case tt.wantHeader != "":
				if got := header.Get(tt.wantHeader); got != expandedTransportSessionID {
					t.Errorf("header %s = %q, want the session id %q", tt.wantHeader, got, expandedTransportSessionID)
				}
				other := affinityHeader
				if tt.wantHeader == other {
					other = xaiHeader
				}
				if got := header.Get(other); got != "" {
					t.Errorf("header %s = %q, want absent on this transport kind", other, got)
				}
			default:
				if got := header.Get(xaiHeader); got != "" {
					t.Errorf("header %s = %q, want no session header on this transport kind", xaiHeader, got)
				}
				if got := header.Get(affinityHeader); got != "" {
					t.Errorf("header %s = %q, want no session header on this transport kind", affinityHeader, got)
				}
			}

			// Marks: exact count, exact location, exact value, never on the
			// volatile tail; and none at all for implicit hosts.
			var marks []expandedTransportMark
			expandedTransportCollectMarks(body, "body", &marks)
			if tt.wantMark {
				if len(marks) != 1 {
					t.Fatalf("cache_control occurrences = %d (%v), want exactly one at %s", len(marks), marks, wantMarkPath)
				}
				if marks[0].Path != wantMarkPath {
					t.Errorf("mark at %s, want it at %s (the last history message BEFORE the volatile tail)", marks[0].Path, wantMarkPath)
				}
				if marks[0].Value["type"] != "ephemeral" {
					t.Errorf("mark type = %v, want ephemeral", marks[0].Value["type"])
				}
				if ttl, present := marks[0].Value["ttl"]; present {
					t.Errorf("mark carries ttl %v; the OpenAI-style mark is {\"type\":\"ephemeral\"} with no lifetime", ttl)
				}
			} else if len(marks) != 0 {
				t.Errorf("cache_control occurrences = %d, want none for %s", len(marks), tt.providerID)
			}

			// Forbidden options and rewrites, asserted on the raw bytes so a
			// nested occurrence cannot hide.
			for _, key := range tt.forbiddenKeys {
				if strings.Contains(string(raw), key) {
					t.Errorf("request body contains forbidden key %s", key)
				}
			}

			// The volatile tail survived serialization on every transport
			// kind: still the last message, still plain string content.
			messages, ok := body["messages"].([]any)
			if !ok {
				t.Fatalf("messages = %T, want an array", body["messages"])
			}
			if len(messages) != 5 {
				t.Fatalf("messages = %d, want 5 (system, history, tool call, tool result, volatile tail)", len(messages))
			}
			tail, ok := messages[len(messages)-1].(map[string]any)
			if !ok {
				t.Fatalf("last message = %T, want an object", messages[len(messages)-1])
			}
			tailContent, ok := tail["content"].(string)
			if !ok {
				t.Fatalf("volatile tail content = %T, want a plain string (no rewrite)", tail["content"])
			}
			if !strings.Contains(tailContent, expandedTransportTailSentinel) {
				t.Errorf("volatile tail content = %q, want it to contain %q", tailContent, expandedTransportTailSentinel)
			}
		})
	}
}
