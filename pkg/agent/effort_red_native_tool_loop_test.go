package agent

// CHECK-07 / S-34/S-90: real native adapter, execution loop and read_file.
// Only the provider HTTP edge is scripted. Off is checked on BOTH actual
// requests; High/On are guarded only inside the active tool loop. The next
// user turn is a positive control against an over-broad historical guard.
import (
	"context"
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
	anthropic "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nativeGuardReply(t *testing.T, w http.ResponseWriter, stream, tool bool, path string) {
	t.Helper()
	if !stream {
		content := []any{map[string]any{"type": "text", "text": "ok"}}
		stop := "end_turn"
		if tool {
			content = []any{map[string]any{"type": "tool_use", "id": "native-read-7731", "name": "read_file", "input": map[string]any{"path": path}}}
			stop = "tool_use"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"id": "native-message-7731", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6", "content": content, "stop_reason": stop, "usage": map[string]any{"input_tokens": 2, "output_tokens": 3}}); err != nil {
			t.Errorf("native response: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"native-message-7731\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n")
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
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
	}
	fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", stop)
}

func TestT_P5_S34_S90_RealNativeToolExecutionRecordsAndNextLoop(t *testing.T) {
	// The actual WS-streaming writer is exercised separately by
	// gateway.TestT_GW_S34_S90_StreamingNativeToolLoop, not by a no-op
	// Streamer that would silently replace production persistence.
	for _, stream := range []bool{false} {
		for _, choice := range []string{"off", "high", "on"} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, choice), func(t *testing.T) {
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
					nativeGuardReply(t, w, body["stream"] == true, n == 1, path)
				}))
				defer peer.Close()
				home := t.TempDir()
				t.Setenv("OMNIPUS_HOME", home)
				// The entity codec, not retired agents.list JSON, loads the given choice.
				raw, err := json.Marshal(map[string]any{"id": testDefaultAgentID, "model": "claude-sonnet-4-6", "provider": "anthropic", "reasoning_effort": choice})
				require.NoError(t, err)
				var entity config.AgentConfig
				require.NoError(t, json.Unmarshal(raw, &entity))
				window := 128000
				entity.ContextWindowOverride = &window
				cfg := &config.Config{}
				cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
				cfg.Agents.List = []config.AgentConfig{entity}
				cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
				// A real catalog supplies both levels and a switch on the native route.
				installNativeGuardCatalog(t)
				provider := anthropic.NewProviderWithBaseURL("test-native-key", peer.URL)
				al, store, _ := newContainmentLoop(t, cfg, provider)
				ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
				require.True(t, ok)
				ag.Tools.RegisterReplacing(tools.NewReadFileTool(filepath.Dir(path), false, 0))
				response, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("read the real file, then answer"))
				require.NoError(t, err)
				require.Equal(t, "ok", response)
				mu.Lock()
				first := append([]map[string]any(nil), requests...)
				mu.Unlock()
				require.Len(t, first, 2, "actual file tool produced a second provider call")
				secondJSON, err := json.Marshal(first[1]["messages"])
				require.NoError(t, err)
				require.Contains(t, string(secondJSON), "NATIVE-REAL-TOOL-RESULT-7731", "actual file contents reached the real adapter continuation")
				for _, body := range first {
					assert.Equal(t, stream, body["stream"] == true, "the requested streaming/nonstreaming path was really used")
				}
				expected := map[string]any{"chosen": choice, "provider": "anthropic", "model": "claude-sonnet-4-6"}
				if choice == "off" {
					for _, body := range first {
						assert.Equal(t, map[string]any{"type": "disabled"}, body["thinking"], "S-90 explicit Off survives each actual request")
						assert.NotContains(t, body, "output_config")
					}
					expected["sent"] = "off"
				} else {
					assert.Equal(t, map[string]any{"type": "adaptive"}, first[0]["thinking"], "initial enabling positive control")
					assert.NotContains(t, first[1], "thinking", "only the current tool loop lacks route-owned blocks")
					assert.NotContains(t, first[1], "output_config")
					expected["reason"] = "continuation"
				}
				assert.Equal(t, expected, fullEffortAnswer(t, store)["effort"], "loop-generated effort record, not an adapter-only assertion")
				_, _, err = al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("new user turn after completed tool loop"))
				require.NoError(t, err)
				mu.Lock()
				later := append([]map[string]any(nil), requests...)
				mu.Unlock()
				require.Len(t, later, 3)
				if choice == "off" {
					assert.Equal(t, map[string]any{"type": "disabled"}, later[2]["thinking"])
				} else {
					assert.Equal(t, map[string]any{"type": "adaptive"}, later[2]["thinking"], "earlier completed loop does not suppress effort on a new user turn")
				}
			})
		}
	}
}

func installNativeGuardCatalog(t *testing.T) {
	t.Helper()
	// Actual native capability row, parsed rather than mocked.
	old := providers.ProviderCatalog()
	raw, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{map[string]any{"id": "anthropic", "name": "Anthropic", "company": "Anthropic", "api": "https://api.anthropic.com", "protocol": "anthropic", "protocols": []any{map[string]any{"protocol": "anthropic", "api": "https://api.anthropic.com"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": []any{map[string]any{"id": "claude-sonnet-4-6", "name": "claude-sonnet-4-6", "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active", "reasoning": map[string]any{"levels": []string{"low", "medium", "high"}, "switch": true}}}}}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(old) })
}
