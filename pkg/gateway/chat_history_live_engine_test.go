package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

// Only the external model transport is a fake. The actual turn engine, session,
// Stop machinery, hub and attach state are real. No model/network charge.
type pagingBlockedProvider struct {
	started, canceled     chan struct{}
	startOnce, cancelOnce sync.Once
	calls                 atomic.Int64
}

func (p *pagingBlockedProvider) GetDefaultModel() string { return "test-model" }
func (p *pagingBlockedProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	p.startOnce.Do(func() { close(p.started) })
	<-ctx.Done()
	p.cancelOnce.Do(func() { close(p.canceled) })
	return nil, ctx.Err()
}

func pagingLiveOnlyEngineStop(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Gateway: config.GatewayConfig{DevModeBypass: true}, Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096}}}
	seedTestAgents(cfg)
	messages := bus.NewMessageBus()
	t.Cleanup(messages.Close)
	provider := &pagingBlockedProvider{started: make(chan struct{}), canceled: make(chan struct{})}
	al := mustAgentLoop(t, cfg, messages, provider)
	boot := session.NewBootEpochStore(home)
	_, err := boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "test-lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "test-inbox")), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	s := al.GetSessionStore()
	meta, err := s.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	pagingSeed(t, s, meta.ID, "saved-before", 1)
	require.NoError(t, session.VerifyChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), meta.ID)), "positive control: valid index before deliberately removing it")
	require.NoError(t, os.RemoveAll(filepath.Join(s.BaseDir(), meta.ID, session.ChatHistoryIndexDirectory)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var turnErr error
	go func() {
		defer close(done)
		_, turnErr = al.ProcessScheduled(ctx, "mia", meta.ID, "Keep working until Stop", "webchat", "history-control-chat")
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if turnErr != nil {
				require.ErrorIs(t, turnErr, context.Canceled, "owned turn error must be the observed cancellation, not swallowed engine failure")
			}
		case <-time.After(15 * time.Second):
			t.Error("owned real turn did not join")
		}
	})
	select {
	case <-provider.started:
	case <-done:
		t.Fatal("real engine never reached the controlled external model call")
	case <-time.After(10 * time.Second):
		t.Fatal("real turn did not enter external model boundary")
	}
	h := newWSHandler(messages, al, "")
	t.Cleanup(h.Wait)
	f := &attachFixture{h: h, store: s, sid: meta.ID}
	wc := f.newConn("history-live-control-view")
	h.handleAttachSession(context.Background(), "history-live-control-view", meta.ID, nil, wc)
	frames := pagingDrain(t, wc)
	require.Equal(t, -1, indexOfType(frames, "session_snapshot"), "history error cannot wipe cached rows")
	complete := indexOfType(frames, "catch_up_complete")
	require.GreaterOrEqual(t, complete, 0)
	require.Equal(t, "live_only", frames[complete].Mode)
	stateIndex := indexOfType(frames, "session_state")
	require.GreaterOrEqual(t, stateIndex, 0)
	var state generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(frames[stateIndex].raw, &state))
	require.NotNil(t, state.HistoryState)
	require.Equal(t, "unavailable", state.HistoryState.Status)
	require.NotNil(t, state.ActiveTurn, "actual active turn remains exposed beside history failure")
	select {
	case <-provider.canceled:
		t.Fatal("history/viewer failure prematurely canceled real execution")
	default:
	}
	fired, _, err := al.RequestCancelForSession(context.Background(), meta.ID, "history-test-user", "web")
	require.NoError(t, err)
	require.True(t, fired, "existing person Stop path reaches this actual running turn")
	select {
	case <-provider.canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop never reached in-flight model execution")
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("stopped turn did not return")
	}
	require.EqualValues(t, 1, provider.calls.Load(), "history Retry/Stop must not relaunch or resend the request")
	api := &restAPI{agentLoop: al, homePath: home}
	pagingRefusal(t, pagingRequest(api, meta.ID, ""), 500, pagingUnavailable)
}
