package gateway

// CHECK-06/12 / D5 rows 1-5,8: drive actual WebSocket ingress, fallback-loop
// routing, adapter serialization and completion. Expected pairs/levels are
// fixed from S-15/S-75; duplicate model names make a lost provider detectable.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_GW_S15_S75_RealFrameRoutesExactPairAndMapsToServingModel(t *testing.T) {
	rows := []struct {
		name                                string
		metadata                            map[string]any
		wantProvider, wantModel, wantEffort string
		refuse                              bool
	}{
		{"model only", map[string]any{"model_name": "B"}, "openai", "B", "medium", false},
		{"exact pair", map[string]any{"provider": "openrouter", "model_name": "B"}, "openrouter", "B", "high", false},
		{"pair plus effort", map[string]any{"provider": "openai", "model_name": "B", "reasoning_effort": "high"}, "openai", "B", "medium", false},
		{"model plus effort", map[string]any{"model_name": "B", "reasoning_effort": "high"}, "openai", "B", "medium", false},
		{"effort only", map[string]any{"reasoning_effort": "medium"}, "openrouter", "A", "medium", false},
		{"unknown provider", map[string]any{"provider": "unconfigured-provider-7731", "model_name": "B", "reasoning_effort": "high"}, "", "", "", true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var mu sync.Mutex
			seen := []map[string]any{}
			peer := func(provider string) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("request JSON: %v", err)
						http.Error(w, "bad JSON", 400)
						return
					}
					mu.Lock()
					seen = append(seen, map[string]any{"provider": provider, "body": body})
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if body["stream"] == true {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ANSWER-%s\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", provider)
					} else {
						fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"ANSWER-%s"},"finish_reason":"stop"}]}`, provider)
					}
				}))
			}
			pa, pb := peer("openrouter"), peer("openai")
			defer pa.Close()
			defer pb.Close()
			cfg := effortProductionConfig(t, pa.URL, pb.URL)
			cfg.Gateway.DevModeBypass = true
			t.Setenv("OMNIPUS_BEARER_TOKEN", "")
			// S-75's Given requires today's bare-name resolution to be unique.
			// Only the explicit-pair rows introduce a duplicate; otherwise a
			// deliberately ambiguous name would be an invalid positive fixture.
			duplicate := row.name == "exact pair" || row.name == "pair plus effort"
			if duplicate {
				cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: "B", APIBase: pa.URL})
			}
			model := func(id string, levels []string) map[string]any {
				return map[string]any{"id": id, "name": id, "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active", "reasoning": map[string]any{"levels": levels, "switch": false}}
			}
			provider := func(id string, models []any) map[string]any {
				return map[string]any{"id": id, "name": id, "company": id, "api": "https://example.invalid/v1", "protocol": "openai-compatible", "protocols": []any{map[string]any{"protocol": "openai-compatible", "api": "https://example.invalid/v1"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": models}
			}
			paModels := []any{model("A", []string{"low", "medium", "high"})}
			if duplicate {
				paModels = append(paModels, model("B", []string{"low", "high"}))
			}
			doc, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{provider("openrouter", paModels), provider("openai", []any{model("B", []string{"low", "medium"})})}})
			require.NoError(t, err)
			cat, err := catalog.NewCatalog(doc)
			require.NoError(t, err)
			providers.SetCatalog(cat)
			f := newEffortProductionTurn(t, cfg)
			t.Cleanup(f.handler.Wait)
			srv := httptest.NewServer(f.handler)
			t.Cleanup(srv.Close)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { _ = conn.Close() })
			sendWSAuthFrameDevMode(t, conn)
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
			frame := map[string]any{"type": "message", "agent_id": "mia", "session_id": f.sid, "content": "pair probe 7731", "metadata": row.metadata}
			require.NoError(t, conn.WriteJSON(frame))
			var done map[string]any
			for {
				var got map[string]any
				require.NoError(t, conn.ReadJSON(&got), "actual WS turn/error must finish")
				if got["type"] == "error" {
					require.True(t, row.refuse, "unexpected ingress refusal: %v", got)
					break
				}
				if got["type"] == "done" && got["message_id"] != nil {
					done = got
					break
				}
			}
			mu.Lock()
			calls := append([]map[string]any(nil), seen...)
			mu.Unlock()
			if row.refuse {
				require.Nil(t, done)
				require.Empty(t, calls, "unknown explicit pair must never borrow a configured provider")
				return
			}
			require.NotNil(t, done)
			require.Len(t, calls, 1, "one serving attempt, no hidden fallback")
			assert.Equal(t, row.wantProvider, calls[0]["provider"])
			body := calls[0]["body"].(map[string]any)
			assert.Equal(t, row.wantModel, body["model"])
			if row.wantProvider == "openrouter" {
				assert.Equal(t, map[string]any{"effort": row.wantEffort}, body["reasoning"])
				assert.NotContains(t, body, "reasoning_effort")
			} else {
				assert.Equal(t, row.wantEffort, body["reasoning_effort"])
				assert.NotContains(t, body, "reasoning")
			}
			stats, ok := done["stats"].(map[string]any)
			require.True(t, ok)
			chosen := "high"
			if row.name == "effort only" {
				chosen = "medium"
			}
			want := map[string]any{"chosen": chosen, "sent": row.wantEffort, "provider": row.wantProvider, "model": row.wantModel}
			if chosen != row.wantEffort {
				want["reason"] = "mapped"
			}
			assert.Equal(t, want, stats["effort"], "record is about the exact serving pair and its mapped value")
		})
	}
}
