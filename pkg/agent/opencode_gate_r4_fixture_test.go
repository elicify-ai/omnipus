package agent

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: ARCHITECT-RULING-R4-goals.md §2 D1-D4 (founder Option R).
// Catalogue sentences reference the generated W14 contract per team-lead's
// 2026-11-23 ruling; code, display names and state/effect oracles stay independent.
const opencodeR4Code = "delegation_not_resumable"

var opencodeR4NoFact = generated.LLMErrorMessageVariants[opencodeR4Code]["no_cli"]

var opencodeR4CLIs = []struct{ key, display string }{
	{"claude-code", "Claude Code"}, {"codex", "Codex"}, {"opencode", "OpenCode"},
}

func opencodeR4Chat(display string) string {
	return strings.ReplaceAll(generated.LLMErrorUserMessages[opencodeR4Code], "{cli}", display)
}

func opencodeR4Neutral(display string) string {
	return strings.ReplaceAll(generated.LLMErrorNeutralMessages[opencodeR4Code], "{cli}", display)
}

func opencodeR4Goal() *steer.GoalSpec {
	return &steer.GoalSpec{
		Criteria: []steer.Criterion{{Text: "The answer contains three numbered steps"}},
		DoD:      []steer.Criterion{{Text: "The answer is the requested text itself"}},
	}
}

func opencodeR4Loop(t *testing.T, cli string) (*AgentLoop, *delegateDispatchProvider) {
	t.Helper()
	p := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, p)
	wireSteerCompletionDeps(t, al)
	worker, ok := al.registry.GetAgent(delegateExtCLIAgentID)
	require.True(t, ok, "SETUP: the authorized external worker must exist")
	worker.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{
		Kind: config.ExecutorKindExternalCLI, CLI: cli,
	}}
	return al, p
}

func opencodeR4Request(parent, target string, goal *steer.GoalSpec) steer.LaunchRequest {
	return steer.LaunchRequest{
		SteeringSessionID: parent, TargetAgentID: target, Task: "Write the requested three steps",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "r4-goal-call"}, Goal: goal,
	}
}

// Only the external process is doubled. Intake, routing, Launch, Dispatch,
// completion/dispose, archive, lifecycle and inbox remain production code.
// No parallel subtests: the production factory is process-global.
type opencodeR4Driver struct {
	*opencodeFixDriver
	cancels atomic.Int64
}

func (d *opencodeR4Driver) Cancel() {
	d.cancels.Add(1)
	d.opencodeFixDriver.Cancel()
}

func opencodeR4ProcessEdge(t *testing.T, al *AgentLoop) (*opencodeR4Driver, *atomic.Int64) {
	t.Helper()
	d := &opencodeR4Driver{opencodeFixDriver: &opencodeFixDriver{
		started: make(chan struct{}), firstEvents: make(chan runner.RunEvent, 2),
	}}
	calls := &atomic.Int64{}
	old := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		calls.Add(1)
		return d, nil
	}
	t.Cleanup(func() {
		d.finishFirst()
		opencodeFixJoin(t, al)
		newExternalDriver = old
	})
	return d, calls
}

func opencodeR4Running(t *testing.T, cli string) (*AgentLoop, *opencodeR4Driver, *atomic.Int64, steer.LaunchResult) {
	t.Helper()
	al, _ := opencodeR4Loop(t, cli)
	d, calls := opencodeR4ProcessEdge(t, al)
	child := launchDelegateTo(t, al, delegateExtCLIAgentID, "original R4 goal-free job")
	_, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err, "SETUP: the goal-free real dispatch must start")
	select {
	case <-d.started:
	case <-time.After(5 * time.Second): // Deadlock failsafe, not a latency oracle.
		t.Fatal("SETUP: real dispatch did not reach the scripted process")
	}
	require.EqualValues(t, 1, calls.Load(), "instrument: the process counter sees a real dispatch")
	return al, d, calls, child
}

type opencodeR4Writes struct {
	lifecycle, goals, sessions int
	transcripts                []string
	active, queued             int
}

func opencodeR4Snapshot(t *testing.T, al *AgentLoop) opencodeR4Writes {
	t.Helper()
	recs, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, err)
	goals, skipped, err := resolveGoalRecordStore().List()
	require.NoError(t, err)
	require.Empty(t, skipped, "a malformed goal cannot be counted as absent")
	metas, err := al.GetSessionStore().ListSessions()
	require.NoError(t, err)
	var paths []string
	err = filepath.WalkDir(al.GetSessionStore().BaseDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.Contains(d.Name(), "transcript") && strings.HasSuffix(d.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	require.NoError(t, err, "the absence check must traverse the complete transcript store")
	sort.Strings(paths)
	gate := al.steerAdmission()
	gate.mu.Lock()
	active, queued := len(gate.active), len(gate.queue)
	gate.mu.Unlock()
	return opencodeR4Writes{len(recs), len(goals), len(metas), paths, active, queued}
}

func opencodeR4SaveHuman(t *testing.T, al *AgentLoop, sid, text string) bus.InboundMessage {
	t.Helper()
	const id = "r4-gateway-saved-human"
	require.NoError(t, al.ResolveSessionStore(sid).AppendTranscriptWithProvenance(sid,
		session.TranscriptEntry{ID: id, Role: "user", Content: text, Timestamp: time.Now().UTC()}, "r4-person"))
	return bus.InboundMessage{
		Channel: "webchat", ChatID: sid, SessionID: sid, Content: text,
		TranscriptEntryID: id, GatewayUserID: "r4-person",
	}
}

// The synchronous tap is the gateway's actual event observation seam. Prove
// it can see a typed helper event before asserting absence or cardinality.
type opencodeR4Events struct {
	mu     sync.Mutex
	events []Event
}

func opencodeR4Observe(t *testing.T, al *AgentLoop, sid string) *opencodeR4Events {
	t.Helper()
	c := &opencodeR4Events{}
	al.SetEventSyncTap(func(e Event) {
		c.mu.Lock()
		c.events = append(c.events, e)
		c.mu.Unlock()
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	al.eventBus.Emit(Event{Kind: EventKindError, Meta: EventMeta{SessionKey: sid},
		Payload: ErrorPayload{SessionID: sid, Code: "r4-instrument", Message: "observer control"}})
	seen := c.take()
	require.Len(t, seen, 1, "instrument: the installed tap must observe actual event-bus emissions")
	payload, ok := seen[0].Payload.(ErrorPayload)
	require.True(t, ok, "instrument: observed event retains its typed payload")
	require.Equal(t, "r4-instrument", payload.Code)
	return c
}

func (c *opencodeR4Events) take() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]Event(nil), c.events...)
	c.events = nil
	return out
}

func opencodeR4AssertRefusal(t *testing.T, al *AgentLoop, before *session.LifecycleRecord, msg bus.InboundMessage, display string, observed *opencodeR4Events) {
	t.Helper()
	// Inspect physical JSONL and a reopened real store, not an invented replay
	// payload or only the live store's in-memory projection.
	rows := qaReceiptReopenTranscript(t, al.ResolveSessionStore(msg.SessionID), msg.SessionID)
	userIndex, userCount, errorsCount := -1, 0, 0
	for i, entry := range rows {
		if entry.ID == msg.TranscriptEntryID || (entry.Role == "user" && entry.Content == msg.Content) {
			userIndex, userCount = i, userCount+1
		}
		if entry.ErrorCode == opencodeR4Code {
			errorsCount++
		}
	}
	assert.Equal(t, 1, userCount, "D2: the already-saved human row must not be duplicated")
	assert.Equal(t, 1, errorsCount, "D2: exactly one classified refusal must be saved")
	if assert.Greater(t, len(rows), userIndex+1, "D2: the NEXT saved row after the human message must be the refusal") {
		next := rows[userIndex+1]
		assert.Equal(t, "error", next.Status, "the refusal is a replayable error entry, not assistant prose")
		assert.Equal(t, opencodeR4Code, next.ErrorCode)
		assert.Equal(t, opencodeR4Chat(display), next.Content)
	}
	var errorsSeen []ErrorPayload
	for _, event := range observed.take() {
		assert.NotEqual(t, before.SteeredBy.SteeringSessionID, event.Meta.SessionKey, "D2: no notice goes to the parent")
		if event.Kind == EventKindError {
			p, ok := event.Payload.(ErrorPayload)
			require.True(t, ok, "the actual event must have a typed error payload")
			errorsSeen = append(errorsSeen, p)
		}
	}
	if assert.Len(t, errorsSeen, 1, "D2: one typed error must be published to the helper's own view") {
		assert.Equal(t, msg.SessionID, errorsSeen[0].SessionID)
		assert.Equal(t, opencodeR4Code, errorsSeen[0].Code)
		assert.Equal(t, opencodeR4Chat(display), errorsSeen[0].Message)
	}
	after, err := al.GetSessionLifecycleStore().Load(msg.SessionID)
	require.NoError(t, err)
	assert.Equal(t, before.State, after.State, "refusal must not change lifecycle state")
	assert.Equal(t, before.Generation, after.Generation, "refusal must not revive the helper")
	assert.Equal(t, before.ExecutionID, after.ExecutionID, "refusal must not dispatch a new execution")
	assert.Zero(t, al.pendingSteeringCountForScope(msg.SessionID), "refused input must never be queued")
}
