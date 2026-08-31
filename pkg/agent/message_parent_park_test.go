// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// message_parent_park_test.go is the regression test for defect C2 (ADR-057
// UAT 2026-08-03): a successful message_parent(kind="question", wait=true)
// call durably parks the calling child's own LifecycleRecord into
// needs_input (pkg/tools/message_parent.go's parkNeedsInput) — but before
// this fix, pkg/agent/loop.go's runTurn tool-execution loop had no way to
// learn about that transition and kept iterating: a further, unwanted LLM
// call, more tool calls, eventually overwriting the durable park before any
// `delegate respond` could ever answer it. The child ended up permanently
// stuck — not parked, not progressing, not terminal, and unreachable via
// respond() ("session ... is not parked on correlation_id ...").
//
// Both tests drive a REAL child sub-turn via a direct spawnSubTurn call
// (mirroring subturn_followup_resume_test.go's and
// subturn_toolcall_hardabort_status_test.go's own established pattern for
// this package) — a real *tools.MessageParentTool, a real runTurn, and a
// REAL, store-backed *session.LifecycleStore/*session.MessageInboxStore
// (never a spy). The child's durable LifecycleRecord is seeded directly
// (mirroring pkg/tools/delegate_adr053_test.go's own seeding convention)
// rather than minted via the full DelegateTool.Execute(action="run") path.
//
// This is a DELIBERATE ISOLATION, not a shortcut: driving the scenario
// through the full synchronous `delegate` tool call surfaces a SEPARATE,
// already-reported defect in pkg/tools/delegate.go's executeSync (~L2026) —
// its post-dispatch switch unconditionally calls
// transitionLifecycle(..., LifecycleCompleted) whenever the returned
// tools.ToolResult has Interrupted==false && IsError==false, which is
// exactly the (correct, non-error, non-interrupted) shape a parked child's
// result has. That call stomps the needs_input state this fix correctly
// leaves in place, reproducing the "session ... is not parked" respond()
// failure from a DIFFERENT cause than C2's actual defect (the turn loop
// itself no longer runs past the park — that is fully fixed and is exactly
// what these two tests prove). See pkg/agent/subturn.go's ToolResult.ParksTurn
// propagation comment (the `else` branch converting turnRes to a
// tools.ToolResult) for the exact fix delegate.go still needs — reported,
// not made here, since pkg/tools/delegate.go is out of this file's scope.
package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	c2ChildTaskMarker  = "CHILD_TASK_C2: ask your parent a question and wait for the answer"
	c2CorrelationID    = "corr-c2-park-1"
	c2ProgressAfterBug = "CHILD_KEPT_RUNNING_AFTER_PARK_C2"
)

// c2ParkTestProvider is a content-routed scripted provider (same technique
// as message_parent_real_context_test.go's mp576RecordingProvider). It
// counts how many times the LLM is invoked — exactly the observable this
// test needs: before the fix, the child is invoked a SECOND time after its
// own message_parent(wait=true) call parks it (the bug, script step 2
// below, which issues a further REAL tool call so the regression shows up
// as a concrete tool-call-count defect, not merely an LLM-call-count one);
// after the fix, it is invoked exactly once and never again.
type c2ParkTestProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *c2ParkTestProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	var lastUser string
	for _, m := range messages {
		if m.Role == "user" {
			lastUser = m.Content
		}
	}
	if !strings.Contains(lastUser, c2ChildTaskMarker) {
		return nil, fmt.Errorf("c2ParkTestProvider: no scripted route for lastUser=%q", lastUser)
	}

	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()

	switch call {
	case 1:
		// First (and, post-fix, ONLY) LLM call: ask a blocking question.
		// authority=self_ok so a later `delegate respond` (the
		// resumability test) is permitted to answer it directly (R§8.2
		// would otherwise fail-closed-deny an owner_required question).
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID: "message_parent-question-0",
					Function: &providers.FunctionCall{
						Name: "message_parent",
						Arguments: fmt.Sprintf(
							`{"kind":"question","text":"need your input to continue","wait":true,`+
								`"correlation_id":%q,"authority":"self_ok"}`,
							c2CorrelationID,
						),
					},
				},
			},
		}, nil
	case 2:
		// ONLY reached PRE-FIX (the bug): the loop resumed after the park
		// and called the LLM again. Issues a SECOND real tool call so the
		// test can assert a concrete tool-call-count regression, not just
		// an LLM-call-count one.
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID: "message_parent-progress-0",
					Function: &providers.FunctionCall{
						Name:      "message_parent",
						Arguments: fmt.Sprintf(`{"kind":"progress","text":%q}`, c2ProgressAfterBug),
					},
				},
			},
		}, nil
	default:
		// ONLY reached pre-fix, after the second tool call above executed
		// normally (kind=progress never parks) — winds the child down so
		// the test harness doesn't need a 3rd script step.
		return &providers.LLMResponse{Content: c2ProgressAfterBug}, nil
	}
}

func (p *c2ParkTestProvider) GetDefaultModel() string { return "c2-park-mock" }

func (p *c2ParkTestProvider) getCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// c2ParkTestHarness bundles everything both C2 tests need: a real AgentLoop
// wired with a real *tools.MessageParentTool and a real, store-backed
// *session.LifecycleStore/*session.MessageInboxStore, driven through one
// real, direct spawnSubTurn call whose child asks a blocking question.
type c2ParkTestHarness struct {
	al               *AgentLoop
	lifecycleStore   *session.LifecycleStore
	inboxStore       *session.MessageInboxStore
	delegateTool     *tools.DelegateTool
	provider         *c2ParkTestProvider
	parentDurableKey string
	childSessionID   string
	spawnResult      *tools.ToolResult
	// parentTS is the SAME *turnState used to dispatch the original spawn
	// below — kept so TestMessageParent_QuestionWaitTrue_ParkedChildResumableViaRespond
	// can wrap its `delegate respond` call's context with it (see that test's
	// own comment): AgentLoopSpawner.SpawnSubTurn (subturn.go) requires a real
	// parent turnState in ctx, exactly mirroring how `delegate respond` is
	// only ever invoked in production as a tool call from within the
	// delegating parent's own live turn.
	parentTS *turnState
	// subTurnEndMu guards subTurnEndEvents (populated by a background
	// goroutine draining al.SubscribeEvents — see setupC2ParkScenario).
	subTurnEndMu     *sync.Mutex
	subTurnEndEvents *[]SubTurnEndPayload
}

func setupC2ParkScenario(t *testing.T) *c2ParkTestHarness {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "c2-park-mock"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}

	provider := &c2ParkTestProvider{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	storeDir := t.TempDir()
	lifecycleStore := session.NewLifecycleStore(filepath.Join(storeDir, "lifecycle"))
	inboxStore := session.NewMessageInboxStore(filepath.Join(storeDir, "inbox"))

	messageParentTool := tools.NewMessageParentTool(inboxStore, lifecycleStore)
	messageParentTool.SetSessionMessagingEnabled(func() bool { return true })
	al.RegisterTool(messageParentTool)

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	// Established test-harness pattern (delegate_sync_toolcall_completion_test.go,
	// delegate_async_completion_test.go, et al.): a manually-constructed
	// *tools.DelegateTool registered via al.RegisterTool overwrites the
	// production one loop.go already auto-wires with a real spawner for every
	// agent (loop.go's NewSubTurnSpawner(al) call at agent construction), so
	// this harness must wire its own or `delegate respond` has no sub-turn
	// spawner to resume the session with — production is never missing this,
	// only a test fixture that forgets to mirror the wiring.
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	delegateTool.SetLifecycleStore(lifecycleStore)
	delegateTool.SetMessageInbox(inboxStore)
	delegateTool.SetSteeringSink(al)
	delegateTool.SetSessionMessagingEnabled(func() bool { return true })
	al.RegisterTool(delegateTool)

	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"message_parent": "allow",
				"delegate":       "allow",
			},
		})
	}

	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")
	parentTS := newFollowUpResumeTestParent(t, al, store, "parent-c2-park")
	childID := "child-c2-park-" + parentTS.transcriptSessionID

	// Seed the child's durable ADR-053 LifecycleRecord directly — normally
	// minted by DelegateTool.executeRun before dispatch; seeding it here is
	// what isolates C2's actual defect from delegate.go's separate,
	// already-reported executeSync bug (see this file's package doc
	// comment).
	require.NoError(t, lifecycleStore.Persist(&session.LifecycleRecord{
		SessionID:        childID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: parentTS.transcriptSessionID,
		WorkspaceID:      "ws-c2-park",
		AgentID:          parentTS.agent.ID,
	}))

	// Subscribe to the event bus BEFORE dispatching, mirroring
	// subturn_cancel_status_test.go's established pattern — this is what
	// lets TestMessageParent_QuestionWaitTrue_SubagentEndFrameParked below
	// observe the real EventKindSubTurnEnd (wire-level SubTurnEndPayload)
	// spawnSubTurn emits, not just the in-process ToolResult.ParksTurn flag
	// the other two tests in this file already check.
	var subTurnEndMu sync.Mutex
	var subTurnEndEvents []SubTurnEndPayload
	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	go func() {
		for evt := range sub.C {
			if payload, ok := evt.Payload.(SubTurnEndPayload); ok {
				subTurnEndMu.Lock()
				subTurnEndEvents = append(subTurnEndEvents, payload)
				subTurnEndMu.Unlock()
			}
		}
	}()

	cfgSub := SubTurnConfig{
		Model:             "c2-park-mock",
		SystemPrompt:      c2ChildTaskMarker,
		DelegateSessionID: childID,
		MaxTokens:         4096,
	}
	spawnResult, spawnErr := spawnSubTurn(withSpawnToolCallID(context.Background(), "call-c2-park"), al, parentTS, cfgSub)
	require.NoError(t, spawnErr, "spawnSubTurn must complete without a dispatch error")

	// spawnSubTurn returning only guarantees dispatch completed — the event
	// bus subscription is drained by the SEPARATE goroutine above, so a
	// brief poll is needed for it to have actually processed the event.
	require.Eventually(t, func() bool {
		subTurnEndMu.Lock()
		defer subTurnEndMu.Unlock()
		return len(subTurnEndEvents) > 0
	}, 2*time.Second, 5*time.Millisecond, "EventKindSubTurnEnd must be observed by the subscriber")

	return &c2ParkTestHarness{
		al:               al,
		lifecycleStore:   lifecycleStore,
		inboxStore:       inboxStore,
		delegateTool:     delegateTool,
		provider:         provider,
		parentDurableKey: parentTS.transcriptSessionID,
		childSessionID:   childID,
		spawnResult:      spawnResult,
		parentTS:         parentTS,
		subTurnEndMu:     &subTurnEndMu,
		subTurnEndEvents: &subTurnEndEvents,
	}
}

// TestMessageParent_QuestionWaitTrue_ParksAndStopsTurn is the core C2
// fix proof. FAILS before the fix (h.provider.getCalls() == 2 and
// rec.State has moved past needs_input) and PASSES after it.
func TestMessageParent_QuestionWaitTrue_ParksAndStopsTurn(t *testing.T) {
	h := setupC2ParkScenario(t)

	// Positive lower bound (Binding Rule 4): the child's message_parent
	// (question) call genuinely executed before anything else is asserted —
	// it is durably recorded in the parent's inbox. A test where the child
	// never got this far would pass the "zero after" assertion below
	// vacuously; this rules that out.
	msgs, _, _, err := h.inboxStore.Drain(h.parentDurableKey, h.childSessionID, "", 10)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "expected exactly the one question message the child sent before parking")
	kind, kerr := msgs[0].Discriminator()
	require.NoError(t, kerr)
	assert.Equal(t, "question", kind)

	// The stop-the-turn proof: the child's LLM strand must have been
	// invoked EXACTLY ONCE. Before the fix, runTurn was blind to the park
	// and looped back for a second LLM call — the scripted response for
	// that second call issues a further, real message_parent(kind=progress)
	// tool call, so pre-fix this count is 2 (not merely "some LLM call
	// happened again" but a further tool call actually running past the
	// park).
	assert.Equal(t, 1, h.provider.getCalls(),
		"the child's LLM strand must be invoked exactly once — a second call means the turn "+
			"resumed after message_parent(wait=true) parked it, reproducing the C2 defect "+
			"(and, per this script, executed a further real tool call that should never have run)")

	// The durable, store-backed (never a spy) proof: the LifecycleRecord
	// must still show needs_input with the ORIGINAL correlation_id, not
	// overwritten by further execution.
	rec, err := h.lifecycleStore.Load(h.childSessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleNeedsInput, rec.State,
		"the child's LifecycleRecord must remain parked in needs_input after the WHOLE child "+
			"turn finishes — an overwritten (e.g. completed/running) state is exactly how the live "+
			"UAT's respond() call failed with \"session ... is not parked on correlation_id ...\"")
	require.NotNil(t, rec.NeedsInput)
	assert.Equal(t, c2CorrelationID, rec.NeedsInput.CorrelationID)

	// The ToolResult spawnSubTurn returns to its caller (delegate.go, for
	// the real production path) must also carry the park signal, so a
	// FUTURE delegate.go fix has something to branch on (see this file's
	// package doc comment for the exact change still needed there).
	require.NotNil(t, h.spawnResult)
	assert.True(t, h.spawnResult.ParksTurn,
		"spawnSubTurn's returned ToolResult must carry ParksTurn so callers (delegate.go's "+
			"executeSync/executeAsync) can learn the child parked rather than completed")
	assert.False(t, h.spawnResult.IsError, "a park is not an error")
}

// TestMessageParent_QuestionWaitTrue_SubagentEndFrameParked is the wire-level
// proof for the LAST layer of the C2 fix: the child's own EventKindSubTurnEnd
// (the in-process source of the subagent_end WS frame — see
// pkg/gateway/websocket.go's EventKindSubTurnEnd handler, a pure passthrough
// of this exact Status field) must carry SubTurnStatusParked, not
// SubTurnStatusSuccess. Before spawnSubTurn's endStatus switch gained its
// `lastTurnStatus == TurnEndStatusParked` case, this test FAILED: every case
// in that switch is gated on err != nil except the (also nil-error)
// TurnEndStatusAborted case, so a parked child — nil error, not aborted —
// fell all the way through to the endStatus := SubTurnStatusSuccess
// initializer. That is the exact operator-reported defect: "a sub-agent that
// pauses to ask its parent a question ... emits a subagent_end frame with
// status:'success' ... the UI can show a paused sub-agent as finished."
func TestMessageParent_QuestionWaitTrue_SubagentEndFrameParked(t *testing.T) {
	h := setupC2ParkScenario(t)

	h.subTurnEndMu.Lock()
	defer h.subTurnEndMu.Unlock()
	events := *h.subTurnEndEvents
	require.Len(t, events, 1, "exactly one EventKindSubTurnEnd must be emitted for the one child sub-turn")
	assert.Equal(t, SubTurnStatusParked, events[0].Status,
		"a child parked by message_parent(kind=\"question\", wait=true) must report its "+
			"subagent_end status as \"parked\" — reporting \"success\" (the pre-fix default) or any "+
			"other value lets the UI show a genuinely-waiting-on-its-parent child as finished")
	assert.Equal(t, "", events[0].Reason,
		"SubagentEndFrame.reason is documented as populated only when status is \"interrupted\" — "+
			"a parked child must not carry a stale/spurious reason")
}

// TestMessageParent_QuestionWaitTrue_ParkedChildResumableViaRespond proves
// the flip side the fix must NOT break: a parked child must remain
// resumable via the REAL `delegate respond` action (pkg/tools/delegate.go's
// executeRespond, unmodified — exercised here, not reimplemented), not
// traded for a different stuck (terminal-and-dead) state.
func TestMessageParent_QuestionWaitTrue_ParkedChildResumableViaRespond(t *testing.T) {
	h := setupC2ParkScenario(t)

	// Precondition re-established (not assumed): the child really is parked
	// on the expected correlation_id before respond() is attempted.
	recBefore, err := h.lifecycleStore.Load(h.childSessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleNeedsInput, recBefore.State)
	require.NotNil(t, recBefore.NeedsInput)
	require.Equal(t, c2CorrelationID, recBefore.NeedsInput.CorrelationID)

	// Drive the REAL delegate `respond` action — the exact production path
	// an owning parent uses to answer a parked question — from a context
	// carrying the PARENT's own transcript session id (the ownership check
	// DelegateTool.executeRespond enforces via verifyCallerOwnsSession) AND
	// the PARENT's own real turnState (withTurnState): respond redispatches
	// the child via the SAME spawner-backed isResume machinery follow_up
	// uses (executeAsync -> t.spawner.SpawnSubTurn), and
	// AgentLoopSpawner.SpawnSubTurn (subturn.go) requires a real parent
	// turnState in ctx — exactly mirroring production, where `delegate
	// respond` is only ever invoked as a tool call issued by the delegating
	// parent's own LLM from within its own live turn, never from a bare
	// context. A bare context.Background() here (no turnState) fails with
	// "parent turnState not found in context - cannot spawn sub-turn outside
	// of a turn" — a test-harness gap, not a product defect, since no real
	// caller of respond exists outside a live parent turn.
	respondCtx := tools.WithTranscriptSessionID(context.Background(), h.parentDurableKey)
	respondCtx = withTurnState(respondCtx, h.parentTS)
	result := h.delegateTool.Execute(respondCtx, map[string]any{
		"action":         "respond",
		"session_id":     h.childSessionID,
		"correlation_id": c2CorrelationID,
		"text":           "yes, go ahead",
	})
	require.False(t, result.IsError,
		"respond must succeed against a genuinely-still-parked session: %s", result.ForLLM)

	// Resumed, not terminal-and-dead: the record left needs_input for a
	// live running state, with the park cleared — mirroring
	// pkg/tools/delegate_adr053_test.go's TestDelegateTool_Respond_ParksThenResumes
	// assertions, now proven against a record a REAL parked runTurn left
	// behind rather than one hand-seeded directly into the store.
	recAfter, err := h.lifecycleStore.Load(h.childSessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleRunning, recAfter.State)
	assert.Nil(t, recAfter.NeedsInput)

	// The answer is genuinely queued for the child's own turn to consume —
	// not silently dropped — proving the session can actually continue, not
	// just that a status flag flipped.
	assert.Equal(t, 1, h.al.pendingSteeringCountForScope(h.childSessionID),
		"the answer must be queued as a steering message for the child's own session scope, "+
			"ready to be delivered into a resumed turn")

	// Drain the redispatch goroutine executeAsync launched above before this
	// test returns and setupC2ParkScenario's t.Cleanup(al.Close) runs
	// (registered first, so it fires AFTER this, LIFO) — otherwise that
	// still-in-flight goroutine races al.Close()'s own session/workspace
	// teardown and t.TempDir()'s RemoveAll, intermittently failing cleanup
	// with "directory not empty" (harness-only; not a product race, since a
	// real gateway process has no such teardown to race). The resumed turn's
	// own script step legitimately has no route in c2ParkTestProvider (its
	// "Answer to your question..." user message is never one of the two
	// scripted cases), so it errors and fails closed — this test only cares
	// that respond's synchronous contract (asserted above) held; draining
	// the async remainder is purely for a clean, non-flaky shutdown.
	h.delegateTool.WaitForAsyncTasks()
}
