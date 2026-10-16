package anthropicprovider

// CHECK gap-closing test (test-integrity-audit AUDIT mode, 2026-10-16).
// Written because the "Moonshot gets a cache mark via the provider-id dialect
// switch" mutation SURVIVED the pack: TestDialectBodies_MoonshotAndMiniMax
// builds its provider without WithProviderID, so only cacheLifetime's
// base-URL hostname branch was under test. The factory pins the catalog
// provider id on every Anthropic-protocol provider
// (pkg/providers/factory_provider.go, ProtocolAnthropic branch), so the
// provider-id branch is the live production path. FR-005/D19: Moonshot sends
// no mark and no 1-hour option; MiniMax uses its documented 5-minute or
// automatic mode and no 1-hour mark.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func captureProviderIDBody(t *testing.T, providerID string, messages []Message, tools []ToolDefinition, model string) []byte {
	t.Helper()
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","stop_sequence":null,"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProviderWithBaseURL("token", "https://catalog-row.example/anthropic").WithProviderID(providerID)
	client := anthropic.NewClient(
		option.WithAuthToken("token"),
		option.WithBaseURL(p.BaseURL()),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: rewriteHost{base: base}}),
	)
	p.client = &client
	if _, err := p.Chat(t.Context(), messages, tools, model, map[string]any{"max_tokens": 64}); err != nil {
		t.Fatalf("official connector Chat: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("official connector sent no body")
	}
	return raw
}

func TestDialectBodies_ProviderIDSelectsDialect(t *testing.T) {
	messages, tools := nativeMessages()
	messages[0].SystemParts[0].CacheControl = &protocoltypes.CacheControl{Type: "ephemeral"}

	t.Run("moonshot by provider id carries no cache field", func(t *testing.T) {
		raw := captureProviderIDBody(t, "moonshotai", messages, tools, "kimi-k2")
		if err := moonshotCacheBodyError(parseCacheBody(t, raw)); err != nil {
			t.Fatalf("Moonshot FR-005/028 via provider id: %v\n%s", err, raw)
		}
	})

	t.Run("minimax by provider id has no one-hour mark", func(t *testing.T) {
		raw := captureProviderIDBody(t, "minimax", messages, tools, "MiniMax-M2.7")
		if err := miniMaxCacheBodyError(parseCacheBody(t, raw)); err != nil {
			t.Fatalf("MiniMax FR-005 via provider id: %v\n%s", err, raw)
		}
		var body cacheBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		for _, mark := range cacheMarks(body) {
			if mark.Control.TTL == "1h" {
				t.Fatalf("MiniMax received a 1-hour mark at %+v; FR-005 forbids it", mark)
			}
		}
	})

	t.Run("anthropic by provider id keeps its documented marks", func(t *testing.T) {
		native, nativeTools := nativeMessages()
		raw := captureProviderIDBody(t, "anthropic", native, nativeTools, "claude-sonnet-4-5")
		if err := nativeCacheBodyError(parseCacheBody(t, raw)); err != nil {
			t.Fatalf("native Anthropic via provider id: %v\n%s", err, raw)
		}
	})
}
