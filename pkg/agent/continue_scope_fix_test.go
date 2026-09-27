// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// continue_scope_fix_test.go: RED tests for the design note
// coordination/logs/continuescope-architect-note.md (2026-09-27), branch
// fix/continue-global-active-turn-scope.
//
// Covers:
//   - Bug 1: AgentLoop.Continue (pkg/agent/steering.go) uses the GLOBAL
//     al.GetActiveTurn() instead of al.GetActiveTurnBySession(sessionKey), so
//     an unrelated session's active turn wrongly blocks THIS session's own
//     post-turn steering drain (session_worker.go's processTurn drain loop).
//   - Bug 2: inside Continue, dequeueSteeringMessagesForScopeWithFallback runs
//     BEFORE agentForSession's nil-check, so a nil-agent failure discards the
//     just-dequeued steering messages outright instead of leaving them
//     recoverable in the queue.
//   - The drain-loop resilience redesign, exit (a): a persistent Continue
//     failure inside processTurn's drain loop must retry a bounded number of
//     times, then dequeue-and-report (never leave the queue silently stuck
//     forever), clear inTurn so the NEXT message for the session is treated as
//     a fresh turn, and publish a distinct, visible failure notice.
//
// Exit (c) (buildContinuationTarget failing) has its own test file:
// continuation_target_failure_scope_test.go.
//
// These tests are test-file-only (qa-lead RED); they never modify production
// code. Every assertion encodes the DESIRED (post-fix) behavior described in
// the design note, so each test is RED today (for the reason cited in its
// comment) and is expected to go GREEN once backend-lead lands the fix.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// scopeFixCountingProvider is a minimal LLM-provider test double (the correct
// mock boundary — the process edge, not the unit under test) that returns a
// distinct, pre-scripted response per call in sequence (repeating the last
// scripted entry once exhausted), so a test can tell which logical turn
// produced a given outbound message without racing a blocking gate. Never
// blocks — deliberately, so these tests exercise the drain loop's actual
// retry/backoff timing rather than a synchronization gate the test controls.
type scopeFixCountingProvider struct {
	mu       sync.Mutex
	calls    int
	scripted []string
}

func (p *scopeFixCountingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.calls
	p.calls++
	resp := "response"
	switch {
	case idx < len(p.scripted):
		resp = p.scripted[idx]
	case len(p.scripted) > 0:
		resp = p.scripted[len(p.scripted)-1]
	}
	return &providers.LLMResponse{Content: resp}, nil
}

func (p *scopeFixCountingProvider) GetDefaultModel() string { return "scope-fix-mock" }

func (p *scopeFixCountingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newContinueScopeFixConfig builds a minimal single-agent config for these
// tests, mirroring the shape newTestAgentLoop/mustNewAgentLoop callers across
// this package already use (List seeds the real "mia" default agent so
// buildContinuationTarget/processMessage have someone to route to).
func newContinueScopeFixConfig(t *testing.T) *config.Config {
	t.Helper()
	tmpDir := t.TempDir()
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: tmpDir}},
		},
	}
}

// TestSessionWorker_Continue_UnrelatedActiveSession_MustNotStrandOwnQueuedFollowup
// is the RED test for Bug 1 (architect note, caller-survey row "GetActiveTurn
// -> Continue" plus the Q1/addendum framing): session A has a genuinely
// active turn (registered directly in al.activeTurnStates, the same
// registerActiveTurn fixture cancel_background_bash_test.go already
// establishes as the package's way to pin a real map entry); session B has NO
// active turn of its own but DOES have a message sitting in ITS OWN steering
// queue when its own post-turn drain loop
// (session_worker.go::processTurn's `for !w.closeSteeringWhenDrained(...)`)
// calls al.Continue(ctx, B's own sessionKey, ...).
//
// Spec (desired, from the design note's Q1 framing and the caller-survey
// verdict "BUG — already being fixed elsewhere: swap to
// GetActiveTurnBySession(sessionKey)"): session A being active must be
// irrelevant to session B's own Continue call — B's own session has no active
// turn, so Continue must succeed, dequeue B's queued follow-up, and run it
// through to completion.
//
// TODAY (bug): Continue's `if active := al.GetActiveTurn(); active != nil`
// (pkg/agent/steering.go::Continue) ranges the WHOLE activeTurnStates map —
// session A's entry is the only one in it (B's own turn already
// self-cleared before the drain loop runs, per turn.go's clearActiveTurn
// doc comment) — so it returns non-nil regardless of which session asked,
// and Continue errors out before ever dequeuing B's message.
//
// Harness note (found empirically, not a production bug): driving this
// through a full w.processTurn(ctx, msgB) call — enqueuing B's late message
// while a blocking provider's first call is in flight, mirroring
// TestAgentLoop_Run_AutoContinuesLateSteeringMessage's own technique — does
// NOT reach processTurn's OUTER drain loop at all. runTurn's OWN iteration
// driver (pkg/agent/loop_run_turn.go, ~line 1188-1198) polls the steering
// queue again once iteration > 1, and (confirmed by an actual run showing 2
// provider calls with session A's active-turn fixture having no effect on
// the outcome) something in that same driver re-loops after a direct-answer
// iteration when steering arrived during the just-finished call — swallowing
// the message into the SAME turn before it ever reaches processTurn's own
// `for !w.closeSteeringWhenDrained(...)` tail. That inner mechanism is a
// different code path from the one Bug 1 lives in, and there is no external
// hook to land a message strictly after runTurn fully exits but strictly
// before processTurn's own drain check without racing that inner poll.
//
// This test therefore drives session_worker.go's actual drain-tail
// functions directly — closeSteeringWhenDrained and Continue, the exact two
// symbols the design note names — in the exact sequence
// processTurn's own loop uses (copied verbatim from session_worker.go
// ~line 604-630), after manually reproducing processTurn's own turn-active
// bookkeeping (w.inTurn.Store(true) under steerMu, mirroring processTurn's
// own turn-start marking at ~line 437-439). It does not re-implement
// Continue's or closeSteeringWhenDrained's own logic — both are called for
// real; only the surrounding processTurn orchestration (response-guard
// defers, panic recovery, typing-stop notify — all irrelevant to Bug 1) is
// not exercised here.
func TestSessionWorker_Continue_UnrelatedActiveSession_MustNotStrandOwnQueuedFollowup(t *testing.T) {
	cfg := newContinueScopeFixConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &scopeFixCountingProvider{scripted: []string{"turn1-response", "turn2-continued-response"}}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	// Session A: a genuinely active, mid-turn state — unrelated to session B,
	// registered directly in the same map GetActiveTurn/GetActiveTurnBySession
	// both read (activeTurnStates), exactly as cancel_background_bash_test.go's
	// registerActiveTurn helper already does for other tests in this package.
	registerActiveTurn(t, al, "unrelated-session-A", "unrelated-turn-A")

	msgB := bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-b"},
		ChatID:  "chat-b",
		Content: "first message for B",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-b"},
	}

	target, err := al.buildContinuationTarget(msgB)
	if err != nil {
		t.Fatalf("buildContinuationTarget(msgB) unexpected error: %v", err)
	}
	if target == nil {
		t.Fatal("buildContinuationTarget(msgB) returned a nil target for an ordinary chat message")
	}

	// Session B's own turn has already fully run and cleared by the time the
	// drain tail starts (real precondition, run once here via a plain,
	// non-blocking call — its own outcome is not asserted on; only the
	// steering queue and activeTurnStates state afterward matter).
	if _, _, err := al.processMessage(context.Background(), msgB); err != nil {
		t.Fatalf("processMessage(msgB) unexpected error: %v", err)
	}

	if _, err := al.EnqueueSteeringMessage(target.SessionKey, testDefaultAgentID, providers.Message{
		Role: "user", Content: "late append for B",
	}, ""); err != nil {
		t.Fatalf("EnqueueSteeringMessage(B's own scope) unexpected error: %v", err)
	}
	if got := al.pendingSteeringCountForScope(target.SessionKey); got != 1 {
		t.Fatalf("precondition failed: pending count for B = %d, want 1", got)
	}

	w := newSessionWorker(target.SessionKey, al, func() {})
	w.steerMu.Lock()
	w.inTurn.Store(true)
	w.steerMu.Unlock()

	// --- processTurn's own drain-tail loop, called for real (session_worker.go ~604-630) ---
	var finalResponse string
	var continueFailed bool
	var lastContinueErr error
	for !w.closeSteeringWhenDrained(target.SessionKey) {
		continued, continueErr := al.Continue(context.Background(), target.SessionKey, target.Channel, target.ChatID, target.WorkspaceID)
		if continueErr != nil {
			continueFailed = true
			lastContinueErr = continueErr
			break
		}
		if continued == "" {
			break
		}
		finalResponse = continued
	}
	if finalResponse != "" {
		al.publishResponseIfNeeded(context.Background(), nil, target.Channel, target.ChatID, finalResponse)
	}

	// --- Assertions encode the DESIRED (post-fix) outcome ---

	// (1) B's own drain must have actually continued and published the
	// continuation's real response — not nothing (today's silent stranding).
	if continueFailed {
		t.Fatalf("Continue(B's own sessionKey) failed: %v — session A's unrelated active turn must not "+
			"block session B's own drain (Bug 1: Continue's global GetActiveTurn() wrongly saw it)",
			lastContinueErr)
	}
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content != "turn2-continued-response" {
			t.Fatalf("outbound content = %q, want %q (the continuation's response)",
				out.Content, "turn2-continued-response")
		}
	default:
		t.Fatal("expected exactly one outbound message (the continuation's response) — got none: " +
			"session B's queued follow-up was never delivered, the exact stranding Bug 1 describes")
	}

	// (2) B's queue must be fully drained — not left "stuck in the steering
	// queue" (the design note's phrase for today's exact failure mode).
	if got := al.pendingSteeringCountForScope(target.SessionKey); got != 0 {
		t.Fatalf("pending count for B after drain = %d, want 0 (message must not be stranded)", got)
	}

	// (3) The continuation must have genuinely run a second turn through the
	// provider — not merely returned a cached/short-circuited value.
	if got := provider.callCount(); got != 2 {
		t.Fatalf("provider call count = %d, want 2 (turn 1 + the continuation) — "+
			"a call count of 1 means Continue never got past the (buggy) active-turn guard", got)
	}

	// (4) inTurn must end up cleared by closeSteeringWhenDrained's own success
	// branch — the mechanism the design note calls "load-bearing, not
	// cosmetic": without it, the NEXT message for this session would be
	// wrongly steered into a queue nobody will ever drain again.
	if w.inTurn.Load() {
		t.Fatal("worker inTurn still true after the drain tail finished — the NEXT message for this " +
			"session would be wrongly steered into a queue nobody will ever drain again")
	}
}

// TestAgentLoop_Continue_NilAgent_MustNotDiscardDequeuedMessages is the RED
// test for Bug 2 (design note, "A bug inside the confirmed bug"): Continue
// dequeues the steering queue (dequeueSteeringMessagesForScopeWithFallback)
// BEFORE checking whether agentForSession(sessionKey) returns nil. When the
// registry has no agent to route to (registry.GetDefaultAgent() nil — the
// note's example: "the session's agent was deleted mid-turn"),
// agentForSession returns nil and Continue returns an error — but the
// just-dequeued message is sitting in a local variable that goes out of
// scope on return. It is not merely "stranded in the queue" (Bug 1's
// failure mode) — it is gone outright, worse than Bug 1.
//
// Spec (desired, from the note's "Required fix... move the
// agentForSession(sessionKey) nil-check to before the dequeue call"): after
// this reorder, a Continue call that fails because there is no agent to run
// must leave the steering queue untouched — the message must still be
// recoverable afterward.
//
// TODAY (bug): the message is dequeued at pkg/agent/steering.go::Continue's
// dequeueSteeringMessagesForScopeWithFallback call (~line 750) and then
// discarded when the nil-agent check (~line 755-758) fails and Continue
// returns — the queue is empty afterward even though nothing was ever run.
func TestAgentLoop_Continue_NilAgent_MustNotDiscardDequeuedMessages(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// Deliberately NO agents registered at all: registry.GetDefaultAgent()
			// then returns nil (registry.go's Priority-1/Priority-2 ladder both
			// require at least one registered chat-target agent), so
			// agentForSession(sessionKey) — which falls back to GetDefaultAgent()
			// whenever the session key doesn't parse to a specific registered
			// agent ID — returns nil for any session key. This is the
			// "session's agent was deleted mid-turn" scenario the design note
			// names as agentForSession's one deterministic, permanent failure
			// cause.
			List: []config.AgentConfig{},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	const sessionKey = "session-with-no-agent"

	if _, err := al.EnqueueSteeringMessage(sessionKey, "", providers.Message{
		Role: "user", Content: "queued instruction",
	}, ""); err != nil {
		t.Fatalf("EnqueueSteeringMessage unexpected error: %v", err)
	}
	if got := al.pendingSteeringCountForScope(sessionKey); got != 1 {
		t.Fatalf("precondition failed: pending count = %d before Continue, want 1", got)
	}

	resp, err := al.Continue(context.Background(), sessionKey, "test", "chat1", "")

	if err == nil {
		t.Fatal("Continue must still fail — there is genuinely no agent to run this session's turn; " +
			"this test is about the QUEUE's state on that failure, not about making Continue succeed")
	}
	if resp != "" {
		t.Fatalf("Continue response = %q on a failure path, want empty", resp)
	}

	if got := al.pendingSteeringCountForScope(sessionKey); got != 1 {
		t.Fatalf("pending count after a failed Continue = %d, want 1 (the message must remain "+
			"recoverable in the queue — a nil-agent failure must not discard a message that was "+
			"never actually run)", got)
	}
}

// TestSessionWorker_DrainLoop_PersistentContinueFailure_BoundedRetryThenAbandonAndNotify
// is a characterization/RED test for the design note's addendum exit (a):
// "each queued message is either processed exactly once or the user is told
// it wasn't." It targets the NEW bounded-retry-then-abandon behavior the note
// recommends (uniform bounded retry inside processTurn's drain loop, then a
// dequeue-and-report step on exhaustion — name-suggested
// `abandonQueuedSteering`, not asserted here by name per this dispatch's
// instruction to test the BEHAVIOR observable from processTurn's outside).
//
// Today's code has no retry and no abandon-and-notify step at all — it just
// logs a WarnCF and returns on the FIRST Continue failure (session_worker.go,
// current code) — so this test is genuinely RED against today's code, for a
// different (simpler) reason than the eventual GREEN: today there is no
// DISTINCT notice about the abandoned steering queue (see this test's own
// correction note below on what IS published today: turn 1's own response,
// via the pre-existing deferred response guard — not "nothing").
//
// Mechanism for a PERMANENT, deterministic Continue failure that leaves
// buildContinuationTarget itself unaffected (so the drain loop's target
// resolves and its queue-pending check fires, exactly as the note's Q1
// mechanics require): a configured builtin hook name that is enabled but not
// registered. ensureHooksInitialized (pkg/agent/hook_mount.go) computes and
// CACHES this error exactly once via sync.Once and returns the identical
// cached error on every subsequent call — so every one of the drain loop's
// retries fails identically, deterministically, forever, while leaving turn
// 1's own processMessage call UNAFFECTED: confirmed empirically (this test's
// first RED run published "Mock response", mockProvider's fixed reply, as
// turn 1's own real response) that processMessage's own call path does not
// call ensureHooksInitialized at all — only Continue
// (pkg/agent/steering.go) and ProcessDirectWithChannel (pkg/agent/loop.go)
// do. This is independent of Bug 1 and Bug 2's fix state.
func TestSessionWorker_DrainLoop_PersistentContinueFailure_BoundedRetryThenAbandonAndNotify(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: tmpDir}},
		},
		Hooks: config.HooksConfig{
			Enabled: true,
			Builtins: map[string]config.BuiltinHookConfig{
				"scope-fix-nonexistent-hook": {Enabled: true},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	msg := bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-a"},
		ChatID:  "chat-a",
		Content: "first message",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-a"},
	}

	target, err := al.buildContinuationTarget(msg)
	if err != nil {
		t.Fatalf("buildContinuationTarget unexpected error: %v — this test needs routing to succeed "+
			"so only Continue (via the broken hook config) fails, not target resolution", err)
	}
	if target == nil {
		t.Fatal("buildContinuationTarget returned a nil target for an ordinary chat message")
	}

	// Precondition: confirm the permanent failure is real and reachable via
	// Continue's own call path before relying on it for the drain loop.
	if _, err := al.Continue(context.Background(), "precondition-probe-scope", "test", "chat-probe", ""); err == nil {
		t.Fatal("precondition failed: Continue must fail on the broken hook config (nonexistent builtin " +
			"hook name) for this test's failure mechanism to be meaningful")
	}

	w := newSessionWorker(target.SessionKey, al, func() {})

	// Harness fix (this was the actual cause of this test's stray RED — not
	// the implementation, which backend-lead independently proved correct
	// with a throwaway scratch test): enqueuing the steering message BEFORE
	// starting the turn (the original fixture) let loop_run_turn.go's own
	// pre-existing initial-steering-poll pick the message up and inject it
	// directly into turn 1 itself — confirmed via the log line "Injected
	// steering message into context content_len=39 iteration=1 ...
	// turn_id=mia-turn-1" (39 = the exact byte length of the enqueued string
	// below). By the time processTurn's POST-turn drain loop ran, the queue
	// was already empty, so the retry/abandon-and-notify path this test
	// exists to prove was never exercised.
	//
	// A first attempt at fixing this adapted lateSteeringProvider
	// (steering_test.go)'s blocking-first-Chat-call gate, enqueuing while
	// turn 1's own provider call was in flight — the same technique
	// TestAgentLoop_Run_AutoContinuesLateSteeringMessage uses. That
	// reproduces a DIFFERENT swallow, empirically confirmed by an actual
	// run: runTurn's own inner mechanism ("Steering arrived after direct LLM
	// response; continuing turn", loop_run_turn.go) sees the message the
	// moment the blocked call is released and consumes it as iteration 2 of
	// the SAME turn (turn_id unchanged, iterations_total=2) — never reaching
	// processTurn's OUTER drain-tail loop at all, so Continue() was never
	// even called. That is in fact the exact mechanism
	// TestAgentLoop_Run_AutoContinuesLateSteeringMessage itself exercises
	// (its own assertions never distinguish an in-turn continuation from a
	// separate post-turn Continue() call) — it is not a technique that can
	// prove THIS test's target behavior, which lives strictly inside
	// processTurn's own `for !w.closeSteeringWhenDrained(...)` loop, reached
	// only once runTurn has fully finished and decided not to re-loop.
	//
	// The correct synchronization point is therefore AFTER runTurn's
	// iteration loop has made its last steering-continuation decision — which
	// is exactly when EventKindTurnEnd fires (loop.go: a defer registered
	// before turnLoop, so LIFO-last to run, right as runTurn is about to
	// return). EventBus.SetSyncTap (eventbus.go) is an existing production
	// hook, built for precisely this: it runs synchronously on the emitting
	// goroutine and blocks Emit until the tap returns. Installing a tap that
	// blocks on the first EventKindTurnEnd lets this test enqueue strictly
	// after turn 1's own iteration logic is done (so the inner mechanism
	// above can no longer see it) and strictly before processTurn's
	// drain-tail loop gets to run (runTurn/processMessage has not yet
	// returned to processTurn) — the ordering the design note needs,
	// guaranteed by a real block, never a sleep.
	turnEndReached := make(chan struct{})
	releaseTurnEnd := make(chan struct{})
	var tapOnce sync.Once
	al.eventBus.SetSyncTap(func(evt Event) {
		if evt.Kind != EventKindTurnEnd {
			return
		}
		tapOnce.Do(func() {
			close(turnEndReached)
			<-releaseTurnEnd
		})
	})
	defer al.eventBus.SetSyncTap(nil)

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.processTurn(context.Background(), msg)
	}()

	select {
	case <-turnEndReached:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for turn 1's own runTurn call to finish (EventKindTurnEnd never observed)")
	}

	// Enqueue strictly after turn 1's own iteration loop is done (no more
	// chances for runTurn's inner steering-continuation mechanism to see it)
	// and strictly before processTurn's own POST-turn drain loop runs
	// (runTurn is still blocked inside the tap, so processMessage has not
	// returned to processTurn yet) — guaranteed by the sync-tap block above,
	// never by a sleep.
	if _, err := al.EnqueueSteeringMessage(target.SessionKey, testDefaultAgentID, providers.Message{
		Role: "user", Content: "late append that can never be delivered",
	}, ""); err != nil {
		t.Fatalf("EnqueueSteeringMessage unexpected error: %v", err)
	}

	close(releaseTurnEnd)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for processTurn to return")
	}
	elapsed := time.Since(start)

	// (i) Bounded: terminates within a generous ceiling (not infinite/hung),
	// and is not the near-instant single failed check a zero-retry
	// implementation would produce (loose floor — this dispatch does not lock
	// backend-lead's exact backoff schedule, only that SOME deliberate
	// bounded-retry delay exists between the first failure and the notice).
	if elapsed > 5*time.Second {
		t.Fatalf("drain loop took %v to give up — want a bounded retry budget, not an unbounded/hanging retry", elapsed)
	}

	// (ii) On exhaustion the queue must be dequeued-and-reported — not left
	// silently stuck forever, and not left non-empty.
	if got := al.pendingSteeringCountForScope(target.SessionKey); got != 0 {
		t.Fatalf("pending count for %q after abandonment = %d, want 0 (dequeued-and-reported, "+
			"never left silently stuck)", target.SessionKey, got)
	}

	// (iii) inTurn must be cleared so the NEXT message for this session is
	// treated as a fresh turn via the inbox, not silently steered into a
	// queue nobody will ever drain again.
	if w.inTurn.Load() {
		t.Fatal("worker inTurn still true after abandonment — the next message for this session " +
			"would be wrongly steered into a queue nobody will ever drain again")
	}

	// (iv) A distinct, visible failure notice must have been published, IN
	// ADDITION to turn 1's own genuine response.
	//
	// Correction against this test's own earlier doc comment: turn 1's own
	// response is NOT suppressed here the way it is in the successful-
	// continuation case (TestAgentLoop_Run_AutoContinuesLateSteeringMessage).
	// That test's suppression comes from `published`/`finalResponse` being
	// explicitly re-managed by the drain loop's SUCCESS path (finalResponse
	// is overwritten with the continuation's own response before the single
	// controlled publish at session_worker.go ~632-635). On the FAILURE path
	// (continueErr != nil -> bare `return`), neither that overwrite nor that
	// controlled publish ever runs, so `published` stays false — meaning
	// processTurn's function-level deferred response guard (~466-477, which
	// fires unconditionally on every return) publishes turn 1's OWN original,
	// untouched response. Confirmed empirically: this test's first RED run
	// asserted "exactly one outbound message, the notice" and failed showing
	// content "Mock response" (mockProvider's fixed reply) — proving a
	// message IS already published today, just the wrong one, not none.
	// This mirrors the design note's own exit-(c) finding almost exactly
	// ("the primary response is not actually lost today; only the
	// steering-queue side is silent") — it turns out to also hold for
	// exit (a) via the identical deferred-guard mechanism.
	var firstContent string
	select {
	case out := <-msgBus.OutboundChan():
		firstContent = out.Content
		if firstContent == "" {
			t.Fatal("turn 1's own response was empty — unexpected for a plain mock provider call")
		}
	default:
		t.Fatal("expected at least one outbound message (turn 1's own response, via the existing " +
			"deferred response guard) — got none")
	}

	// The specific wording below ("resend"/"could not be processed"/
	// "problem") tracks the design note's OWN suggested copy ("Something
	// like...") — a soft assumption, not a locked product string; flagged in
	// the RED-pack report as adjustable if backend-lead phrases the notice
	// differently.
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content == "" {
			t.Fatal("notice content is empty — the whole point of the fail-loud design is a visible message")
		}
		if out.Content == firstContent {
			t.Fatalf("second outbound message is identical to turn 1's own response (%q) — want a "+
				"DISTINCT notice about the abandoned steering queue, not a re-publish", firstContent)
		}
		lower := strings.ToLower(out.Content)
		if !strings.Contains(lower, "resend") && !strings.Contains(lower, "could not be processed") &&
			!strings.Contains(lower, "problem") {
			t.Fatalf("notice content = %q — does not look like the distinct fail-loud notice the design "+
				"note describes (expected wording hinting at a failed/undeliverable follow-up)", out.Content)
		}
	default:
		t.Fatal("expected a SECOND, distinct outbound message (the fail-loud notice about the abandoned " +
			"steering queue) — got none: today's code has no abandon-and-notify step at all, it silently " +
			"drops the queue's fate on the first Continue failure")
	}

	// No THIRD, competing outbound message should have been queued either.
	select {
	case extra := <-msgBus.OutboundChan():
		t.Fatalf("unexpected THIRD outbound message %q — expected exactly turn 1's response plus one notice", extra.Content)
	default:
	}
}
