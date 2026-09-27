// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// WP-E (issue #750, ORIGINAL scope only): the factory's Anthropic dispatch.
//
// #750's analysis: the static system prompt is built with an Anthropic
// cache_control: ephemeral breakpoint (pkg/agent/context.go), and the adapter
// the factory dispatches — anthropic_messages — flattens structured
// SystemParts into a plain string and emits no cache control at all. The
// adapter that DOES implement the mapping (pkg/providers/anthropic, reached
// only via ClaudeProvider) had no non-test caller.
//
// The follow-up caching epic proposed in the issue's comment thread
// (tool-array segmentation, multi-breakpoint caching, TTL, other providers'
// marker dialects) is OUT OF SCOPE for WP-E and deliberately untested here.

// oracleURL is the base URL an SDK-backed Anthropic provider reports for a
// catalog endpoint. The SDK adapter (pkg/providers/anthropic/
// provider.go::normalizeBaseURL) strips a trailing "/v1" from the row's api
// and re-adds it in its request path (<base>/v1/messages), so the row's
// catalog endpoint is reached either way. CHARACTERIZATION TEST: the exact
// stripped form pins the adapter's existing normalization; the spec-level
// claim under test is "the row's own endpoint is used, not the SDK default".
func oracleURL(catalogAPI string) string {
	base := strings.TrimRight(strings.TrimSpace(catalogAPI), "/")
	return strings.TrimSuffix(base, "/v1")
}

// TestFactoryAnthropicDispatch_BuildsSDKBackedProvider — WP-E item 1: the
// Anthropic-protocol dispatch builds the SDK-backed *ClaudeProvider (the only
// wrapper of the adapter that maps SystemParts to cache_control), NOT
// *anthropicmessages.Provider, at the row's own endpoint. All URL
// expectations derive from the fixture document
// (testdata/factory_dispatch_catalog.json), never from the factory's source.
func TestFactoryAnthropicDispatch_BuildsSDKBackedProvider(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "WP_E_ROUTING_KEY")

	tests := []struct {
		name    string
		cfg     config.ModelConfig
		wantURL string
	}{
		{
			// Fixture minimax row: api "https://api.minimax.io/anthropic/v1",
			// protocol "anthropic".
			name:    "catalog row whose own protocol is anthropic",
			cfg:     config.ModelConfig{Provider: "minimax", Model: "MiniMax-M2.7"},
			wantURL: oracleURL("https://api.minimax.io/anthropic/v1"),
		},
		{
			// Fixture zai row: protocols[1] = {anthropic, "https://api.z.ai/api/anthropic"}.
			name:    "explicit secondary protocol picks that row's endpoint",
			cfg:     config.ModelConfig{Provider: "zai", Model: "glm-5.2", Protocol: "anthropic"},
			wantURL: oracleURL("https://api.z.ai/api/anthropic"),
		},
		{
			name: "custom row on the anthropic protocol keeps its own base",
			cfg: config.ModelConfig{
				Provider: "my-proxy-2", Custom: true, Protocol: "anthropic",
				Model: "claude-x", APIBase: "https://llm2.example",
			},
			wantURL: "https://llm2.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.APIKeyRef = ref

			p, _, err := CreateProviderFromConfig(&cfg)
			if err != nil {
				t.Fatalf("CreateProviderFromConfig() error = %v", err)
			}
			got, ok := p.(*ClaudeProvider)
			if !ok {
				t.Fatalf("provider = %T, want *ClaudeProvider (WP-E #750: the SDK-backed adapter that maps SystemParts to cache_control)", p)
			}
			if gotURL := got.delegate.BaseURL(); gotURL != tt.wantURL {
				t.Errorf("base URL = %q, want %q", gotURL, tt.wantURL)
			}
		})
	}
}

// TestFactoryAnthropicDispatch_StillRequiresCredential — the dispatch's
// credential precondition (requireKey) must survive the adapter switch: a
// cloud Anthropic-protocol row with no key and no api_base is still an error
// naming the missing api_key. Green today by design; this is a regression
// guard for GREEN, not RED evidence.
func TestFactoryAnthropicDispatch_StillRequiresCredential(t *testing.T) {
	withFixtureCatalog(t)

	_, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider: "minimax", Model: "MiniMax-M2.7",
	})
	if err == nil {
		t.Fatal("an Anthropic-protocol cloud row without a credential must be rejected")
	}
	if !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("error = %q, want it to name the missing api_key", err)
	}
}

// markerProbeServer captures the JSON request body an Anthropic-protocol
// provider posts, and serves a Messages-API response both adapter families
// parse. Both adapter families must REACH this server for the captured body
// to be the oracle surface: the pre-switch adapter posts <base>/messages,
// the SDK-backed one posts <base>/v1/messages. Serving both keeps a RED
// failure on the body assertion, not on a transport 404.
func markerProbeServer(t *testing.T) (*httptest.Server, func() []byte) {
	t.Helper()
	var mu sync.Mutex
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/messages", "/v1/messages":
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		mu.Lock()
		captured = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",` +
			`"model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],` +
			`"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":3}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// TestFactoryAnthropicDispatch_CacheControlMarkerReachesWire — WP-E item 2
// (issue #750's original DoD): a provider built through the factory's ACTUAL
// Anthropic-protocol construction path must send a request whose `system`
// field is the SystemParts BLOCK ARRAY, with cache_control: ephemeral on the
// marked static block and NO marker on the unmarked one. RED today: the
// dispatched adapter drops SystemParts entirely, so `system` never reaches
// the body at all.
func TestFactoryAnthropicDispatch_CacheControlMarkerReachesWire(t *testing.T) {
	withFixtureCatalog(t)

	// Oracle (issue #750 + pkg/agent/context.go's producer contract, cited
	// there): the static block is marked, the dynamic block is deliberately
	// unmarked. Values are the test's own, derived from the producer shape —
	// never read off either adapter.
	staticPrompt := "You are Omnipus. " + strings.Repeat("stable prefix. ", 64)
	dynamicCtx := "time: 2026-09-28T00:00:00Z"

	t.Run("marked static block carries cache_control; unmarked block does not", func(t *testing.T) {
		srv, captured := markerProbeServer(t)
		p, modelID, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "wp-e-marker-probe", Custom: true, Protocol: "anthropic",
			Model:     "claude-sonnet-4-6",
			APIBase:   srv.URL,
			APIKeyRef: keyRef(t, "WP_E_MARKER_KEY"),
		})
		if err != nil {
			t.Fatalf("factory build: %v", err)
		}

		messages := []Message{
			{
				// Content stays empty: the producer sends structured SystemParts
				// with no plain Content (#750's context.go citation).
				Role: "system",
				SystemParts: []ContentBlock{
					{Type: "text", Text: staticPrompt, CacheControl: &CacheControl{Type: "ephemeral"}},
					{Type: "text", Text: dynamicCtx},
				},
			},
			{Role: "user", Content: "hi"},
		}
		resp, err := p.Chat(context.Background(), messages, nil, modelID, map[string]any{"max_tokens": 256})
		if err != nil {
			t.Fatalf("Chat: %v", err)
		}
		if resp == nil || resp.Content != "ok" {
			t.Errorf("resp = %+v, want content %q", resp, "ok")
		}

		var wire struct {
			System json.RawMessage `json:"system"`
		}
		if err := json.Unmarshal(captured(), &wire); err != nil {
			t.Fatalf("captured body is not JSON: %v", err)
		}
		if len(wire.System) == 0 || string(wire.System) == "null" {
			t.Fatalf("request body carries NO system field — the dispatched adapter dropped SystemParts entirely (issue #750); body: %s", captured())
		}
		var blocks []map[string]any
		if err := json.Unmarshal(wire.System, &blocks); err != nil {
			t.Fatalf("system is not a block array, got %s — the adapter flattened SystemParts to a plain string (issue #750)", wire.System)
		}
		if len(blocks) != 2 {
			t.Fatalf("system blocks = %d, want 2 (marked static + unmarked dynamic)", len(blocks))
		}
		if gotText, _ := blocks[0]["text"].(string); gotText != staticPrompt {
			t.Errorf("block 0 text = %q, want the static prompt", gotText)
		}
		cc, ok := blocks[0]["cache_control"].(map[string]any)
		if !ok {
			t.Fatalf("block 0 carries no cache_control object; block: %v", blocks[0])
		}
		if cc["type"] != "ephemeral" {
			t.Errorf("cache_control.type = %v, want \"ephemeral\"", cc["type"])
		}
		if _, present := blocks[1]["cache_control"]; present {
			t.Errorf("unmarked dynamic block carries a cache_control marker: %v", blocks[1])
		}
		if gotText, _ := blocks[1]["text"].(string); gotText != dynamicCtx {
			t.Errorf("block 1 text = %q, want the dynamic context", gotText)
		}
	})

	t.Run("a non-ephemeral marker is dropped, not sent", func(t *testing.T) {
		// protocoltypes.CacheControl: "Currently only 'ephemeral' is supported".
		// A marker of any other type must not reach the wire, where the API
		// would reject the whole request.
		srv, captured := markerProbeServer(t)
		p, modelID, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "wp-e-marker-probe-2", Custom: true, Protocol: "anthropic",
			Model:     "claude-sonnet-4-6",
			APIBase:   srv.URL,
			APIKeyRef: keyRef(t, "WP_E_MARKER2_KEY"),
		})
		if err != nil {
			t.Fatalf("factory build: %v", err)
		}

		messages := []Message{
			{
				Role: "system",
				SystemParts: []ContentBlock{
					{Type: "text", Text: staticPrompt, CacheControl: &CacheControl{Type: "not-a-real-type"}},
				},
			},
			{Role: "user", Content: "hi"},
		}
		if _, err := p.Chat(context.Background(), messages, nil, modelID, map[string]any{"max_tokens": 256}); err != nil {
			t.Fatalf("Chat: %v", err)
		}

		var wire struct {
			System json.RawMessage `json:"system"`
		}
		if err := json.Unmarshal(captured(), &wire); err != nil {
			t.Fatalf("captured body is not JSON: %v", err)
		}
		if len(wire.System) == 0 || string(wire.System) == "null" {
			t.Fatalf("request body carries NO system field; body: %s", captured())
		}
		var blocks []map[string]any
		if err := json.Unmarshal(wire.System, &blocks); err != nil {
			t.Fatalf("system is not a block array, got %s", wire.System)
		}
		if len(blocks) != 1 {
			t.Fatalf("system blocks = %d, want 1 (the single unmarked block)", len(blocks))
		}
		if gotText, _ := blocks[0]["text"].(string); gotText != staticPrompt {
			t.Errorf("block text = %q, want the static prompt", gotText)
		}
		if cc, present := blocks[0]["cache_control"]; present {
			t.Errorf("non-ephemeral marker reached the wire: %v", cc)
		}
	})
}
