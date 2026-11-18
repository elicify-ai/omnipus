package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Network edge only is scripted. Effort input goes through config decoding;
// usage goes through the real provider decoder, turn accounting and streamer.
func egwRealEffortDone(t *testing.T) map[string]any {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request decode: %v", err)
		}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"REAL-ANSWER-7731\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":180,\"total_tokens\":190,\"completion_tokens_details\":{\"reasoning_tokens\":120}}}\n\ndata: [DONE]\n\n"))
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"REAL-ANSWER-7731"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":120}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	var cfg config.Config
	raw, err := json.Marshal(map[string]any{"version": 1, "agents": map[string]any{"defaults": map[string]any{"home": filepath.Join(home, "workspace"), "default_model": map[string]any{"provider": "openrouter", "model": "m-levels", "reasoning_effort": "high"}, "max_tokens": 4096, "max_tool_iterations": 3}}, "providers": []any{map[string]any{"provider": "openrouter", "model": "m-levels", "api_base": srv.URL, "api_key": "test"}}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &cfg))
	window := 128000
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", ContextWindowOverride: &window}}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	previous := providers.ProviderCatalog()
	providers.SetCatalog(egwEffortCatalog(t))
	t.Cleanup(func() { providers.SetCatalog(previous) })
	seedAgentEntities(t, home, cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, &cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	handler.webchatCh = newWebchatChannel(handler)
	msgBus.SetStreamDelegate(handler)
	t.Cleanup(handler.Wait)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, Origin: &session.Origin{Kind: session.OriginKindChat}, AgentID: "mia", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerScopeKind: session.OwnerScopeHuman}))
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 256)
	t.Cleanup(wc.close)
	bindTestConnToSession(handler, "chat-effort-real", meta.ID, wc)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()
	t.Cleanup(func() { stop(); require.NoError(t, <-runDone) })
	handler.handleChatMessage(ctx, "chat-effort-real", meta.ID, "answer once", "mia", nil, "", "", wc)
	select {
	case final := <-msgBus.OutboundChan():
		require.Equal(t, "REAL-ANSWER-7731", final.Content, "positive control: actual provider completed the turn")
	case <-ctx.Done():
		t.Fatal("provider-backed web turn never completed")
	}
	for {
		select {
		case frame := <-wc.sendCh:
			var m map[string]any
			require.NoError(t, json.Unmarshal(frame, &m))
			if m["type"] == "done" && m["message_id"] != nil {
				return m
			}
		case <-ctx.Done():
			t.Fatal("real answered turn emitted no live done frame")
			return nil
		}
	}
}

func TestT_GW_RealTurnDisplaysReportedReasoningSnapshot(t *testing.T) {
	done := egwRealEffortDone(t)
	stats, ok := done["stats"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(120), stats["reasoning_tokens"], "display snapshot is the reported 120, never 240")
}
