// Package agent — ProcessScheduled tests (issue #264, W-1 / FR-001/003/006/009).
//
// These tests exercise the dedicated headless scheduled-run entry point:
//   - owner-pinned routing (runs the given owner, never the default);
//   - missing owner is a hard error (no default-agent fallback);
//   - the sessionActiveAgent handoff map is never read/written/deleted;
//   - the per-owner session key namespacing (agent:<owner>:session:<id>);
//   - ask-policy tool calls are auto-denied (never block for approval);
//   - a canceled (deadline) run returns a context-derived error promptly.
//
// All tests use scripted/blocking mock providers — no wall-clock sleeps.

package agent

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// panicProvider fails the test if Chat is ever called. Used to prove the
// default/other agent is NOT invoked during an owner-pinned scheduled run.
type panicProvider struct {
	t      *testing.T
	called atomic.Bool
}

func (p *panicProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	p.called.Store(true)
	p.t.Errorf("panicProvider.Chat was called — the wrong agent was invoked")
	return &providers.LLMResponse{Content: "WRONG AGENT"}, nil
}

func (p *panicProvider) GetDefaultModel() string { return "panic-model" }

// blockingProvider blocks inside Chat until the context is canceled, then
// returns ctx.Err(). It is used to prove a scheduled run registers a
// cancellable turn and returns promptly when the caller imposes a deadline /
// calls RequestCancel.
type blockingProvider struct {
	entered chan struct{}
	once    sync.Once
}

func (b *blockingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingProvider) GetDefaultModel() string { return "blocking-model" }

// autoApproveApprover approves every ask-policy request and records that it was
// consulted. In the auto-deny test it proves the auto-deny short-circuits the
// run BEFORE any approver is consulted: if AutoDenyAsk were ignored, this
// approver would approve and the stub tool would execute.
type autoApproveApprover struct{ consulted atomic.Bool }

func (a *autoApproveApprover) RequestApproval(context.Context, PolicyApprovalReq) (bool, string) {
	a.consulted.Store(true)
	return true, "auto-approved"
}

// snapshotHandoffMap returns a stable copy of the sessionActiveAgent map so a
// test can assert it is unchanged across a scheduled run.
func snapshotHandoffMap(al *AgentLoop) map[string]string {
	out := map[string]string{}
	al.sessionActiveAgent.Range(func(k, v any) bool {
		ks, _ := k.(string)
		vs, _ := v.(string)
		out[ks] = vs
		return true
	})
	return out
}

// schedTestLoop builds an AgentLoop with a shared session store wired so
// ResolveSessionStore finds scheduled sessions. The loop-level provider is a
// plain mockProvider; per-agent providers are supplied by registerAgent.
func schedTestLoop(t *testing.T) (*AgentLoop, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	return al, home
}

// registerAgent registers an agent with the given id, provider, and default flag
// into the loop's registry, returning the instance.
func registerAgent(
	t *testing.T,
	al *AgentLoop,
	home, id string,
	provider providers.LLMProvider,
	isDefault bool,
) *AgentInstance {
	t.Helper()
	cfg := al.cfg
	ag := NewAgentInstance(
		&config.AgentConfig{ID: id, Name: id, Default: isDefault},
		&cfg.Agents.Defaults, cfg, provider,
	)
	ag.Workspace = filepath.Join(home, "agents", id)
	ag.ContextBuilder = NewContextBuilder(ag.Workspace).WithAgentInfo(id, id)
	al.registry.mu.Lock()
	al.registry.agents[id] = ag
	al.registry.mu.Unlock()
	return ag
}

// TestProcessScheduled_RoutesToOwnerNotDefault asserts a scheduled run executes
// as the OWNING agent (mia) and the default agent (max) is never invoked.
// Traces to: FR-001, US1 AS1, SC-001.
func TestProcessScheduled_RoutesToOwnerNotDefault(t *testing.T) {
	al, home := schedTestLoop(t)

	miaProvider := testutil.NewScenario().WithText("mia handled it")
	registerAgent(t, al, home, "mia", miaProvider, false)

	maxProvider := &panicProvider{t: t}
	registerAgent(t, al, home, "max", maxProvider, true) // max is the DEFAULT

	// Sanity: max is the default agent.
	require.Equal(t, "max", al.GetRegistry().GetDefaultAgent().ID)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	reply, err := al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "do the thing", "scheduled", meta.ID,
	)
	require.NoError(t, err)
	assert.Equal(t, "mia handled it", reply)
	assert.Equal(t, 1, miaProvider.CallCount(), "the owner (mia) must run the turn")
	assert.False(t, maxProvider.called.Load(), "the default agent (max) must NOT be invoked")
}

// TestProcessScheduled_MissingOwner_NoDefaultFallback asserts that an unknown /
// disabled owner is a hard error and the default agent is NOT invoked.
// Traces to: FR-001, US1 AS2.
func TestProcessScheduled_MissingOwner_NoDefaultFallback(t *testing.T) {
	al, home := schedTestLoop(t)

	defProvider := &panicProvider{t: t}
	registerAgent(t, al, home, "max", defProvider, true) // default — must not run

	meta, err := al.GetSessionStore().NewScheduledSession("ghost")
	require.NoError(t, err)

	reply, err := al.ProcessScheduled(
		context.Background(), "ghost", meta.ID, "do the thing", "scheduled", meta.ID,
	)
	require.Error(t, err, "missing owner must be a hard error")
	assert.Contains(t, err.Error(), "owner unavailable")
	assert.Empty(t, reply)
	assert.False(t, defProvider.called.Load(), "no default-agent fallback is allowed")
}

// TestProcessScheduled_DoesNotTouchHandoffMap asserts a scheduled run never
// reads/writes/deletes the sessionActiveAgent handoff map — a scheduled run must
// not disturb a human's in-flight agent switch in that session.
// Traces to: W-1 (J-2), FR-001.
func TestProcessScheduled_DoesNotTouchHandoffMap(t *testing.T) {
	al, home := schedTestLoop(t)
	registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("ok"), false)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	// Seed a human handoff override on the SAME session id, as if a human
	// switched the active agent to "ray" in this session.
	handoffKey := "session:" + meta.ID
	al.sessionActiveAgent.Store(handoffKey, "ray")

	snapshotBefore := snapshotHandoffMap(al)

	_, err = al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "scheduled work", "scheduled", meta.ID,
	)
	require.NoError(t, err)

	snapshotAfter := snapshotHandoffMap(al)
	assert.Equal(t, snapshotBefore, snapshotAfter,
		"sessionActiveAgent handoff map must be identical before and after a scheduled run")
	// The human's override specifically must survive unchanged.
	v, ok := al.sessionActiveAgent.Load(handoffKey)
	require.True(t, ok, "human handoff override must not be deleted by a scheduled run")
	assert.Equal(t, "ray", v)
}

// TestProcessScheduled_PerOwnerSessionKey asserts the run uses the per-owner
// session key agent:<owner>:session:<id> for history, so isolated runs do not
// collide. We verify by reading the history bucket under that exact key.
// Traces to: W-1 (agentSessionKey namespacing).
func TestProcessScheduled_PerOwnerSessionKey(t *testing.T) {
	al, home := schedTestLoop(t)
	mia := registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("recorded"), false)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	_, err = al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "remember me", "scheduled", meta.ID,
	)
	require.NoError(t, err)

	wantKey := "agent:mia:session:" + meta.ID
	hist := mia.Sessions.GetHistory(wantKey)
	require.NotEmpty(t, hist, "history must be written under the per-owner session key %q", wantKey)
	// The wrong-namespace keys must be empty (collision guard).
	assert.Empty(t, mia.Sessions.GetHistory("agent:mia:session:other"),
		"history must not leak into a different session id bucket")
}

// TestProcessScheduled_AskToolAutoDenied asserts that an ask-policy tool call in
// a scheduled run is auto-denied (the tool never executes) and the run does not
// stall — it proceeds to the LLM's next turn and completes.
// Traces to: FR-009, US3 AS4, SC-004.
func TestProcessScheduled_AskToolAutoDenied(t *testing.T) {
	al, home := schedTestLoop(t)

	// Step 1: LLM calls an ask-policy tool. Step 2: plain text recovery.
	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("could not use that tool, finishing anyway")
	mia := registerAgent(t, al, home, "mia", prov, false)

	stub := &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies:      map[string]string{"dangerous_tool": "ask"},
	})

	// Install an APPROVE-everything approver. If AutoDenyAsk is honored the run
	// never consults it and the tool is still denied; if AutoDenyAsk were
	// ignored, this approver would approve and the stub WOULD execute.
	approver := &autoApproveApprover{}
	al.SetToolApprover(approver)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	reply, err := al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "please use dangerous_tool", "scheduled", meta.ID,
	)
	require.NoError(t, err, "auto-deny is a loop-level action, not an agent error")
	assert.Equal(t, "could not use that tool, finishing anyway", reply)
	assert.False(t, stub.wasCalled.Load(),
		"ask-policy tool must be auto-denied — Execute must never run in a headless scheduled run")
	assert.False(t, approver.consulted.Load(),
		"auto-deny must short-circuit BEFORE the approver is consulted")
	assert.Equal(t, 2, prov.CallCount(),
		"the run must continue past the auto-deny (second LLM turn), not stall")
}

// TestProcessScheduled_CancelledRunReturnsPromptly asserts the run registers a
// cancellable turn under the concrete sessionID and returns a context-derived
// error promptly when RequestCancel(CancelScope{SessionID}) fires — no
// wall-clock sleep is used; we wait on the blocking provider's entered channel.
// Traces to: FR-006, W-1 (abort), SC-002.
func TestProcessScheduled_CancelledRunReturnsPromptly(t *testing.T) {
	al, home := schedTestLoop(t)
	blocker := &blockingProvider{entered: make(chan struct{})}
	registerAgent(t, al, home, "mia", blocker, false)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	type result struct {
		reply string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		reply, runErr := al.ProcessScheduled(
			context.Background(), "mia", meta.ID, "hang forever", "scheduled", meta.ID,
		)
		done <- result{reply, runErr}
	}()

	// Wait until the turn is actually executing inside the provider (so the turn
	// is registered under transcriptSessionID == meta.ID).
	select {
	case <-blocker.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking provider was never entered — turn did not start")
	}

	// Abort by session id. This is the caller-side timeout primitive.
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: meta.ID},
		CancelCanceller{UserID: "scheduler", Channel: "system"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired,
		"the turn must be registered under the concrete session id so RequestCancel fires")

	select {
	case res := <-done:
		require.Error(t, res.err, "a canceled scheduled run must return a non-nil error")
		assert.Empty(t, res.reply)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled run did not return promptly after cancellation")
	}
}
