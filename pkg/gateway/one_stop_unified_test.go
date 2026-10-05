// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// one_stop_unified_test.go pins the gateway side of the founder's one-stop
// decision (2026-10-05, coordination/LANE-A-ONE-STOP-DECISION-20261005.md):
//
//	"there should be only one stop mechanism that works for sessions, only it
//	 can be triggered by agents and humans" ... "remove the 5 seconds and make
//	 3 seconds everywhere".
//
// Contract: every entry (web Stop/scope frames, /stop, /cancel, REST DELETE of
// a session, the agent's delegate stop_all) runs the SAME stop: the polite
// stop is requested immediately and the forced stop fires 3 s later. The
// immediate-hard REST path is gone.
//
// Oracle: the decision file above and the team-lead rulings quoted in the
// brief, never the current implementation. The instrument is a real tool call
// parked inside a real helper turn: the graceful phase of a stop does NOT
// reach a running tool (only the forced stop cancels its context -- see the
// header of pkg/agent/hard_abort_finish_method_test.go), so the moment the
// tool's context is cancelled is the moment the FORCED stop happened.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const oneStopProbeToolName = "one_stop_probe"

// oneStopProbeTool parks inside Execute until its context is cancelled and
// records WHEN that happened.
type oneStopProbeTool struct {
	started   chan struct{}
	startOnce sync.Once
	cancelled chan struct{}
	cancelAt  atomic.Int64 // unix nanos of ctx cancellation, 0 = never
}

func newOneStopProbeTool() *oneStopProbeTool {
	return &oneStopProbeTool{started: make(chan struct{}), cancelled: make(chan struct{})}
}

func (p *oneStopProbeTool) Name() string { return oneStopProbeToolName }
func (p *oneStopProbeTool) Description() string {
	return "parks until its context is cancelled (stop timing probe)"
}
func (p *oneStopProbeTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (p *oneStopProbeTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (p *oneStopProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (p *oneStopProbeTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	p.startOnce.Do(func() { close(p.started) })
	<-ctx.Done()
	p.cancelAt.Store(time.Now().UnixNano())
	close(p.cancelled)
	return tools.ErrorResult(ctx.Err().Error()).WithError(ctx.Err())
}

// cancelledBy reports whether the probe's context was cancelled within d of since.
func (p *oneStopProbeTool) cancelledBy(since time.Time, d time.Duration) bool {
	at := p.cancelAt.Load()
	return at != 0 && time.Unix(0, at).Sub(since) <= d
}

func (p *oneStopProbeTool) cancelDelay(since time.Time) time.Duration {
	at := p.cancelAt.Load()
	if at == 0 {
		return time.Hour
	}
	return time.Unix(0, at).Sub(since)
}

// oneStopProbeProvider asks for the probe tool on every request that is not a
// reply to a tool result, and parks (honouring ctx) on any later request.
type oneStopProbeProvider struct{ release chan struct{} }

func (oneStopProbeProvider) GetDefaultModel() string { return "one-stop-probe" }

func (p oneStopProbeProvider) Chat(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
			return nil, context.Canceled
		}
	}
	fc := providers.FunctionCall{Name: oneStopProbeToolName, Arguments: "{}"}
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{ID: "call_one_stop", Type: "function", Name: oneStopProbeToolName, Function: &fc}},
	}, nil
}

type oneStopGatewayFixture struct {
	al        *agent.AgentLoop
	lifecycle *session.LifecycleStore
	probe     *oneStopProbeTool
	rootID    string
	childID   string
}

// newOneStopGatewayFixture builds a real chat root with one real running
// helper whose turn is parked inside the probe tool.
func newOneStopGatewayFixture(t *testing.T) *oneStopGatewayFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "one-stop-probe"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: home}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 2, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := oneStopProbeProvider{release: make(chan struct{})}
	t.Cleanup(func() { close(provider.release) })
	al := mustAgentLoop(t, cfg, msgBus, provider)

	boot := session.NewBootEpochStore(home)
	epoch, err := boot.Mint()
	require.NoError(t, err, "SETUP mint a genuine boot epoch")
	require.NotZero(t, epoch)
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	al.SetSteerAudienceDeps(
		agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	probe := newOneStopProbeTool()
	al.RegisterTool(probe)
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "SETUP: agent mia must be registered")
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{oneStopProbeToolName: "allow"}})

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(testHarnessWorkspaceMembershipID)}))
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: "mia", Origin: &session.Origin{Kind: session.OriginKindChat},
	}))

	launcher := agent.NewSteerLauncher(al)
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID, TargetAgentID: "mia", Task: "park inside the probe tool until stopped",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate},
	})
	require.NoError(t, err, "SETUP: launch a real helper under the chat root")
	dispatched, err := launcher.Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State, "SETUP: the helper must actually run")
	select {
	case <-probe.started:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("SETUP: the helper never entered the probe tool; the timing instrument would prove nothing")
	}
	require.NoError(t, probeNotCancelled(probe), "SETUP: the probe must not be cancelled before any stop")
	return &oneStopGatewayFixture{al: al, lifecycle: lifecycle, probe: probe, rootID: meta.ID, childID: launched.SessionID}
}

func probeNotCancelled(p *oneStopProbeTool) error {
	select {
	case <-p.cancelled:
		return assert.AnError
	default:
		return nil
	}
}

// requireForcedAtThreeSeconds asserts the shared timeline: the parked tool is
// NOT cancelled while only the polite stop has run, and IS cancelled by the
// forced stop at ~3 s (never immediately, never at 5 s).
func requireForcedAtThreeSeconds(t *testing.T, probe *oneStopProbeTool, since time.Time, entry string) {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	require.False(t, probe.cancelledBy(since, 1500*time.Millisecond),
		"%s: the parked tool's context was cancelled within ~1.5s of the stop -- that is an immediate FORCED stop; "+
			"the one stop method asks politely first and forces only at 3s", entry)
	select {
	case <-probe.cancelled:
	case <-time.After(4500 * time.Millisecond):
		t.Fatalf("%s: the forced stop never cancelled the parked tool within 6s of the stop; it must fire at 3s", entry)
	}
	delay := probe.cancelDelay(since)
	assert.GreaterOrEqual(t, delay, 2500*time.Millisecond, "%s: forced stop fired at %v, before the 3s mark", entry, delay)
	assert.LessOrEqual(t, delay, 4200*time.Millisecond, "%s: forced stop fired at %v; the unified timeline is 3s, not 5s", entry, delay)
}

// TestOneStop_RestDeleteSession_PoliteThenForcedAtThreeSeconds pins the REST
// entry: DELETE /sessions/{id} of a chat that has a running helper goes
// through the same polite-then-3s stop as every other entry. The retired
// CancelSubtree path was an immediate hard abort.
func TestOneStop_RestDeleteSession_PoliteThenForcedAtThreeSeconds(t *testing.T) {
	f := newOneStopGatewayFixture(t)
	api := &restAPI{agentLoop: f.al, allowedOrigin: "http://localhost:3000", homePath: config.OmnipusHomeDir()}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+f.rootID, nil)
	r.URL.Path = "/api/v1/sessions/" + f.rootID
	since := time.Now()
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "DELETE of a chat whose helper is stopping must succeed; body=%s", w.Body.String())

	requireForcedAtThreeSeconds(t, f.probe, since, "REST DELETE session")

	// The owning execution lands the helper stopped after its tail retired:
	// fence cleared, note kept, not terminal (D2). Nothing else lands it.
	require.Eventually(t, func() bool {
		rec, err := f.lifecycle.Load(f.childID)
		return err == nil && rec.State == session.LifecycleStopped && rec.Stop == nil && rec.StopNote != nil
	}, 10*time.Second, 50*time.Millisecond,
		"the helper stopped by REST DELETE must land stopped (fence cleared, StopNote kept) once its running work shut down")
	rec, err := f.lifecycle.Load(f.childID)
	require.NoError(t, err)
	assert.False(t, rec.Terminal(), "a stop is never terminal")
	assert.Equal(t, session.StopCauseCascade, rec.StopNote.Cause, "a helper reached through its parent's stop-all carries cause cascade")
}

// TestOneStop_WebStopAllFrame_SameTimelineAsRestDelete pins that the web
// Stop-all (scope tree) frame produces the identical observable sequence as
// the REST entry: same fixture, same timeline.
func TestOneStop_WebStopAllFrame_SameTimelineAsRestDelete(t *testing.T) {
	f := newOneStopGatewayFixture(t)
	h := makeMinimalHandler()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	h.agentLoop, h.msgBus = f.al, msgBus
	wc, _ := makeForwarderTestConn(64)
	wc.userID = "one-stop-human"
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "one-stop-chat"}

	tree := "tree"
	data, err := json.Marshal(generated.CancelFrame{
		Type: string(generated.WsFrameTypeCancel), SessionId: f.rootID, Scope: &tree,
	})
	require.NoError(t, err)
	since := time.Now()
	require.Equal(t, wsHandlerReadLoopNext,
		reader.dispatchFrame(data, wsTypeOnly{Type: string(generated.WsFrameTypeCancel)}),
		"drive the production CancelFrame decoder/dispatcher, not an internal handler")
	requireForcedAtThreeSeconds(t, f.probe, since, "web Stop all frame")
}
