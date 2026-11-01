package agent

// CHECK-03/16 / D4 S-26/S-28: a real file tool runs, rejection can occur
// before or after it, catalog refresh keeps memory, and a new loop/store
// retries effort after restart. Only the HTTP provider and a timeout edge are
// scripted; routing, execution, records, persistence and reload are production.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_P2_S26_ActualToolRejectionRefreshAndNewProcessState(t *testing.T) {
	for _, where := range []string{"first-request", "after-tool"} {
		t.Run(where, func(t *testing.T) {
			installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
			var mu sync.Mutex
			requests := []map[string]any{}
			toolServed := false
			var probe string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("request JSON: %v", err)
					http.Error(w, "bad request", 400)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, body)
				hasTool := false
				for _, raw := range body["messages"].([]any) {
					m := raw.(map[string]any)
					if m["role"] == "tool" {
						hasTool = true
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if bodyHasEffort(body) && (where == "first-request" || hasTool) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"code":"400","message":"effort-rejection-7731"}}`))
					return
				}
				if !toolServed {
					toolServed = true
					args, err := json.Marshal(map[string]any{"path": probe})
					if err != nil {
						t.Errorf("tool fixture: %v", err)
						return
					}
					response := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "read-7731", "type": "function", "function": map[string]any{"name": "read_file", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Errorf("tool response: %v", err)
					}
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			}))
			defer peer.Close()
			al, store := fullEffortRequestLoop(t, peer.URL, "high", nil)
			// Ordinary user data lives outside OMNIPUS_HOME, whose whole tree
			// is a protected internal carve-out even when read_file is allowed.
			probe = filepath.Join(t.TempDir(), "effort-tool-probe.txt")
			require.NoError(t, os.MkdirAll(filepath.Dir(probe), 0700))
			require.NoError(t, os.WriteFile(probe, []byte("ACTUAL-FILE-TOOL-RESULT-7731"), 0600))
			ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			ag.Tools.RegisterReplacing(tools.NewReadFileTool(al.GetConfig().Agents.Defaults.Home, false, 0))
			snapshot := func() []map[string]any {
				mu.Lock()
				defer mu.Unlock()
				return append([]map[string]any(nil), requests...)
			}
			answer, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("read the probe and answer"))
			require.NoError(t, err)
			require.Equal(t, "ok", answer)
			first := snapshot()
			toolResult := false
			for _, body := range first {
				for _, raw := range body["messages"].([]any) {
					m := raw.(map[string]any)
					if m["role"] == "tool" {
						// Compatible APIs permit either a text scalar or text
						// blocks. The marker exists only in the actual file, not
						// in the scripted model's answer or tool-call arguments.
						content, err := json.Marshal(m["content"])
						require.NoError(t, err)
						if strings.Contains(string(content), "ACTUAL-FILE-TOOL-RESULT-7731") {
							toolResult = true
						}
					}
				}
			}
			require.True(t, toolResult, "the actual filesystem tool executed, not just a serialized fake history")
			t.Log("positive control: actual read_file result reached the continuation request")
			require.Equal(t, 3, len(first), "one real tool round plus the rejected attempt and exactly one resend")
			initial, ok := fullEffortAnswer(t, store)["effort"].(map[string]any)
			require.True(t, ok, "completed answering entry has its rejection record")
			assert.Equal(t, "rejected", initial["reason"])
			assert.NotContains(t, initial, "sent")
			for _, phase := range []string{"later-request", "after-catalog-refresh"} {
				if phase == "after-catalog-refresh" {
					installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
				}
				before := len(snapshot())
				answer, _, err = al.processMessageAsPrimaryForTest(context.Background(), containmentTurn(phase))
				require.NoError(t, err)
				require.Equal(t, "ok", answer)
				later := snapshot()
				require.Len(t, later, before+1)
				assert.False(t, bodyHasEffort(later[before]), "same pair stays rejected in process, including after catalog refresh")
				assert.Equal(t, map[string]any{"chosen": "high", "reason": "rejected_earlier", "provider": "openrouter", "model": "primary-m"}, fullEffortAnswer(t, store)["effort"])
			}
			cfg := al.GetConfig()
			al.Stop()
			al.Close()
			freshStore, err := session.NewUnifiedStore(store.BaseDir())
			require.NoError(t, err)
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			fresh := mustNewAgentLoop(t, cfg, msgBus, &effortScriptedProvider{content: "UNEXPECTED-FALLBACK", finish: "stop"})
			fresh.sharedSessionStore = freshStore
			t.Cleanup(fresh.Close)
			before := len(snapshot())
			answer, _, err = fresh.processMessageAsPrimaryForTest(context.Background(), containmentTurn("after real loop/store restart"))
			require.NoError(t, err)
			require.Equal(t, "ok", answer)
			after := snapshot()
			require.Len(t, after, before+2, "new process state tries effort again and retries once")
			assert.True(t, bodyHasEffort(after[before]))
			assert.False(t, bodyHasEffort(after[before+1]))
		})
	}
}

func TestT_P2_S28_TimeoutWithEffortHasNoEffortResend(t *testing.T) {
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
	var mu sync.Mutex
	requests := []map[string]any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request JSON: %v", err)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		<-r.Context().Done()
	}))
	defer peer.Close()
	f := newRejectionFixture(t, peer.URL, "", "", "high")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	f.rt.turnCtx = ctx
	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.ErrorContains(t, err, "context deadline exceeded")
	mu.Lock()
	got := append([]map[string]any(nil), requests...)
	mu.Unlock()
	require.Len(t, got, 1, "timeout does not trigger the effort-specific retry")
	assert.True(t, bodyHasEffort(got[0]), "positive control: timed-out request actually carried effort")
}
