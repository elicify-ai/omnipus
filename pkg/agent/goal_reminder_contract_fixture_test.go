package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: founder-approved 2026-10-21 dispatch R1-R3. Only the paid provider
// is replaced. In particular, no test writes a running state or execution ID.
const (
	grReviewerID = "reminder-reviewer"
	grReply      = "The requested work round has finished."
	grWatchdog   = 10 * time.Second // deadlock watchdog, never a timing oracle
)

type grCall struct {
	sessionID string
	record    *session.LifecycleRecord
	readErr   error
	turn      *turnState
	claim     executionClaim
	owner     *executionDisposition
	human     bool
	userID    string
	messages  []providers.Message
	release   chan struct{}
	once      sync.Once
}

func (c *grCall) open() { c.once.Do(func() { close(c.release) }) }

// Each execution is held at its first external request. Once released, later
// model rounds of that SAME execution may finish (including a child handback).
// A second execution has its own gate, so an illicit duplicate is observable.
type grProvider struct {
	al       *AgentLoop
	agentID  string
	mu       sync.Mutex
	calls    []*grCall
	opened   map[executionClaim]bool
	allOpen  bool
	requests chan *grCall
}

func newGRProvider(al *AgentLoop, agentID string) *grProvider {
	return &grProvider{al: al, agentID: agentID, opened: make(map[executionClaim]bool), requests: make(chan *grCall, 32)}
}

func (*grProvider) GetDefaultModel() string { return "goal-reminder-contract-provider" }

func (p *grProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	c := &grCall{messages: append([]providers.Message(nil), messages...), release: make(chan struct{})}
	p.al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok || ts.agentID != p.agentID {
			return true
		}
		ts.mu.RLock()
		c.turn, c.sessionID = ts, ts.transcriptSessionID
		c.human, c.userID = ts.opts.UserInitiated, ts.opts.UserID
		c.owner = ts.opts.executionDisposition
		ts.mu.RUnlock()
		c.claim = p.al.tsExecutionClaim(ts, c.sessionID)
		return false
	})
	if c.sessionID != "" {
		c.record, c.readErr = session.NewLifecycleStore(p.al.GetSessionLifecycleStore().Dir()).Load(c.sessionID)
	}
	p.mu.Lock()
	p.calls = append(p.calls, c)
	if p.allOpen || (c.claim.RunID != "" && p.opened[c.claim]) {
		c.open()
	}
	p.mu.Unlock()
	select {
	case p.requests <- c:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-c.release:
		return &providers.LLMResponse{Content: grReply}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *grProvider) release(c *grCall) {
	p.mu.Lock()
	p.opened[c.claim] = true
	for _, call := range p.calls {
		if call == c || (c.claim.RunID != "" && call.claim == c.claim) {
			call.open()
		}
	}
	p.mu.Unlock()
}

func (p *grProvider) openAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allOpen = true
	for _, c := range p.calls {
		c.open()
	}
}

func (p *grProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *grProvider) drainRequests() {
	for {
		select {
		case <-p.requests:
		default:
			return
		}
	}
}

type grResult struct {
	reply string
	err   error
}

type grPending struct {
	result grResult
	done   chan struct{}
}

type grHarness struct {
	al       *AgentLoop
	id       string
	goalID   string
	root     *grProvider
	reviewer *grProvider
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// Optional actor configuration changes only fixture inputs. The default path
// and all real runtime/ownership assertions remain unchanged for R1/R2/Stop.
type grHarnessOptions struct {
	extraAgents   []config.AgentConfig
	extraPolicies map[string]string
}

func newGRHarness(t *testing.T, options ...grHarnessOptions) *grHarness {
	t.Helper()
	resetGoalTriggerStateForTest()
	t.Cleanup(resetGoalTriggerStateForTest)
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{Agents: config.AgentsConfig{
		// The loop derives its runtime home from this parent, while tools use
		// that home to resolve the real default workspace and task stores.
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10},
		List:     []config.AgentConfig{{ID: testDefaultAgentID, Home: t.TempDir()}, {ID: grReviewerID, Home: t.TempDir()}},
	}}
	cfg.Sandbox.ToolPolicies = map[string]string{tools.SetGoalToolName: "allow", "message_parent": "allow", "read_file": "allow", "set_todos": "allow", "goal_claim": "allow"}
	for _, option := range options {
		cfg.Agents.List = append(cfg.Agents.List, option.extraAgents...)
		for name, policy := range option.extraPolicies {
			cfg.Sandbox.ToolPolicies[name] = policy
		}
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)
	installTestSessionLauncher(t, al)
	pwWireRuntime(t, al)
	al.SetGoalRouteSessionStore()
	ctx, cancel := context.WithCancel(context.Background())
	h := &grHarness{al: al, ctx: ctx, cancel: cancel}
	h.id = newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	owner := "goal-reminder-owner"
	require.NoError(t, al.GetSessionStore().SetMeta(h.id, session.MetaPatch{Owner: &owner}))
	h.root, h.reviewer = newGRProvider(al, testDefaultAgentID), newGRProvider(al, grReviewerID)
	for id, p := range map[string]*grProvider{testDefaultAgentID: h.root, grReviewerID: h.reviewer} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok, "SETUP: contract native agent must be registered")
		inst.Provider = p
	}
	t.Cleanup(func() {
		h.root.openAll()
		h.reviewer.openAll()
		h.cancel()
		joined := make(chan struct{})
		go func() { h.wg.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(grWatchdog):
			t.Error("SETUP: contract receiver goroutines did not join before store teardown")
		}
	})
	return h
}

func (h *grHarness) start(fn func() (string, error)) *grPending {
	run := &grPending{done: make(chan struct{})}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer close(run.done)
		run.result.reply, run.result.err = fn()
	}()
	return run
}

func (h *grHarness) humanInput(t *testing.T, text string) bus.InboundMessage {
	t.Helper()
	entryID := fmt.Sprintf("human-%d", h.root.count())
	require.NoError(t, h.al.GetSessionStore().AppendTranscript(h.id, session.TranscriptEntry{
		ID: entryID, Role: "user", AgentID: testDefaultAgentID, Content: text, Timestamp: time.Now().UTC(),
	}))
	return bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + h.id,
		SessionID: h.id, SessionKey: fmt.Sprintf("agent:%s:session:%s", testDefaultAgentID, h.id),
		GatewayUserID: "goal-reminder-owner", UserInitiated: true, Content: text,
		Sender: bus.SenderInfo{CanonicalID: "human:goal-reminder-owner"},
	}
}

func (h *grHarness) human(t *testing.T, text string) *grPending {
	t.Helper()
	msg := h.humanInput(t, text)
	return h.start(func() (string, error) {
		reply, _, err := h.al.processMessage(h.ctx, msg)
		return reply, err
	})
}

func (h *grHarness) system(msg bus.InboundMessage) *grPending {
	return h.start(func() (string, error) { return h.al.processSystemMessage(h.ctx, msg) })
}

func grAwaitCall(t *testing.T, provider *grProvider, run *grPending) *grCall {
	t.Helper()
	select {
	case c := <-provider.requests:
		return c
	case <-run.done:
		t.Fatalf("expected admitted work at the real provider; receiver exited: reply=%q error=%v", run.result.reply, run.result.err)
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: receiver did not reach provider or return")
	}
	return nil
}

func grJoin(t *testing.T, run *grPending) grResult {
	t.Helper()
	select {
	case <-run.done:
		return run.result
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: receiver did not finish after provider release")
		return grResult{}
	}
}

func (h *grHarness) load(t *testing.T) *session.LifecycleRecord {
	t.Helper()
	rec, err := session.NewLifecycleStore(h.al.GetSessionLifecycleStore().Dir()).Load(h.id)
	require.NoError(t, err, "root lifecycle must be durably readable")
	return rec
}

func (h *grHarness) completedRoot(t *testing.T) *session.LifecycleRecord {
	t.Helper()
	run := h.human(t, "Start the requested work.")
	call := grAwaitCall(t, h.root, run)
	h.root.release(call)
	result := grJoin(t, run)
	require.NoError(t, result.err, "SETUP: genuine human root turn must complete")
	require.Equal(t, grReply, result.reply)
	rec := h.load(t)
	require.Equal(t, session.LifecycleCompleted, rec.State, "SETUP: start from a genuinely completed ordinary root")
	require.NotNil(t, rec.ExecutionID, "SETUP: prior human execution must have a genuine identity")
	require.NotEmpty(t, rec.ExecutionID.RunID)
	h.root.drainRequests()
	return rec
}

func (h *grHarness) arm(t *testing.T) {
	t.Helper()
	const intent = "Produce the requested work and obtain an independent review."
	h.goalID = armGoalRecord(t, h.id, intent, recordedGoalCriteria(intent), 0, time.Now().Add(-time.Hour))
	h.al.recordGoalRouting(h.id, h.goalID, "webchat", "chat-"+h.id,
		fmt.Sprintf("agent:%s:session:%s", testDefaultAgentID, h.id), testDefaultAgentID)
}

// This is the real keeper tick, notifier and bus; never a fixture-made wake.
func (h *grHarness) reminder(t *testing.T) bus.InboundMessage {
	t.Helper()
	h.al.goalQuietWindowSettle(time.Now())
	select {
	case msg := <-h.al.bus.InboundChan():
		require.Equal(t, "system", msg.Channel)
		require.Equal(t, h.id, msg.AsyncTranscriptSessionID)
		require.Equal(t, testDefaultAgentID, msg.AsyncOriginAgentID)
		require.Equal(t, "system:goal_loop", msg.Sender.CanonicalID)
		return msg
	default:
		t.Fatal("real keeper did not publish a reminder for the eligible completed root")
		return bus.InboundMessage{}
	}
}

func (h *grHarness) assertAdmission(t *testing.T, call *grCall, prior *session.LifecycleRecord) {
	t.Helper()
	require.NoError(t, call.readErr)
	require.NotNil(t, call.record, "reminder provider must observe a durable root record")
	assert.Equal(t, h.id, call.sessionID)
	assert.Equal(t, session.LifecycleRunning, call.record.State, "R1: reminder work must run on a genuinely admitted running root, not the prior completed execution")
	assert.Equal(t, prior.Generation+1, call.record.Generation, "R1: a completed root reminder starts the next ordinary round")
	assert.Nil(t, call.record.SteeredBy, "root reminder must not manufacture a parent edge")
	assert.False(t, call.human, "automatic reminder must not pretend a human message")
	assert.Empty(t, call.userID, "automatic reminder must not borrow the human principal")
	require.NotNil(t, call.record.ExecutionID)
	assert.NotEqual(t, prior.ExecutionID.RunID, call.record.ExecutionID.RunID, "R1: reminder needs a fresh execution ID")
	assert.NotEmpty(t, call.record.ExecutionID.RunID)
	assert.NotEqual(t, uint64(0), call.record.ExecutionID.BootSeq, "R1: the admitted execution must use a genuine nonzero boot epoch")
	assert.Equal(t, h.al.bootEpochFor(), call.record.ExecutionID.BootSeq)
	want := executionClaim{SessionID: h.id, Generation: prior.Generation + 1,
		RunID: call.record.ExecutionID.RunID, BootSeq: call.record.ExecutionID.BootSeq}
	assert.Equal(t, want, call.claim, "R1: live reminder turn must be bound to its durable execution identity")
	if assert.NotNil(t, call.owner, "R1: reminder must have the ordinary execution owner, not a manual running stamp") {
		assert.Equal(t, want, call.owner.claim, "R1: the actual ordinary owner must belong to the producing reminder, not another session or round")
	}
}
