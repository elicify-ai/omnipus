// Package agent — subturn_key_reuse_race_test.go
//
// Regression test for #586 (root cause N2 — "Stop generation does not
// reliably terminate a chat turn's backend agent loop"), confirmed via
// server-log evidence: a session's recall_conversation tool kept firing
// every ~3s, indefinitely, for at least 26 minutes after "Stop generation"
// was clicked once — outliving the Stop click, a "New chat" navigation, and
// three subsequent unrelated sessions.
//
// Root cause: al.activeTurnStates (a sync.Map keyed by turnState.sessionKey)
// is deleted from in three places — runTurn's `defer al.clearActiveTurn(ts)`
// (loop.go), processTaskDirectExternalCLI's identical defer, and
// spawnSubTurn's own `defer al.activeTurnStates.Delete(childID)`
// (subturn.go) — all of which, before this fix, deleted BY KEY alone,
// without checking that the map still held the SAME *turnState that
// registered it.
//
// A native `delegate follow_up` warm-resume reuses its session id (==
// turnState.sessionKey for that sub-turn) VERBATIM for the next generation,
// as soon as the prior generation's LifecycleRecord reaches a terminal state
// (pkg/tools/delegate.go's spawnCorrectiveFollowUp / executeFollowUp's
// rec.Terminal() gate). But the prior generation's OWN spawnSubTurn call
// keeps re-registering that same key in al.activeTurnStates for up to
// ~935ms AFTER runTurn itself has already returned (see subturn.go's
// "Re-register childTS in activeTurnStates" comment, added so
// IsSubTurnActiveForSpawnCall can still see the span during its own
// persist-retry window) — and the LifecycleRecord can go terminal well
// within that window. If a follow_up's new generation registers under the
// same key during that window, the OLD generation's own deferred cleanup —
// which fires LATER STILL, once its own post-processing finishes — used to
// unconditionally erase WHATEVER now sits under that key. That silently
// deletes the NEW, live, still-running generation's activeTurnStates entry.
//
// Once a live turn's entry is gone from activeTurnStates, NOTHING can ever
// cancel it again: GetActiveTurnHookForSession, InterruptSession,
// InterruptSessionHard, and sessionTurnsStillAlive (pkg/agent/cancel.go,
// steering.go) all resolve exclusively by walking activeTurnStates. The turn
// then runs unmolested — including repeatedly calling whatever tool it
// happens to be stuck invoking (recall_conversation, per the reported
// evidence) — until its own (independently large) MaxIterations ceiling,
// regardless of how many times a user clicks "Stop".
//
// This test reproduces the exact interleaving against the REAL
// registerActiveTurn/clearActiveTurn production primitives (not a
// reimplementation) and asserts that RequestCancel, issued against the
// shared chat session id AFTER the race has played out, still reaches and
// signals the SURVIVING (generation-2) turnState — the concrete, bounded
// proof that "Stop" can actually terminate the turn a confused retry loop is
// running on, even when a follow_up-style key-reuse race occurs.

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestSubTurnKeyReuseRace_CancelStillReachesSurvivingGeneration drives the
// exact activeTurnStates interleaving a native `delegate follow_up`
// warm-resume can produce (see file doc comment) and asserts a subsequent
// RequestCancel against the shared chat session id still finds and signals
// the surviving generation.
func TestSubTurnKeyReuseRace_CancelStillReachesSurvivingGeneration(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "key-reuse-race-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")

	// The parent CHAT session — the id the web SPA's "Stop generation" click
	// actually targets. A real session so ResolveSessionStore/lifecycle
	// transition calls inside RequestCancel have somewhere real to write.
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	chatSessionID := meta.ID
	require.NotEmpty(t, chatSessionID)

	// The delegate/sub-turn's OWN session id (== turnState.sessionKey), the
	// identity a native `follow_up` reuses verbatim across generations. It is
	// deliberately distinct from chatSessionID — a sub-turn's
	// transcriptSessionID equals the PARENT's shared transcript id (FR-6a) so
	// a chat-wide Stop cascades to it, while sessionKey is its own private
	// steering/registration address (subturn.go's DelegateSessionID doc
	// comment).
	const childID = "delegate-session-key-reuse-race"

	// --- Step 1: generation 1 registers (spawnSubTurn's registerActiveTurn) ---
	gen1 := &turnState{
		turnID:              "gen1",
		sessionKey:          childID,
		transcriptSessionID: chatSessionID,
		// ADR-057 FR-011/FR-015 fixture repair: GetActiveTurnHookForSession
		// (and RequestCancel's use of it) now matches on routingSessionID,
		// not transcriptSessionID.
		routingSessionID: session.RoutingSessionID(chatSessionID),
		depth:            1,
		parentTurnID:     "root-turn",
		finishedChan:     make(chan struct{}),
	}
	al.registerActiveTurn(gen1)

	got := al.getActiveTurnState(childID)
	require.Same(t, gen1, got, "generation 1 must be registered under childID")

	// --- Step 2: generation 1's turnLoop finishes; runTurn's OWN internal
	// `defer al.clearActiveTurn(ts)` fires (loop.go) ---
	al.clearActiveTurn(gen1)
	require.Nil(t, al.getActiveTurnState(childID), "clearActiveTurn must remove generation 1")

	// --- Step 3: spawnSubTurn's post-runTurn re-Store (subturn.go: "Register
	// child turn state ... re-register childTS in activeTurnStates") puts
	// gen1 BACK under childID for its own up-to-~935ms cleanup window, even
	// though gen1 has already fully finished. ---
	al.activeTurnStates.Store(childID, gen1)

	// --- Step 4: a native `delegate follow_up` call sees gen1's
	// LifecycleRecord as terminal (it legitimately is, by now) and spawns
	// generation 2, reusing childID VERBATIM (spawnCorrectiveFollowUp: "Native
	// follow_up reuses sessionID verbatim"). It shares the SAME
	// transcriptSessionID (still the same parent chat) and registers via the
	// same production entry point. ---
	gen2 := &turnState{
		turnID:              "gen2",
		sessionKey:          childID,
		transcriptSessionID: chatSessionID,
		routingSessionID:    session.RoutingSessionID(chatSessionID),
		depth:               1,
		parentTurnID:        "root-turn",
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(gen2)
	require.Same(t, gen2, al.getActiveTurnState(childID),
		"generation 2 must have overwritten the stale generation-1 re-Store")

	// --- Step 5: generation 1's OWN deferred cleanup FINALLY fires. This
	// calls the REAL, patched clearActiveTurn (turn.go) — the exact function
	// this fix hardened from a bare `al.activeTurnStates.Delete(ts.sessionKey)`
	// to `al.activeTurnStates.CompareAndDelete(ts.sessionKey, ts)`.
	// subturn.go's own spawnSubTurn defer (`defer
	// al.activeTurnStates.CompareAndDelete(childID, childTS)`) applies the
	// identical primitive for the identical reason; calling the production
	// clearActiveTurn here (rather than reaching into spawnSubTurn's private
	// deferred closure, which would require standing up a full delegation
	// dispatch) exercises the SAME hardened compare-and-delete semantics this
	// change applies everywhere activeTurnStates entries are removed.
	al.clearActiveTurn(gen1)

	// generation 2 — live and running — MUST still be registered. Before the
	// #586 fix (a bare `al.activeTurnStates.Delete(childID)` here), this next
	// assertion fails: the unconditional delete erases generation 2's entry
	// regardless of what is currently stored under childID.
	require.Same(t, gen2, al.getActiveTurnState(childID),
		"generation 2 must survive generation 1's deferred cleanup")

	// --- Full causal-chain proof: a real "Stop generation" (RequestCancel
	// against the shared chat session id) must still find and signal
	// generation 2. GetActiveTurnHookForSession/InterruptSession/
	// sessionTurnsStillAlive all resolve exclusively via activeTurnStates, so
	// this is the concrete, bounded assertion that Stop can actually
	// terminate the turn a stuck retry loop (e.g. recall_conversation) is
	// running on. ---
	hook := al.GetActiveTurnHookForSession(chatSessionID)
	require.NotNil(t, hook, "RequestCancel's turn lookup must find a live turn for the chat session")
	assert.Equal(t, "gen2", hook.TurnID(), "the cancel must resolve to the SURVIVING generation, not the erased/stale one")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: chatSessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "RequestCancel must fire against the surviving generation")
	assert.Contains(t, outcome.Descendants, "gen2", "generation 2 must be in the canceled descendants list")

	graceful, _ := gen2.gracefulInterruptRequested()
	assert.True(t, graceful, "generation 2's turnState must actually receive the graceful-interrupt signal — "+
		"this is the flag runTurn's turnLoop checks every iteration (loop.go) to stop calling tools "+
		"(e.g. a stuck recall_conversation loop) and finalize the turn")
}

// TestSubTurnKeyReuseRace_SpawnSubTurnSideCompareAndDelete covers the SECOND
// activeTurnStates cleanup site — subturn.go's deferred
// `clearActiveTurnStateEntry(childID, childTS)` (subturn.go:1136) — which the
// sibling test above does NOT exercise (it drives clearActiveTurn, the
// runTurn-side cleanup at turn.go). The two are independent lines of code; a
// regression limited to the spawnSubTurn defer alone would not be caught by
// the sibling test.
//
// Rather than stand up a full delegation dispatch (the sibling test's
// rationale for not reaching into spawnSubTurn's private closure), this test
// drives the factored-out helper that subturn.go's defer now calls —
// clearActiveTurnStateEntry — against the exact interleaving a native
// `delegate follow_up` warm-resume produces, and asserts a subsequent
// RequestCancel still reaches the surviving generation.
func TestSubTurnKeyReuseRace_SpawnSubTurnSideCompareAndDelete(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "key-reuse-race-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")

	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	chatSessionID := meta.ID
	require.NotEmpty(t, chatSessionID)

	const childID = "delegate-session-key-reuse-race-subturn-side"

	// Generation 1: the original child. spawnSubTurn registers it via
	// registerActiveTurn; here we register it directly to isolate the
	// cleanup-site under test (the spawn-side defer) from spawnSubTurn's
	// own dispatch machinery.
	gen1 := &turnState{
		turnID:              "gen1-spawnside",
		sessionKey:          childID,
		transcriptSessionID: chatSessionID,
		routingSessionID:    session.RoutingSessionID(chatSessionID),
		depth:               1,
		parentTurnID:        "root-turn",
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(gen1)
	require.Same(t, gen1, al.getActiveTurnState(childID))

	// gen1 finishes its turnLoop but spawnSubTurn's post-runTurn re-Store
	// (subturn.go's "Re-register childTS in activeTurnStates") puts gen1
	// BACK under childID for its up-to-~935ms cleanup window.
	al.activeTurnStates.Store(childID, gen1)

	// A native `delegate follow_up` sees gen1 as terminal and reuses childID
	// verbatim for generation 2 (spawnCorrectiveFollowUp).
	gen2 := &turnState{
		turnID:              "gen2-spawnside",
		sessionKey:          childID,
		transcriptSessionID: chatSessionID,
		routingSessionID:    session.RoutingSessionID(chatSessionID),
		depth:               1,
		parentTurnID:        "root-turn",
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(gen2)
	require.Same(t, gen2, al.getActiveTurnState(childID),
		"generation 2 must have overwritten the stale generation-1 re-Store")

	// Generation 1's spawnSubTurn defer finally fires. This is the production
	// primitive under test — subturn.go's
	// `defer al.clearActiveTurnStateEntry(childID, childTS)` — invoked here
	// against gen1 (the finished child) while gen2 (the live, reused-key
	// generation) is the current entry under childID. If this helper ever
	// regresses to a bare Delete(childID), gen2 is erased.
	al.clearActiveTurnStateEntry(childID, gen1)

	require.Same(t, gen2, al.getActiveTurnState(childID),
		"generation 2 must survive generation 1's spawnSubTurn-side cleanup")

	// Causal-chain proof: a "Stop generation" (RequestCancel against the
	// shared chat session id) must still find and signal the surviving gen2.
	hook := al.GetActiveTurnHookForSession(chatSessionID)
	require.NotNil(t, hook, "cancel turn lookup must find a live turn for the chat session")
	assert.Equal(t, "gen2-spawnside", hook.TurnID(),
		"the cancel must resolve to the SURVIVING generation, not the erased/stale one")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: chatSessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "RequestCancel must fire against the surviving generation")
	assert.Contains(t, outcome.Descendants, "gen2-spawnside",
		"generation 2 must be in the canceled descendants list")

	graceful, _ := gen2.gracefulInterruptRequested()
	assert.True(t, graceful, "generation 2's turnState must receive the graceful-interrupt signal")
}
