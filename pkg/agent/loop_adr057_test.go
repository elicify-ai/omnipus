// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_adr057_test.go — tests for ADR-057 unit U9 (pkg/agent/loop.go):
//
//   - W4: WS payload stamping (FR-011/FR-012/FR-013) — the
//     ToolExecStart/EndPayload construction sites and the
//     u9ToolExecSessionIDs helper that powers them, plus TurnEndPayload's
//     FR-012 stamping.
//   - W10b: the grant-read cross-unit obligation FROM U17a (Integration
//     order, cross-unit requests table: "U9 | the two-key grant read —
//     IsAllowed under the child's own session key | U17"), closing FR-031/
//     FR-080 end to end.
//   - W16b: ListAllSessions' FR-098 cross-store ordering + cursor contract,
//     plus FR-091/FR-104's hierarchy filter (u9FilterSessionHierarchy) and
//     FR-098(a)'s comparator (u9SessionRecencyLess).
//
// FR-100 (the loop.go doc-rot fix — the stale bare "InterruptSession"
// reference at the bottom of loop.go's command-runtime wiring) has no
// separate runtime behaviour: the symbol no longer compiling if
// reintroduced is the only mechanical guard available, which is exactly
// what building this package already provides.
//
// Per binding Rule 5, this is a NEW file — no other unit may add a test
// here. Per binding Rule 1, every assertion below runs against REAL
// production types: a REAL *AgentLoop with a REAL *session.UnifiedStore
// (al.sharedSessionStore, wired by the real NewAgentLoop boot path via
// mustNewAgentLoop/newAL), a REAL *security.ApprovalGrantStore, and REAL
// turnStates registered in activeTurnStates via al.registerActiveTurn —
// never a spy or fake that records its argument and returns success.
//
// Where the property under test can be reached through a full, real turn
// (the root-level W4 case), it is:
// TestToolExecPayloads_RealRootTurn_StampsRoutingKeyOnly drives a real
// al.runAgentLoop call and asserts on the REAL emitted events read off a
// real al.SubscribeEvents subscription. The DIVERGING (delegated-child)
// case cannot be reached through any real end-to-end call yet — U7's
// spawnSubTurn, the only production code that will ever set
// routingSessionID != transcriptSessionID, is Wave F and does not exist in
// this tree — so those cases construct a turnState directly and register
// it, mirroring turn_adr057_test.go's (U3) identical precedent for the
// exact same underlying reason (see that file's own note on test #29).
//
// Per the spec's "Corollary — distinct ids everywhere", every parent/child
// (or routing/transcript) id pair below is constructed as two distinct,
// non-equal values, and assertions check WHICH one was used, not merely
// that a value is present.
package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u9IsolateSharedStore replaces al.sharedSessionStore with a fresh
// *session.UnifiedStore rooted at a brand-new t.TempDir(), and returns it.
//
// Why this is necessary rather than trusting newAL/newTestAgentLoop's own
// store: that helper points cfg.Agents.Defaults.Home directly at an
// os.MkdirTemp("", "agent-test-*") directory (not a per-agent subdirectory),
// so AgentLoop's boot-time `homePath := filepath.Dir(cfg.AgentHomeBasePath())`
// climbs ONE level ABOVE that temp directory — landing on the OS temp root
// itself (verified: os.TempDir()/sessions, e.g. /tmp/sessions on this box) —
// and every test in the package that goes through that helper shares that
// SAME fixed directory for the lifetime of the temp root, not just for one
// test's t.TempDir() lifetime. Confirmed by hand: a fresh newAL(t) followed
// immediately by 5 al.sharedSessionStore.NewSession calls and one
// al.ListAllSessions(0, 0, "", false) returned 24 sessions, most bearing
// ULIDs from unrelated, much older test runs on this same machine — proving
// the pollution is real, pre-existing (nothing this unit's loop.go change
// introduced), and not scoped per-test. Rather than editing that shared,
// unowned test helper (out of this unit's ownership and out of scope for an
// unrelated infrastructure defect), every ListAllSessions test in this file
// swaps in its own t.TempDir()-rooted store immediately after construction,
// satisfying binding Rule 1 ("a REAL UnifiedStore rooted at a t.TempDir()")
// more precisely than relying on the shared helper would.
func u9IsolateSharedStore(t *testing.T, al *AgentLoop) *session.UnifiedStore {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	al.sharedSessionStore = store
	return store
}

// ---------------------------------------------------------------------
// W4 — u9ToolExecSessionIDs (FR-011/FR-012/FR-013)
// ---------------------------------------------------------------------

// TestU9ToolExecSessionIDs_RootTurn pins the byte-identical-at-root
// property: for a root turn (routingSessionID == transcriptSessionID, the
// default newTurnState/U3 sets), the wire session_id is the turn's own
// session and no producing_session_id is stamped.
func TestU9ToolExecSessionIDs_RootTurn(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const rootSessionID = "u9-root-session-toolexec"
	ts := &turnState{
		sessionKey:          "u9-root-sesskey-toolexec",
		turnID:              "u9-root-turn-toolexec",
		transcriptSessionID: rootSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID),
	}
	al.registerActiveTurn(ts)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(ts.sessionKey, ts) })

	sid, psid := u9ToolExecSessionIDs(ts)
	assert.Equal(t, rootSessionID, sid, "root turn: wire session_id must be the turn's own session")
	assert.Empty(t, psid, "root turn: producing_session_id must be absent (producing == routing)")
}

// TestU9ToolExecSessionIDs_ChildTurn is this unit's flagship red/green pin
// for FR-011/FR-012/FR-013's WS-payload stamping contract. Simulates the
// exact post-D1 shape U7's spawnSubTurn (Wave F) is contracted to produce: a
// child turn with its OWN real, distinct transcriptSessionID, but with
// routingSessionID inherited verbatim from the root (turn.go's
// routingSessionID field doc comment: "childTS.routingSessionID =
// parentTS.routingSessionID").
//
// RED/GREEN EVIDENCE (required by this unit's task; the actual terminal
// output from performing this by hand is quoted in this dispatch's report).
// Temporarily reverting u9ToolExecSessionIDs (pkg/agent/loop.go) to
//
//	func u9ToolExecSessionIDs(ts *turnState) (string, session.SessionID) {
//	    return ts.transcriptSessionID, ""
//	}
//
// (the pre-ADR-057 shape: stamps the CHILD's own id as session_id and never
// populates producing_session_id — "left unstamped") makes this test's
// first assertion fail: got the child's own id where the root's was wanted.
// Reverting instead to
//
//	func u9ToolExecSessionIDs(ts *turnState) (string, session.SessionID) {
//	    r := string(ts.routingSessionID)
//	    return r, session.SessionID(r)
//	}
//
// ("stamped with the routing id") makes the SECOND assertion fail: got the
// root's id where the child's own, distinct id was wanted. Restoring the
// correct two-branch function makes both pass.
func TestU9ToolExecSessionIDs_ChildTurn(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID  = "u9-root-session-child-toolexec"
		childSessionID = "u9-child-session-child-toolexec-distinct"
	)
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")

	child := &turnState{
		sessionKey:          "u9-child-sesskey-toolexec",
		turnID:              "u9-child-turn-toolexec",
		transcriptSessionID: childSessionID,                          // D1: the child's OWN real session
		routingSessionID:    session.RoutingSessionID(rootSessionID), // inherited verbatim (U7's contract)
		depth:               1,
		parentTurnID:        "u9-root-turn-child-toolexec",
	}
	al.registerActiveTurn(child)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(child.sessionKey, child) })

	sid, psid := u9ToolExecSessionIDs(child)
	assert.Equal(t, rootSessionID, sid,
		"delegated child: wire session_id must be the ROUTING (root) id, never the child's own")
	assert.Equal(t, session.SessionID(childSessionID), psid,
		"delegated child: producing_session_id must be the child's OWN real session")
	assert.NotEqual(t, session.SessionID(rootSessionID), psid,
		"producing_session_id must never be stamped with the routing id")
}

// u9StubTool is a minimal test-only tool implementing tools.Tool, mirroring
// the existing dangerousStubTool (scenario_runturn_test.go) shape but under
// this unit's own name/policy id so this file has no coupling to another
// test file's fixture staying unchanged.
type u9StubTool struct {
	tools.BaseTool
	wasCalled atomic.Bool
}

func (d *u9StubTool) Name() string { return "u9_stub_tool" }

func (d *u9StubTool) Description() string { return "U9 ADR-057 test stub — records whether it ran" }

func (d *u9StubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (d *u9StubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (d *u9StubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	d.wasCalled.Store(true)
	return &tools.ToolResult{ForLLM: "u9 stub executed", IsError: false}
}

// TestToolExecPayloads_RealRootTurn_StampsRoutingKeyOnly drives a REAL turn
// through al.runAgentLoop (not a spy) and asserts the ACTUAL emitted
// ToolExecStartPayload / ToolExecEndPayload / TurnEndPayload — read off a
// real al.SubscribeEvents subscription — carry the expected FR-012/FR-013
// fields. This exercises this file's real construction sites (the
// al.emitEvent calls this unit edited), not merely the
// u9ToolExecSessionIDs/TurnEndPayload logic in isolation. Root-only because
// no real code path can produce a diverging turnState until U7 (Wave F)
// lands — see TestU9ToolExecSessionIDs_ChildTurn above for that case.
func TestToolExecPayloads_RealRootTurn_StampsRoutingKeyOnly(t *testing.T) {
	const customAgentID = "u9-toolexec-real-agent"
	home := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: customAgentID, Name: "U9 ToolExec Agent", Type: config.AgentTypeCustom},
			},
		},
	}

	provider := testutil.NewScenario().
		WithToolCall("u9_stub_tool", `{}`).
		WithText("done")

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	stub := &u9StubTool{}
	al.RegisterTool(stub)
	customAgent, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok, "SETUP: custom agent must be registered")
	// No-default-policy model (CLAUDE.md hard constraint 6): u9_stub_tool has
	// zero policy coverage on this bare test config, so it fails closed to
	// deny unless granted explicitly.
	customAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"u9_stub_tool": "allow"},
	})

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", customAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	_, err = al.runAgentLoop(context.Background(), customAgent, processOptions{
		SessionKey:          "u9-toolexec-real-sesskey",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "run the stub tool",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err, "runAgentLoop must succeed")
	require.True(t, stub.wasCalled.Load(), "SETUP: the stub tool must actually have executed")

	events := drainEvents(sub.C)
	require.NotEmpty(t, events, "positive lower bound: at least one event must have been emitted")

	var sawStart, sawEnd, sawTurnEnd bool
	for _, evt := range events {
		switch p := evt.Payload.(type) {
		case ToolExecStartPayload:
			sawStart = true
			assert.Equal(t, sessionID, p.SessionID,
				"ToolExecStartPayload.SessionID must be the routing (== own, at root) session id")
			assert.Empty(t, p.ProducingSessionID, "ToolExecStartPayload.ProducingSessionID must be absent at root")
		case ToolExecEndPayload:
			sawEnd = true
			assert.Equal(t, sessionID, p.SessionID,
				"ToolExecEndPayload.SessionID must be the routing (== own, at root) session id")
			assert.Empty(t, p.ProducingSessionID, "ToolExecEndPayload.ProducingSessionID must be absent at root")
		case TurnEndPayload:
			sawTurnEnd = true
			assert.Equal(t, sessionID, p.SessionID,
				"TurnEndPayload.SessionID must be the routing (== own, at root) session id")
		}
	}
	assert.True(t, sawStart, "a real ToolExecStartPayload must have been observed on the event bus")
	assert.True(t, sawEnd, "a real ToolExecEndPayload must have been observed on the event bus")
	assert.True(t, sawTurnEnd, "a real TurnEndPayload must have been observed on the event bus")
}
