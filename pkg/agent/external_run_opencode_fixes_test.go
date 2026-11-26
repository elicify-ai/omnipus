package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const opencodeFixNativeID = "native-conversation-1899"

// opencodeFixDriver replaces only the external process. The real intake,
// steering queue, continuation, transcript writers and completion all run.
// finishFirst is the process-exit barrier: before it, an unready process has
// emitted no native id; interrupting it cannot manufacture one afterwards.
// HasNativeConversation is the optional readiness seam proposed in DIAGNOSIS.
type opencodeFixDriver struct {
	mu           sync.Mutex
	started      chan struct{}
	firstEvents  chan runner.RunEvent
	firstCtx     context.Context
	firstClosed  bool
	readyAtStart bool
	nativeID     string
	runOpts      []runner.RunOptions
	resumeOpts   []runner.RunOptions
	resumeIDs    []string
	resumeErr    error
}

func (d *opencodeFixDriver) Run(ctx context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.runOpts = append(d.runOpts, opts)
	if len(d.runOpts) != 1 {
		return nil, errors.New("fixture: continuation incorrectly started a fresh conversation")
	}
	d.firstCtx = ctx
	if d.readyAtStart {
		d.nativeID = opencodeFixNativeID
	}
	close(d.started)
	return d.firstEvents, nil
}

func (d *opencodeFixDriver) HasNativeConversation() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nativeID != ""
}

func (d *opencodeFixDriver) finishFirst() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.firstClosed {
		return
	}
	d.firstClosed = true
	if d.firstCtx != nil && d.firstCtx.Err() == nil {
		d.nativeID = opencodeFixNativeID
		d.firstEvents <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "original work completed"}}
		d.firstEvents <- runner.RunEvent{Kind: runner.EventKindEnd}
	}
	close(d.firstEvents)
}

func (d *opencodeFixDriver) ResumeWith(_ context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resumeOpts = append(d.resumeOpts, opts)
	d.resumeIDs = append(d.resumeIDs, d.nativeID)
	if d.nativeID == "" {
		return nil, errors.New("no native conversation id was captured from the prior run")
	}
	if d.resumeErr != nil {
		return nil, d.resumeErr
	}
	events := make(chan runner.RunEvent, 2)
	events <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "follow-up delivered"}}
	events <- runner.RunEvent{Kind: runner.EventKindEnd}
	close(events)
	return events, nil
}

func (d *opencodeFixDriver) Resume(ctx context.Context, runID string, instruction ...string) (<-chan runner.RunEvent, error) {
	opts := runner.RunOptions{RunID: runID}
	if len(instruction) > 0 {
		opts.Input = instruction[0]
	}
	return d.ResumeWith(ctx, opts)
}

func (d *opencodeFixDriver) Decide(runner.PermissionDecision) {}
func (d *opencodeFixDriver) Cancel()                          { d.finishFirst() }
func (d *opencodeFixDriver) Input(string) error {
	return errors.New("fixture: external CLI has no mid-run input channel")
}
func (d *opencodeFixDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

var _ runner.ExternalAgentRunner = (*opencodeFixDriver)(nil)
var _ runner.CurrentOptionsResumer = (*opencodeFixDriver)(nil)

func newOpencodeFixRun(t *testing.T, ready bool, resumeErr error) (*AgentLoop, *opencodeFixDriver, steer.LaunchResult) {
	t.Helper()
	al := newDelegateDispatchLoop(t, &delegateDispatchProvider{})
	wireSteerCompletionDeps(t, al)
	worker, ok := al.registry.GetAgent(delegateExtCLIAgentID)
	require.True(t, ok)
	worker.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{
		Kind: config.ExecutorKindExternalCLI, CLI: "opencode",
	}}
	d := &opencodeFixDriver{
		started: make(chan struct{}), firstEvents: make(chan runner.RunEvent, 2),
		readyAtStart: ready, resumeErr: resumeErr,
	}
	previous := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return d, nil }
	// Join before restoring the factory or tearing down the real stores.
	t.Cleanup(func() {
		d.finishFirst()
		opencodeFixJoin(t, al)
		newExternalDriver = previous
	})
	child := launchDelegateTo(t, al, delegateExtCLIAgentID, "original job 1899")
	_, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	select {
	case <-d.started:
	case <-time.After(5 * time.Second): // Deadlock failsafe, never a latency oracle.
		t.Fatal("SETUP: the real external helper dispatch did not reach the process boundary")
	}
	return al, d, child
}

func opencodeFixJoin(t *testing.T, al *AgentLoop) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		al.steerAdmission().turns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("helper dispatch/continuation goroutine did not finish")
	}
}

// The gateway saves the row before routing this envelope. Use that same
// provenance-aware writer; the test's id is independent of any queue identity.
func opencodeFixHumanMessage(t *testing.T, al *AgentLoop, child steer.LaunchResult, text string) string {
	t.Helper()
	const savedID = "gateway-saved-human-row-1894"
	require.NoError(t, al.ResolveSessionStore(child.SessionID).AppendTranscriptWithProvenance(child.SessionID,
		session.TranscriptEntry{ID: savedID, Role: "user", Content: text, Timestamp: time.Now().UTC()}, "person-1894"))
	require.True(t, al.deliverHumanHelperInput(bus.InboundMessage{
		Channel: "webchat", ChatID: child.SessionID, SessionID: child.SessionID,
		Content: text, TranscriptEntryID: savedID, GatewayUserID: "person-1894",
	}), "the real human-helper route must accept this message")
	return savedID
}

// #1899, founder 2026-11-18 Q1=B: no native id yet means QUEUE WITHOUT
// INTERRUPT, then resume the same conversation after the original run finishes.
func TestOpenCodeFix1899_StartingHelperQueuesWithoutInterruptAndResumesSameConversation(t *testing.T) {
	al, d, child := newOpencodeFixRun(t, false, nil)
	require.False(t, d.HasNativeConversation(), "precondition: no CLI conversation id announced yet")
	const followup = "deliver this after the original run finishes"
	opencodeFixHumanMessage(t, al, child, followup)
	require.Equal(t, 1, al.pendingSteeringCountForScope(child.SessionID), "Q1=B must retain the accepted follow-up")
	d.mu.Lock()
	interrupted := d.firstCtx.Err()
	d.mu.Unlock()
	assert.NoError(t, interrupted, "#1899: a typed message must NOT interrupt a helper before its native conversation id exists")

	d.finishFirst() // Only now may the process announce its id and complete.
	opencodeFixJoin(t, al)
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, rec.State, "#1899: queued startup input must not fail the helper")
	d.mu.Lock()
	assert.Len(t, d.runOpts, 1, "FR-043: never start a replacement conversation")
	assert.Equal(t, []string{opencodeFixNativeID}, d.resumeIDs, "the same captured native conversation must receive the follow-up")
	if assert.Len(t, d.resumeOpts, 1) {
		assert.Equal(t, 1, strings.Count(d.resumeOpts[0].Input, followup), "deliver the exact new input once")
		assert.NotContains(t, d.resumeOpts[0].Input, "original job 1899", "do not replay the original task")
		assert.Equal(t, d.runOpts[0].WorkDir, d.resumeOpts[0].WorkDir, "preserve the workspace")
		assert.Equal(t, d.runOpts[0].Model, d.resumeOpts[0].Model, "preserve the model")
		assert.Equal(t, d.runOpts[0].MaxTurns, d.resumeOpts[0].MaxTurns, "preserve the turn cap")
	}
	d.mu.Unlock()
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Content, "A queued follow-up message could not be processed", "#1899: accepted startup input must not be abandoned")
	}
	assert.Zero(t, al.pendingSteeringCountForScope(child.SessionID), "follow-up must be consumed, not stranded")
}

// #1894: a queued human input keeps the gateway's already-durable identity,
// irrespective of whether the later external delivery succeeds or fails.
func TestOpenCodeFix1894_HumanFollowupHasExactlyOneGatewayTranscriptRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want session.LifecycleState
	}{
		{name: "delivery_succeeds", want: session.LifecycleCompleted},
		{name: "delivery_fails", err: errors.New("fixture resume rejected"), want: session.LifecycleFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, d, child := newOpencodeFixRun(t, true, tc.err)
			const text = "one typed human follow-up 1894"
			savedID := opencodeFixHumanMessage(t, al, child, text)
			d.finishFirst()
			opencodeFixJoin(t, al)
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			require.Equal(t, tc.want, rec.State, "precondition: exercised the requested later-delivery outcome")
			entries := qaReceiptReopenTranscript(t, al.ResolveSessionStore(child.SessionID), child.SessionID)
			var ids []string
			for _, entry := range entries {
				if entry.Role == "user" && entry.Content == text {
					ids = append(ids, entry.ID)
					assert.False(t, strings.HasPrefix(entry.ID, "instruction-ctl_"), "#1894: human input must not acquire a duplicate delegate receipt row")
				}
			}
			assert.Equal(t, []string{savedID}, ids, "#1894: exactly one user row, with the gateway's saved row id, on success AND failure")
			for _, line := range qaReceiptReadLines(t, al, child.SessionID) {
				if line.Text == text {
					assert.NotEqual(t, "agent:"+delegateExtCLIAgentID, line.Actor, "a person's message is not an agent-authored delegate steer")
				}
			}
		})
	}
}

// PIN — #1894 fix must preserve ADR sub-agent control-plane D4: unlike a
// human's saved chat row, delegate(steer) needs its exact-text receipt row.
func TestOpenCodeFix1894_DelegateSteerKeepsDurableReceiptRow(t *testing.T) {
	al, d, child := newOpencodeFixRun(t, true, nil)
	const text = "agent-authored delegate instruction 1894"
	id, err := al.DeliverExternalCLIInstruction(context.Background(), child.SessionID, delegateExtCLIAgentID,
		providers.Message{Role: "user", Content: text}, "delegate-correlation-1894")
	require.NoError(t, err)
	require.Equal(t, "delegate-correlation-1894", id)
	d.finishFirst()
	opencodeFixJoin(t, al)
	lines := qaReceiptReadLines(t, al, child.SessionID)
	require.Len(t, lines, 2, "D4 requires queued acceptance followed by delivered receipt")
	assert.Equal(t, "queued", lines[0].State)
	assert.Equal(t, "delivered", lines[1].State)
	assert.Equal(t, lines[0].ControlID, lines[1].ControlID)
	assert.Equal(t, text, lines[1].Text)
	entries := qaReceiptReopenTranscript(t, al.ResolveSessionStore(child.SessionID), child.SessionID)
	var ids []string
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == text {
			ids = append(ids, entry.ID)
		}
	}
	assert.Equal(t, []string{"instruction-" + lines[0].ControlID}, ids, "D4: one durable injection keyed to the real control receipt")
}
