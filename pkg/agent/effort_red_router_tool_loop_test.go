package agent

// CHECK-07 / S-30/S-31/S-32/S-33: real turn projection, two real read_file
// calls, durable close/reopen, and adapter serialization across routes. Unlike
// the codec-boundary fixtures, this test never copies a carrier into Message.
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

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertRouterLoopCarriers(t *testing.T, body map[string]any, want []any, routed bool) {
	t.Helper()
	messages, ok := body["messages"].([]any)
	require.True(t, ok, "positive control: request contains real conversation messages")
	calls, results := 0, 0
	for _, value := range messages {
		m := value.(map[string]any)
		if m["role"] == "assistant" && m["tool_calls"] != nil {
			calls++
			if routed {
				assert.Equal(t, want, m["reasoning_details"], "engine projection and durable codec preserve every opaque/unknown member")
			} else {
				assert.NotContains(t, m, "reasoning_details", "populated OpenRouter carrier is private to that route")
			}
		}
		if m["role"] == "tool" {
			results++
			content, err := json.Marshal(m["content"])
			require.NoError(t, err)
			assert.Contains(t, string(content), "ROUTER-ACTUAL-FILE-7731", "actual tool result, including structured text blocks, not a fabricated tool message")
		}
	}
	assert.Equal(t, 2, calls, "both actual tool calls remain visible; nil serialization cannot pass")
	assert.Equal(t, 2, results)
}

func TestT_C6_S30_S31_S32_ActualRouterToolLoopAndDurableRestart(t *testing.T) {
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
	probe := filepath.Join(t.TempDir(), "router-user-file.txt")
	require.NoError(t, os.WriteFile(probe, []byte("ROUTER-ACTUAL-FILE-7731"), 0600))
	want := []any{
		map[string]any{"index": float64(0), "type": "reasoning.text", "text": "step one"},
		map[string]any{"index": float64(1), "type": "reasoning.encrypted", "format": "opaque/v1", "id": "rs_7731", "signature": "SIG-7731", "data": "OPAQUE-7731", "vendor_extra": map[string]any{"keep": []any{float64(1), float64(2)}}},
	}
	var mu sync.Mutex
	bodies := []map[string]any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("router request: %v", err)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		message := map[string]any{"role": "assistant", "content": "ok"}
		finish := "stop"
		if n <= 2 {
			args, err := json.Marshal(map[string]any{"path": probe})
			if err != nil {
				t.Errorf("file args: %v", err)
				return
			}
			message = map[string]any{"role": "assistant", "content": "", "reasoning_details": want, "tool_calls": []any{map[string]any{"id": fmt.Sprintf("router-read-%d", n), "type": "function", "function": map[string]any{"name": "read_file", "arguments": string(args)}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}}); err != nil {
			t.Errorf("router response: %v", err)
		}
	}))
	defer peer.Close()
	al, store := fullEffortRequestLoop(t, peer.URL, "high", nil)
	ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	ag.Tools.RegisterReplacing(tools.NewReadFileTool(filepath.Dir(probe), false, 0))
	answer, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("read the actual file twice, then answer"))
	require.NoError(t, err)
	require.Equal(t, "ok", answer)
	mu.Lock()
	captured := append([]map[string]any(nil), bodies...)
	mu.Unlock()
	require.Len(t, captured, 3)
	assertRouterLoopCarriers(t, captured[2], want, true)

	modelStore, ok := ag.Sessions.(*session.UnifiedStore)
	require.True(t, ok, "actual agent model-history store is durable")
	root, id := modelStore.BaseDir(), containmentTurn("").SessionKey
	require.NotEmpty(t, modelStore.GetHistory(id), "instrument control: address the actual model history before closing it")
	al.Close()
	require.NoError(t, modelStore.Close())
	require.NoError(t, store.Close())
	reopened, err := session.NewUnifiedStore(root)
	require.NoError(t, err)
	defer reopened.Close()
	history := reopened.GetHistory(id)
	require.NotEmpty(t, history)
	for _, provider := range []string{"openai", "openrouter"} {
		adapter, err := openai_compat.NewProvider("test-placeholder", peer.URL, "", openai_compat.WithProviderID(provider))
		require.NoError(t, err)
		response, err := adapter.Chat(context.Background(), history, []providers.ToolDefinition{}, "primary-m", nil)
		require.NoError(t, err)
		require.Equal(t, "ok", response.Content)
		mu.Lock()
		body := bodies[len(bodies)-1]
		mu.Unlock()
		assertRouterLoopCarriers(t, body, want, provider == "openrouter")
	}
}
