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
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
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

func (d *u9StubTool) Name() string        { return "u9_stub_tool" }
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

// ---------------------------------------------------------------------
// W10b — CheckGrantOrRequestApproval's grant read (FR-031/FR-080)
// ---------------------------------------------------------------------

// TestCheckGrantOrRequestApproval_UsesActingSessionKey is this unit's
// regression guard for the cross-unit obligation FROM U17a (Integration
// order, cross-unit requests table): "the two-key grant read — IsAllowed
// under the child's own session key". U17a's InheritFrom (Wave A,
// pkg/security/approvalgrants.go) copies a spawn-time grant from a SOURCE
// {sessionID, agentID} into a DESTINATION {sessionID, agentID} — never into
// a shared/routing key. This test proves loop.go's READ side
// (CheckGrantOrRequestApproval, which every ask-policy tool call in runTurn
// goes through — see its own doc comment's "Identity" paragraph, corrected
// by this unit) resolves an inherited grant when queried under the CHILD's
// own (acting) session id, and — the discriminating negative control — does
// NOT resolve it under the ROOT's session id, the value
// turnState.routingSessionID would hold for this same child (FR-011:
// "inherited verbatim from the root of its delegation subtree"). This is
// the exact mistake FR-014's closed-consumer-set rule exists to prevent:
// routingSessionID must never be read as an approval-grant key.
//
// The three ids are deliberately NOT a simple two-hop parent/child pair: the
// ROOT never records or receives any grant of its own (unlike the immediate
// PARENT, whose own direct "Always Allow" is legitimately valid for the
// parent's own future calls and would make a parent-keyed read succeed for
// an unrelated, correct reason — that would NOT discriminate anything). The
// ROOT is the only id in this fixture with zero grants under it by
// construction, so approved==true there could only mean the read leaked
// across keys.
//
// No PolicyApprover is wired on this AgentLoop, so a query that misses the
// grant-store short-circuit reaches loadToolApprover's nopPolicyApprover
// fallback, which ALWAYS denies with reason "no_approver_configured"
// (tool_approver.go's V2.B fail-closed default, never the pre-V2.B
// auto-approve). approved==true is therefore only reachable via a real
// grant-store hit — a genuine production fallback proves the read, not a
// spy recording its argument.
func TestCheckGrantOrRequestApproval_UsesActingSessionKey(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID   = "u9-root-session-grant-uninvolved"
		parentSessionID = "u9-parent-session-grant"
		childSessionID  = "u9-child-session-grant-distinct"
		agentID         = "u9-grant-agent"
		toolName        = "u9_grant_tool"
	)
	require.NotEqual(t, parentSessionID, childSessionID, "fixture defect: parent and child ids must be distinct")
	require.NotEqual(t, rootSessionID, parentSessionID, "fixture defect: root and parent ids must be distinct")
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")

	grants := security.NewApprovalGrantStore()
	// The immediate PARENT recorded "Always Allow" under its OWN session
	// id — the root itself never did...
	require.True(t, grants.Record(parentSessionID, agentID, toolName, nil))
	// ...and InheritFrom (U17a) copies it into the CHILD's OWN session id at
	// spawn — mirroring what U7's spawnSubTurn call site (Wave F) will do.
	// routingSessionID for this same child would be the ROOT's id (FR-011),
	// never the parent's — InheritFrom's source here is the immediate
	// parent, which is a DIFFERENT value from routing the moment delegation
	// is two levels deep, exactly the case this test's negative control
	// exercises against the root.
	grants.InheritFrom(parentSessionID, agentID, childSessionID, agentID)
	al.approvalGrants = grants

	// Positive lower bound (Rule 4): the grant genuinely exists under the
	// child's key, via the store's own read, before the code under test runs.
	require.True(t, grants.IsAllowed(childSessionID, agentID, toolName, nil),
		"SETUP: InheritFrom must have copied the grant into the child's own key")

	approved, reason := al.CheckGrantOrRequestApproval(
		context.Background(), childSessionID, agentID, toolName, "u9-tc-1", "u9-turn-1", nil)
	assert.True(t, approved, "grant read keyed on the child's own (acting) session id must resolve the inherited grant")
	assert.Empty(t, reason)

	// Discriminating negative control: reading under the ROOT's session id —
	// the value a buggy caller would pass if it read routingSessionID
	// instead of transcriptSessionID for this child — must NOT resolve. The
	// root was never granted anything, directly or via inheritance, so any
	// approved==true here could only mean the read crossed keys.
	approvedWrongKey, reasonWrongKey := al.CheckGrantOrRequestApproval(
		context.Background(), rootSessionID, agentID, toolName, "u9-tc-2", "u9-turn-2", nil)
	assert.False(t, approvedWrongKey, "reading under the root's (routing-shaped) session id must NOT find the child's inherited grant")
	assert.Equal(t, nopApproverDenialReason, reasonWrongKey,
		"a miss must fall through to the fail-closed nopPolicyApprover, never silently approve")
}

// ---------------------------------------------------------------------
// W16b — ListAllSessions pagination, ordering, hierarchy
// (FR-091/FR-097/FR-098/FR-104)
// ---------------------------------------------------------------------

// TestListAllSessions_PagesStablyAcrossSharedStore is this unit's positive
// lower bound plus window/stability pin for FR-098(a)/(b), mirroring
// pkg/session's TestListSessionsPage_WindowAndStability (U6, store layer) at
// the layer that merges across stores.
func TestListAllSessions_PagesStablyAcrossSharedStore(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	store := u9IsolateSharedStore(t, al)

	const n = 5
	for i := 0; i < n; i++ {
		_, err := store.NewSession(session.SessionTypeChat, "", "u9-page-agent")
		require.NoError(t, err)
	}

	full, errs := al.ListAllSessions(0, 0, "", false)
	require.Empty(t, errs)
	require.Len(t, full.Sessions, n, "positive lower bound: all N sessions must be listed before paging them")
	assert.Equal(t, -1, full.NextOffset, "an unlimited page must report no further offset")
	assert.Equal(t, n, full.Total)

	page1, errs := al.ListAllSessions(2, 0, "", false)
	require.Empty(t, errs)
	require.Len(t, page1.Sessions, 2)
	assert.Equal(t, 2, page1.NextOffset)
	assert.Equal(t, n, page1.Total)

	page2, errs := al.ListAllSessions(2, page1.NextOffset, "", false)
	require.Empty(t, errs)
	require.Len(t, page2.Sessions, 2)
	assert.Equal(t, 4, page2.NextOffset)

	page3, errs := al.ListAllSessions(2, page2.NextOffset, "", false)
	require.Empty(t, errs)
	require.Len(t, page3.Sessions, 1)
	assert.Equal(t, -1, page3.NextOffset, "the final page must report no further offset")

	// Stability (FR-098(b)): concatenating the pages, in order, must
	// reproduce the SAME sequence of ids the unpaged call returned — no
	// duplicate, no skip, no reorder — across independent calls with no
	// intervening write.
	paged := make([]string, 0, len(page1.Sessions)+len(page2.Sessions)+len(page3.Sessions))
	for _, m := range page1.Sessions {
		paged = append(paged, m.ID)
	}
	for _, m := range page2.Sessions {
		paged = append(paged, m.ID)
	}
	for _, m := range page3.Sessions {
		paged = append(paged, m.ID)
	}
	unpaged := make([]string, 0, len(full.Sessions))
	for _, m := range full.Sessions {
		unpaged = append(unpaged, m.ID)
	}
	assert.Equal(t, unpaged, paged, "concatenated pages must reproduce the unpaged sequence exactly")

	// Out-of-range offset (FR-098(b)): empty, non-nil page, NextOffset==-1,
	// not an error.
	oor, errs := al.ListAllSessions(2, 100, "", false)
	require.Empty(t, errs)
	assert.NotNil(t, oor.Sessions, "an out-of-range offset must return a non-nil, empty page")
	assert.Empty(t, oor.Sessions)
	assert.Equal(t, -1, oor.NextOffset)
}

// TestU9SessionRecencyLess_TiesBreakOnID pins FR-098(a)'s exact comparator:
// UpdatedAt descending, id ascending as the tiebreak for two sessions
// sharing a timestamp — the case a real filesystem clock cannot reliably
// reproduce on demand, so the comparator is tested directly against
// hand-built (but real-typed, non-spy) session.UnifiedMeta values carrying
// an EXACT tie, exactly mirroring UnifiedStore.ListSessions' own already-
// tested comparator shape (pkg/session/unified.go) at the layer that merges
// across stores.
func TestU9SessionRecencyLess_TiesBreakOnID(t *testing.T) {
	now := time.Now()
	a := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "u9-tie-aaa", UpdatedAt: now}}
	b := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "u9-tie-bbb", UpdatedAt: now}}
	require.True(t, a.UpdatedAt.Equal(b.UpdatedAt), "fixture defect: timestamps must be an exact tie")
	require.NotEqual(t, a.ID, b.ID, "fixture defect: ids must be distinct")

	assert.True(t, u9SessionRecencyLess(a, b), "on a tie, the lexicographically smaller id must sort first")
	assert.False(t, u9SessionRecencyLess(b, a), "the comparator must be antisymmetric on a tie")

	newer := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "u9-tie-zzz", UpdatedAt: now.Add(time.Second)}}
	assert.True(t, u9SessionRecencyLess(newer, a),
		"a strictly newer UpdatedAt must sort first regardless of id ordering")
}

// TestListAllSessions_HierarchyRootsOrphansAndParentFilter is this unit's
// pin for FR-091/FR-104's three hierarchy cases u9FilterSessionHierarchy
// implements.
func TestListAllSessions_HierarchyRootsOrphansAndParentFilter(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	store := u9IsolateSharedStore(t, al)

	root, err := store.NewSession(session.SessionTypeChat, "", "u9-hier-agent")
	require.NoError(t, err)
	child, err := store.CreateSessionWithID("u9-hier-child-distinct", root.ID, session.SessionTypeDelegate, "", "u9-hier-agent")
	require.NoError(t, err)
	require.NotEqual(t, root.ID, child.ID, "fixture defect: root and child ids must be distinct")
	// FINDING (verified 2026-08-03, not this unit's file to fix):
	// CreateSessionWithID (pkg/session/unified_api.go, U2) takes parentID as
	// a parameter but only uses it to inherit Owner (FR-006/FR-082) — it
	// never persists ParentSessionID on the child, despite FR-005/FR-008
	// requiring every delegated child to carry it. `rg ParentSessionID
	// pkg/session/unified_api.go` returns zero matches. Until U7's spawnSubTurn
	// (Wave F) call site — or U2 itself — closes this gap, a real caller must
	// make this follow-up SetMeta call explicitly, exactly as done here, for
	// FR-091's roots/children split to see the edge at all. Flagged in this
	// unit's dispatch report for U7/U2/main; pkg/session/unified_api.go is
	// U2's exclusive file, not touched here.
	parentID := root.ID
	require.NoError(t, store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parentID}))

	// An orphan: ParentSessionID names a session absent from the whole merge
	// (FR-091's "no longer resolves" clause).
	orphan, err := store.NewSession(session.SessionTypeDelegate, "", "u9-hier-agent")
	require.NoError(t, err)
	missingParent := "u9-hier-vanished-parent-" + orphan.ID
	require.NoError(t, store.SetMeta(orphan.ID, session.MetaPatch{ParentSessionID: &missingParent}))

	// Positive lower bound: all three are genuinely present in the merge
	// before any hierarchy filtering is asserted.
	flatPage, errs := al.ListAllSessions(0, 0, "", true)
	require.Empty(t, errs)
	require.Len(t, flatPage.Sessions, 3, "flat=true must return every merged session regardless of hierarchy")

	rootsPage, errs := al.ListAllSessions(0, 0, "", false)
	require.Empty(t, errs)
	rootIDs := make([]string, 0, len(rootsPage.Sessions))
	for _, m := range rootsPage.Sessions {
		rootIDs = append(rootIDs, m.ID)
	}
	assert.Contains(t, rootIDs, root.ID, "a genuine root must be listed")
	assert.Contains(t, rootIDs, orphan.ID,
		"an orphan (unresolvable parent) must be returned AS A ROOT, not dropped (FR-091)")
	assert.NotContains(t, rootIDs, child.ID, "a real, resolvable child must NOT appear in the roots-only view")

	childrenPage, errs := al.ListAllSessions(0, 0, root.ID, false)
	require.Empty(t, errs)
	require.Len(t, childrenPage.Sessions, 1, "parent_session_id filter must return exactly the direct children")
	assert.Equal(t, child.ID, childrenPage.Sessions[0].ID)
}

// TestListAllSessions_PartialErrorDoesNotHaltPage is FR-098(c)'s dedicated
// pin: a legacy per-agent store that errors mid-merge contributes zero rows
// and is appended to errs, but the page itself — including its
// NextOffset/cursor — is unaffected. Uses the same real
// permission-revocation failure injection as the pre-existing
// TestListAllSessions_PartialErrors (list_all_sessions_test.go, updated by
// this unit for the new signature) rather than a fake store, so the error
// is genuine I/O, not a spy.
func TestListAllSessions_PartialErrorDoesNotHaltPage(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission-based failure injection is ineffective under root; run as non-root")
	}

	al, cleanup := newAL(t)
	defer cleanup()
	sharedStore := u9IsolateSharedStore(t, al)

	// One good session in the shared store.
	goodMeta, err := sharedStore.NewSession(session.SessionTypeChat, "", "u9-partial-good")
	require.NoError(t, err)

	// One broken legacy per-agent store, wired the same way
	// list_all_sessions_test.go's pre-existing coverage does.
	brokenBaseDir := t.TempDir()
	brokenStore, err := session.NewUnifiedStore(brokenBaseDir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(brokenBaseDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(brokenBaseDir, 0o700) })

	brokenAgentID := "u9-partial-broken-agent"
	seedTestWorkspaceMembershipForIDs(t, []string{brokenAgentID})
	al.registry.mu.Lock()
	al.registry.agents[brokenAgentID] = NewAgentInstance(
		&config.AgentConfig{ID: brokenAgentID, Name: brokenAgentID},
		&al.cfg.Agents.Defaults, al.cfg, &mockProvider{},
	)
	al.registry.agents[brokenAgentID].Sessions = brokenStore
	al.registry.mu.Unlock()

	page, errs := al.ListAllSessions(0, 0, "", true)
	require.Len(t, errs, 1, "the broken legacy store must contribute exactly one partial error")
	assert.NotEmpty(t, errs[0].Error())

	ids := make([]string, 0, len(page.Sessions))
	for _, m := range page.Sessions {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, goodMeta.ID, "the good session must still be returned despite the partial error")
	assert.Equal(t, -1, page.NextOffset, "an unlimited page must still report a valid (terminal) cursor, not fail")
}
