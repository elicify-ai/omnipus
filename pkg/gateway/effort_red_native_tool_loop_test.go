package gateway

// CHECK-07 / S-34/S-90: actual gateway streamer and final-answer writer,
// native HTTP adapter and filesystem tool. No test Streamer writes entries.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nativeStreamToolReply(t *testing.T, w http.ResponseWriter, tool bool, path string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"native-ws-7731\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n")
	stop := "end_turn"
	if tool {
		args, err := json.Marshal(map[string]any{"path": path})
		if err != nil {
			t.Errorf("tool args: %v", err)
			return
		}
		delta, err := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)}})
		if err != nil {
			t.Errorf("tool delta: %v", err)
			return
		}
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"native-read-7731\",\"name\":\"read_file\",\"input\":{}}}\n\n")
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", delta)
		stop = "tool_use"
	} else {
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ANSWER-NATIVE-7731\"}}\n\n")
	}
	fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", stop)
}
func nativeStreamCatalog(t *testing.T) {
	t.Helper()
	model := map[string]any{"id": "claude-sonnet-4-6", "name": "claude-sonnet-4-6", "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active", "reasoning": map[string]any{"levels": []string{"low", "medium", "high"}, "switch": true}}
	provider := map[string]any{"id": "anthropic", "name": "Anthropic", "company": "Anthropic", "api": "https://api.anthropic.com", "protocol": "anthropic", "protocols": []any{map[string]any{"protocol": "anthropic", "api": "https://api.anthropic.com"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": []any{model}}
	raw, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{provider}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	old := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(old) })
}
func TestT_GW_S34_S90_StreamingNativeToolLoop(t *testing.T) {
	for _, choice := range []string{"off", "high", "on"} {
		t.Run(choice, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native-user-file.txt")
			require.NoError(t, os.WriteFile(path, []byte("NATIVE-REAL-TOOL-RESULT-7731"), 0600))
			var mu sync.Mutex
			requests := []map[string]any{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("native request: %v", err)
					return
				}
				mu.Lock()
				requests = append(requests, body)
				n := len(requests)
				mu.Unlock()
				nativeStreamToolReply(t, w, n == 1, path)
			}))
			defer peer.Close()
			// Versioned config's own provider codec retains its test-only API key.
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			raw, err := json.Marshal(map[string]any{"version": 1, "agents": map[string]any{"defaults": map[string]any{"home": filepath.Join(home, "workspace"), "max_tool_iterations": 4, "max_tokens": 4096}}, "providers": []any{map[string]any{"provider": "anthropic", "model": "claude-sonnet-4-6", "api_base": peer.URL, "api_key": "test-native-key"}}})
			require.NoError(t, err)
			var cfg config.Config
			require.NoError(t, json.Unmarshal(raw, &cfg))
			cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
			raw, err = json.Marshal(map[string]any{"id": "mia", "provider": "anthropic", "model": "claude-sonnet-4-6", "reasoning_effort": choice})
			require.NoError(t, err)
			var entity config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &entity))
			window := 128000
			entity.ContextWindowOverride = &window
			cfg.Agents.List = []config.AgentConfig{entity}
			nativeStreamCatalog(t)
			f := newEffortProductionTurn(t, &cfg)
			ag, ok := f.al.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			ag.Tools.RegisterReplacing(tools.NewReadFileTool(filepath.Dir(path), false, 0))
			done := f.turn(t, "read the actual native file, then answer", "")
			mu.Lock()
			first := append([]map[string]any(nil), requests...)
			mu.Unlock()
			require.Len(t, first, 2)
			for _, body := range first {
				require.Equal(t, true, body["stream"], "actual native streaming branch")
			}
			toolJSON, err := json.Marshal(first[1]["messages"])
			require.NoError(t, err)
			require.Contains(t, string(toolJSON), "NATIVE-REAL-TOOL-RESULT-7731", "actual file result, not a synthetic tool history")
			expected := map[string]any{"chosen": choice, "provider": "anthropic", "model": "claude-sonnet-4-6"}
			if choice == "off" {
				for _, body := range first {
					assert.Equal(t, map[string]any{"type": "disabled"}, body["thinking"])
					assert.NotContains(t, body, "output_config")
				}
				expected["sent"] = "off"
			} else {
				assert.Equal(t, map[string]any{"type": "adaptive"}, first[0]["thinking"], "enabling positive control")
				assert.NotContains(t, first[1], "thinking")
				assert.NotContains(t, first[1], "output_config")
				expected["reason"] = "continuation"
			}
			assert.Equal(t, expected, effortRequireType[map[string]any](t, done["stats"])["effort"])
			entries, err := f.al.GetSessionStore().ReadTranscript(f.sid)
			require.NoError(t, err)
			var saved map[string]any
			for _, entry := range entries {
				if entry.Content == "ANSWER-NATIVE-7731" {
					saved = effortPublicJSON(t, entry)
				}
			}
			require.NotNil(t, saved, "the real wsStreamer persisted its answer")
			assert.Equal(t, expected, saved["effort"])
			f.turn(t, "new user turn after native tool loop", "")
			mu.Lock()
			later := append([]map[string]any(nil), requests...)
			mu.Unlock()
			require.Len(t, later, 3)
			if choice == "off" {
				assert.Equal(t, map[string]any{"type": "disabled"}, later[2]["thinking"])
			} else {
				assert.Equal(t, map[string]any{"type": "adaptive"}, later[2]["thinking"], "an earlier completed loop cannot suppress the next turn")
			}
		})
	}
}
