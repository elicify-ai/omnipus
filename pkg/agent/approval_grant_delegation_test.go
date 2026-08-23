// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// approval_grant_delegation_test.go — docs/internal/specs/agent-delegation-spec.md
// FR-D5 / FR-D8: the missing chained regression tests proving tool-approval
// grant inheritance (d0f65482's ApprovalGrantStore.Inherit) fires correctly
// when driven by a REAL (not hand-rolled) spawnSubTurn call — the same
// function both delegate's async and sync modes funnel through today.
//
// IMPORTANT DISCOVERY (backend-lead, ADR-036 delivery, 2026-07-04): a targeted
// delegate(agent_id="X") call only rebases the CHILD sub-turn's own identity
// (agent.ID, and therefore ApprovalGrantStore's childAgentID key) onto the
// named target when dispatch resolves to external-cli. For NATIVE dispatch —
// the common case — subturn.go deliberately leaves the child's AgentInstance
// "100% baseAgent-sourced" (ADR-032 fix note in pkg/agent/subturn.go), so
// agent.ID stays equal to the PARENT's own ID regardless of TargetAgentID.
// Concretely: al.ApprovalGrants().Inherit(parentTS.transcriptSessionID,
// parentTS.agentID, agent.ID) ends up being a same-identity union (a no-op in
// substance) for a native-dispatch named delegation — grant "inheritance" in
// that case is trivial because parent and child already share one bucket.
// The genuinely distinct-identity case these tests need to exercise a REAL
// spawnSubTurn call against is therefore external-cli dispatch (a
// subagent_3p-style target), which IS the code path that reassigns
// agent.ID = targetAgent.ID (subturn.go's ADR-032 FIX 1 block). Both tests
// below use a fake external-cli driver (mirroring
// pkg/agent/subturn_target_identity_test.go's established pattern) so the
// child's own identity genuinely differs from the parent's via the real,
// production dispatch-and-identity-resolution logic — not a hand-constructed
// mismatch that could never occur from a real turnState.
//
// UPDATE (ADR-036 §3.4, gate consolidation, 2026-07-04): the legacy WS-frame
// tool-approval gate (wsApprovalHook, pkg/gateway/ws_approval.go) has been
// DELETED — it ran before runTurn's TOCTOU "ask" branch and had become
// permanently unreachable once its answering frontend UI was removed. Grant
// consultation now lives SOLELY in AgentLoop.CheckGrantOrRequestApproval
// (pkg/agent/loop.go), which is exported specifically so it is directly
// callable from a test in THIS package — no cross-package pkg/gateway import
// needed anymore. The two tests below therefore no longer stop at proving
// Inherit populated the grant store; they go one step further and drive the
// actual approval DECISION through CheckGrantOrRequestApproval using the
// child's (or grandchild's) real post-inheritance identity, proving:
//   - a granted tool auto-approves without ever reaching the interactive
//     approver (agent-delegation-spec.md User Story 2: "the child's first
//     call to that tool does not prompt again"), and
//   - an UN-granted tool still reaches the interactive approver, so the
//     grant does not blanket-approve every tool the child might call.
//
// The "policy deny overrides an existing grant" / "policy allow never
// prompts" properties (independent of any grant) are covered by
// TestToolApproval_PolicyDenyOverridesGrant / _PolicyAllowNeverPrompts in
// pkg/gateway/ws_approval_grants_test.go, which exercise
// AgentLoop.ResolveApprovalToolPolicy — a plain-string-in/string-out method
// pkg/gateway can call without needing a *turnState.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// neverCallApprover is a PolicyApprover whose RequestApproval fails the test
// immediately if invoked. Used to prove a tool call was auto-approved via the
// grant store (AgentLoop.CheckGrantOrRequestApproval) without ever reaching
// the interactive approval path — the "no re-prompt" half of the grant
// contract.
type neverCallApprover struct{ t *testing.T }

func (n *neverCallApprover) RequestApproval(_ context.Context, req PolicyApprovalReq) (bool, string) {
	n.t.Helper()
	n.t.Fatalf(
		"RequestApproval must not be called for a granted tool (tool=%q, session=%q, agent=%q) — the grant should have auto-approved it",
		req.ToolName,
		req.SessionID,
		req.AgentID,
	)
	return false, "unreachable"
}

// scriptedApprover is a PolicyApprover that records every call it receives
// and returns a fixed outcome. Used to prove the interactive approval path IS
// reached for a tool that has no matching grant on file.
type scriptedApprover struct {
	mu     sync.Mutex
	calls  int
	result bool
	reason string
}

func (s *scriptedApprover) RequestApproval(_ context.Context, _ PolicyApprovalReq) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.result, s.reason
}

func (s *scriptedApprover) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// newGrantChainAgentLoop builds an AgentLoop with a default (native) agent
// plus two subagent_3p-style external-cli worker agents ("child-agent",
// "grandchild-agent") for the grant-inheritance chain tests below. Real CLI
// binaries are never invoked — each test installs a fake/blocking driver
// before dispatching to either worker.
//
// seedDistinctTestWorkspacesForIDs gives each worker its OWN solo workspace
// (called BEFORE mustNewAgentLoop's own ensureTestWorkspaceMembership, which
// leaves an already-covered id alone) — mirrors cancel_stress_test.go's
// identical need: without it, both workers fall into ADR-046 P1's shared
// default test-harness workspace and therefore the SAME resolved work/ dir,
// so TestApprovalGrant_TransitiveAcrossThreeLevels's two concurrently-active
// hops would serialize on external_dispatch.go's workspaceRunLocks — hop 2
// would not even reach its own driver until hop 1's dispatch released the
// lock, silently turning "prove the grant persists while BOTH hops are
// active" into "prove it survives until hop 1 times out".
func newGrantChainAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	seedDistinctTestWorkspacesForIDs(t, []string{"child-agent", "grandchild-agent"})
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "test-model"},
			},
			List: []config.AgentConfig{
				// The delegating PARENT: an ordinary, explicitly-registered
				// non-worker agent. No "main" sentinel to fall back to
				// anymore — GetDefaultAgent's Priority 2 skips workers, and
				// without this entry the only registered agents are BOTH
				// workers, so the degenerate all-workers fallback would
				// return "child-agent" itself as "the default agent",
				// collapsing this test's parent/child distinction.
				{
					ID:   "grant-chain-parent",
					Type: config.AgentTypeCore,
				},
				{
					ID:    "child-agent",
					Type:  config.AgentTypeWorker,
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
				{
					ID:    "grandchild-agent",
					Type:  config.AgentTypeWorker,
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	return mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
}

// TestApprovalGrant_FullChainSpawnInheritDeny is the FR-D5 regression test:
// it chains Record -> a REAL (not mocked) spawnSubTurn call -> InheritFrom,
// proving the grant a parent holds is visible to the child it delegates to
// via the actual production delegation path (spawnSubTurn), not merely a
// hand-rolled InheritFrom() call as pkg/security/approvalgrants_test.go
// already exercises in isolation.
//
// ADR-057 W10/FR-031 inversion `[grill C-1]`: the single-key Inherit(sessionID,
// srcAgentID, dstAgentID) this test used to drive was REMOVED, not
// re-parameterised — subturn.go now calls the two-key
// InheritFrom(parentTS.transcriptSessionID, parentTS.agentID, childID,
// agent.ID), which UNIONS the grant into the CHILD'S OWN session
// (subturn.go:939), never the shared parent session. The pre-ADR-057
// contract this test pinned — "the child shows the grant under the SAME
// session id the parent recorded it under" — is exactly what D1 (a
// delegated child owns its own real session) makes false: a child's
// transcriptSessionID is no longer the parent's shared id (FR-007/FR-009),
// so IsAllowed's own consumer (loop.go:8617, `ts.transcriptSessionID`) would
// look up the child's OWN session, not the parent's, when the delegate
// itself calls a tool. This test is inverted to assert the NEW invariant:
// the grant lands under the child's distinct session id, and — the negative
// space the old assertion could never express — is NOT visible under the
// parent's shared session for the child's identity, proving InheritFrom
// genuinely re-keyed rather than merely also-wrote the old location.
//
// Traces to: agent-delegation-spec.md FR-D5, BDD "The combined end-to-end path";
// ADR-057 spec Functional Requirements "Approvals and session teardown (W10)", FR-031.
//
// ADR-057 FR-033/W10d TIMING NOTE (found while inverting this test — not a
// separate grill finding): CloseSession("delegate_terminal") clears the
// CHILD's OWN grant set the instant its turn ends (subturn.go:1546) —
// intentional, so an inherited "Always Allow" grant does not outlive the
// child that received it. The pre-ADR-057 version of this test could check
// the grant AFTER spawnSubTurn returned because it checked the PARENT's
// session, which the child's own CloseSession never touches. Now that the
// assertion is correctly keyed to the CHILD's OWN session, checking after a
// synchronous spawnSubTurn call returns would be checking AFTER FR-033 has
// already cleared it. This test uses a BLOCKING external-cli driver
// (subturn_external_cancel_test.go's withBlockingDriver) to keep the child's
// dispatch genuinely in flight, observes the grant DURING that window
// (exactly like a real tool call the child makes mid-turn would), then
// explicitly ends the child's turn and asserts FR-033's clearing as its own,
// separate, consequential-semantics proof (US-18).
func TestApprovalGrant_FullChainSpawnInheritDeny(t *testing.T) {
	al := newGrantChainAgentLoop(t)
	driver, restore := withBlockingDriver(t)
	defer restore()

	parentAgent := al.GetRegistry().GetDefaultAgent()
	if parentAgent == nil {
		t.Fatal("test setup: no default agent")
	}

	// ADR-057 FR-005/FR-096 fixture repair: spawnSubTurn's
	// sharedStore.CreateSessionWithID(childID, parentTS.transcriptSessionID, ...)
	// requires the parent id to resolve to a REAL session in
	// al.GetSessionStore() — the old literal "S1" was never created there.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	parentMeta, err := store.NewSession(session.SessionTypeChat, "test-channel", parentAgent.ID)
	require.NoError(t, err)
	parentSessionID := parentMeta.ID

	// Corollary — distinct ids everywhere: the child's own session id is a
	// deliberately DIFFERENT, non-equal value from the parent's, pinned via
	// SubTurnConfig.DelegateSessionID so the test can assert on exactly which
	// session the grant landed under.
	const childSessionID = "grant-chain-child-1"
	require.NotEqual(t, parentSessionID, childSessionID, "parent and child session ids must be distinct")

	// 1. Parent records an "Always Allow" grant for "bash" in ITS OWN session.
	al.ApprovalGrants().Record(parentSessionID, parentAgent.ID, "bash", nil)
	if !al.ApprovalGrants().IsAllowed(parentSessionID, parentAgent.ID, "bash", nil) {
		t.Fatal("setup: parent grant must be recorded before delegating")
	}

	// Sanity: the child must NOT already show the grant under its own
	// (not-yet-created) session before any delegation happens — otherwise the
	// assertion below would be vacuous.
	if al.ApprovalGrants().IsAllowed(childSessionID, "child-agent", "bash", nil) {
		t.Fatal("setup invariant broken: child-agent must not already have the grant")
	}

	// 2. The parent delegates to "child-agent" (a subagent_3p external-cli
	// worker) via a REAL spawnSubTurn call — async=false / await mode,
	// matching delegate(async=false) exactly (the same function delegate's
	// async mode also funnels through). Launched on a goroutine (spawnSubTurn
	// itself blocks synchronously on the child's dispatch regardless of
	// Async) so the test can observe the child's grant WHILE its turn is
	// still genuinely active, before FR-033's terminal cleanup — see the
	// TIMING NOTE above.
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-grant-chain",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parentAgent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     store,
		agentID:             parentAgent.ID,
	}
	spawnDone := make(chan struct{})
	go func() {
		defer close(spawnDone)
		ctx := withSpawnToolCallID(context.Background(), "grant-chain-call-1")
		_, _ = spawnSubTurn(ctx, al, parentTS, SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "do the thing",
			TargetAgentID:     "child-agent",
			Async:             false,
			Timeout:           5 * time.Second,
			DelegateSessionID: childSessionID,
		})
	}()
	select {
	case <-driver.started:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: child-agent's external-cli driver never started")
	}

	// 3. child-agent must now show the inherited "bash" grant UNDER ITS OWN
	// SESSION, while its turn is still genuinely active — proving InheritFrom
	// fired correctly through the REAL production spawnSubTurn path
	// (parentTS.transcriptSessionID/agentID as source, childID/agent.ID as
	// destination — subturn.go:939), not just in a unit test that calls
	// InheritFrom directly.
	if !al.ApprovalGrants().IsAllowed(childSessionID, "child-agent", "bash", nil) {
		t.Fatal("expected child-agent to inherit the parent's 'bash' grant, keyed to its OWN session, via the real spawnSubTurn -> InheritFrom call")
	}
	// THE NEW-INVARIANT PROOF (negative space the pre-ADR-057 assertion could
	// never express): the grant must NOT be visible under the shared PARENT
	// session for the child's identity — InheritFrom re-keys to the child's
	// own session, it does not also leave a copy under the parent's.
	if al.ApprovalGrants().IsAllowed(parentSessionID, "child-agent", "bash", nil) {
		t.Fatal("child-agent's inherited grant must be keyed to its OWN session, not the shared parent session — " +
			"this is the D1 identity split InheritFrom (FR-031) exists to honor")
	}

	// 4. THE END-TO-END PROOF (ADR-036 §3.4, FR-D5): drive the actual approval
	// DECISION through AgentLoop.CheckGrantOrRequestApproval — the SOLE
	// consultation point now that the legacy WS-frame gate is gone — using the
	// child's REAL post-inheritance identity (its OWN session, "child-agent").
	// A neverCallApprover that fails the test if invoked proves the inherited
	// grant auto-approves "bash" without ever re-prompting.
	al.SetToolApprover(&neverCallApprover{t: t})
	approved, denialReason := al.CheckGrantOrRequestApproval(
		context.Background(), childSessionID, "child-agent", "bash", "grant-chain-verify-1", "verify-turn-1", nil,
	)
	if !approved {
		t.Fatalf(
			"expected the child's inherited grant to auto-approve 'bash' without prompting, got denied: %s",
			denialReason,
		)
	}

	// 5. Conversely, a tool the child was NEVER granted must still reach the
	// interactive approver — the inherited grant does not blanket-approve
	// every tool, only the exact (session, agent, tool) it was recorded for.
	scripted := &scriptedApprover{result: false, reason: "denied for test"}
	al.SetToolApprover(scripted)
	approved, denialReason = al.CheckGrantOrRequestApproval(
		context.Background(), childSessionID, "child-agent", "read_file", "grant-chain-verify-2", "verify-turn-1", nil,
	)
	if approved {
		t.Fatal("expected an ungranted tool to require interactive approval, not auto-approve")
	}
	if denialReason != "denied for test" {
		t.Fatalf("expected the scripted approver's denial reason to surface, got %q", denialReason)
	}
	if got := scripted.callCount(); got != 1 {
		t.Fatalf("expected the approver to be consulted exactly once for an ungranted tool, got %d calls", got)
	}

	// 6. End the child's turn — direct point-lookup by its own session key
	// (ScopeSelfOnly), mirroring session_messaging_wire.go's real
	// delegate(action="cancel") wiring exactly (al.InterruptSessionHard(sessionKey, ScopeSelfOnly, hint)).
	if _, err := al.InterruptSessionHard(childSessionID, ScopeSelfOnly, "test cleanup"); err != nil {
		t.Fatalf("InterruptSessionHard: %v", err)
	}
	select {
	case <-spawnDone:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: spawnSubTurn did not return after canceling the child")
	}
	require.True(t, driver.ctxCanceled.Load(), "the child's external-cli driver ctx must have been canceled")

	// 7. THE CONSEQUENTIAL-SEMANTICS PROOF (FR-033, US-18): once the child's
	// turn has genuinely ended, its inherited grant must be cleared — it must
	// not leak for the process lifetime of every ever-delegated child.
	if al.ApprovalGrants().IsAllowed(childSessionID, "child-agent", "bash", nil) {
		t.Fatal("expected child-agent's inherited grant to be cleared once its turn ended (FR-033 CloseSession)")
	}
}

// TestApprovalGrant_TransitiveAcrossThreeLevels is the FR-D8 regression test:
// a grant held by a grandparent must be visible to a grandchild two
// delegation hops away, proven via two REAL (not mocked) spawnSubTurn calls
// chained end-to-end — grandparent delegates to parent (inherits), parent
// delegates to grandchild (inherits transitively, since InheritFrom copies
// the parent's ENTIRE current bucket, which already includes what it
// inherited).
//
// ADR-057 W10/FR-031 inversion `[grill C-1]`: see the identical note on
// TestApprovalGrant_FullChainSpawnInheritDeny above — the single-key Inherit
// this test used to drive was removed for the two-key InheritFrom, which
// re-keys onto each delegate's OWN session rather than the shared
// grandparent session. Two consequences pinned here that a same-session
// design could never distinguish: (1) hop 1's grant lands under child-agent's
// OWN session, not the grandparent's; (2) hop 2's source read is therefore
// child-agent's OWN session (its inherited bucket), matching D1's own rule
// that a delegated child's transcriptSessionID is its own — hop2Parent below
// is built with transcriptSessionID = the CHILD's session from hop 1, mirroring
// exactly what the REAL childTS hop 1 produced would carry, not the
// grandparent's shared id.
//
// Traces to: agent-delegation-spec.md FR-D8, BDD "A grant flows transitively
// across a three-level delegation chain"; ADR-057 spec FR-031.
func TestApprovalGrant_TransitiveAcrossThreeLevels(t *testing.T) {
	al := newGrantChainAgentLoop(t)

	grandparentAgent := al.GetRegistry().GetDefaultAgent()
	if grandparentAgent == nil {
		t.Fatal("test setup: no default agent")
	}

	// ADR-057 FR-005/FR-096 fixture repair: see the identical note in
	// TestApprovalGrant_FullChainSpawnInheritDeny above.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	grandparentMeta, err := store.NewSession(session.SessionTypeChat, "test-channel", grandparentAgent.ID)
	require.NoError(t, err)
	grandparentSessionID := grandparentMeta.ID

	// Corollary — distinct ids everywhere: three deliberately different,
	// non-equal session ids across the chain.
	const childSessionID = "grant-chain-child-2"
	const grandchildSessionID = "grant-chain-grandchild-1"
	require.NotEqual(t, grandparentSessionID, childSessionID)
	require.NotEqual(t, childSessionID, grandchildSessionID)
	require.NotEqual(t, grandparentSessionID, grandchildSessionID)

	// 1. Grandparent (the default agent) records an "Always Allow" grant for
	// "bash" in ITS OWN session.
	al.ApprovalGrants().Record(grandparentSessionID, grandparentAgent.ID, "bash", nil)

	// 2. Hop 1 — grandparent delegates to "child-agent" via a REAL
	// spawnSubTurn call (external-cli dispatch via a BLOCKING driver, so
	// child-agent's own identity is genuinely distinct from the grandparent's
	// — see the file-level DISCOVERY note on why native dispatch cannot be
	// used to prove this — and its turn stays genuinely active long enough
	// for hop 2 to delegate onward from it before FR-033 tears it down; see
	// the TIMING NOTE on TestApprovalGrant_FullChainSpawnInheritDeny above).
	driver1, restore1 := withBlockingDriver(t)
	defer restore1()

	hop1Parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "grandparent-turn",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               grandparentAgent,
		transcriptSessionID: grandparentSessionID,
		routingSessionID:    session.RoutingSessionID(grandparentSessionID),
		transcriptStore:     store,
		agentID:             grandparentAgent.ID,
	}
	hop1Done := make(chan struct{})
	go func() {
		defer close(hop1Done)
		hop1Ctx := withSpawnToolCallID(context.Background(), "grant-chain-hop-1")
		_, _ = spawnSubTurn(hop1Ctx, al, hop1Parent, SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "hop 1: delegate to child-agent",
			TargetAgentID:     "child-agent",
			Async:             false,
			Timeout:           5 * time.Second,
			DelegateSessionID: childSessionID,
		})
	}()
	select {
	case <-driver1.started:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: hop 1's external-cli driver never started")
	}

	// Confirm hop 1's inheritance landed UNDER THE CHILD'S OWN SESSION before
	// proceeding to hop 2 — this is the same assertion
	// TestApprovalGrant_FullChainSpawnInheritDeny makes, re-checked here
	// because hop 2's correctness depends on it.
	if !al.ApprovalGrants().IsAllowed(childSessionID, "child-agent", "bash", nil) {
		t.Fatal("hop 1: child-agent must inherit the grandparent's grant, keyed to its OWN session, before hop 2 can prove transitivity")
	}

	// 3. Hop 2 — "child-agent" (now itself a delegator, still mid-turn)
	// delegates onward to "grandchild-agent" via a SECOND real spawnSubTurn
	// call, using its OWN blocking driver. The turnState here represents the
	// real child turn hop 1 produced: transcriptSessionID is the CHILD'S OWN
	// session (D1 — a delegated child's transcriptSessionID is its own, never
	// the parent's shared id), routingSessionID inherited verbatim from the
	// grandparent (FR-011, exactly as spawnSubTurn's own
	// `childTS.routingSessionID = parentTS.routingSessionID` would have set
	// it), agentID "child-agent" (the identity hop 1's external-cli rebase
	// assigned it), depth 1.
	childAgentInstance, ok := al.GetRegistry().GetAgent("child-agent")
	if !ok {
		t.Fatal("child-agent must be registered")
	}
	driver2, restore2 := withBlockingDriver(t)
	defer restore2()

	hop2Parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "child-turn",
		depth:               1,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               childAgentInstance,
		transcriptSessionID: childSessionID,
		routingSessionID:    session.RoutingSessionID(grandparentSessionID),
		transcriptStore:     store,
		agentID:             "child-agent",
	}
	hop2Done := make(chan struct{})
	go func() {
		defer close(hop2Done)
		hop2Ctx := withSpawnToolCallID(context.Background(), "grant-chain-hop-2")
		_, _ = spawnSubTurn(hop2Ctx, al, hop2Parent, SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "hop 2: delegate to grandchild-agent",
			TargetAgentID:     "grandchild-agent",
			Async:             false,
			Timeout:           5 * time.Second,
			DelegateSessionID: grandchildSessionID,
		})
	}()
	select {
	case <-driver2.started:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: hop 2's external-cli driver never started")
	}

	// 4. THE PROOF: grandchild-agent — two hops from the original grantor —
	// must show the inherited "bash" grant UNDER ITS OWN SESSION, while its
	// own turn is still genuinely active, with NO new inheritance logic
	// required (InheritFrom's copy-at-spawn semantics already carry it, since
	// hop 2's InheritFrom call copies child-agent's ENTIRE current bucket —
	// read from child-agent's OWN session — which by this point already
	// includes what it inherited from the grandparent in hop 1).
	if !al.ApprovalGrants().IsAllowed(grandchildSessionID, "grandchild-agent", "bash", nil) {
		t.Fatal(
			"expected grandchild-agent to inherit the grandparent's 'bash' grant transitively, keyed to its OWN session, across two real spawnSubTurn hops",
		)
	}
	// THE NEW-INVARIANT PROOF (negative space): the grant must not be visible
	// under either ancestor's shared session for the grandchild's identity.
	if al.ApprovalGrants().IsAllowed(grandparentSessionID, "grandchild-agent", "bash", nil) {
		t.Fatal("grandchild-agent's inherited grant must be keyed to its OWN session, not the grandparent's")
	}
	if al.ApprovalGrants().IsAllowed(childSessionID, "grandchild-agent", "bash", nil) {
		t.Fatal("grandchild-agent's inherited grant must be keyed to its OWN session, not its direct parent's")
	}

	// 5. THE END-TO-END PROOF (ADR-036 §3.4): drive the actual approval
	// DECISION for grandchild-agent through AgentLoop.CheckGrantOrRequestApproval
	// — the SOLE consultation point now that the legacy WS-frame gate is gone
	// — using the grandchild's REAL post-inheritance identity (its OWN
	// session), proving the transitively-inherited grant auto-approves "bash"
	// two delegation hops away from the original grantor, without ever
	// reaching the interactive approver.
	al.SetToolApprover(&neverCallApprover{t: t})
	approved, denialReason := al.CheckGrantOrRequestApproval(
		context.Background(),
		grandchildSessionID,
		"grandchild-agent",
		"bash",
		"grant-chain-verify-transitive",
		"verify-turn-1",
		nil,
	)
	if !approved {
		t.Fatalf(
			"expected grandchild-agent's transitively-inherited grant to auto-approve 'bash' without prompting, got denied: %s",
			denialReason,
		)
	}

	// 6. Teardown, deepest descendant first — cancel the grandchild, wait for
	// hop 2 to return, then cancel the child, wait for hop 1 to return. Each
	// cancel is a direct point-lookup by its own session key (ScopeSelfOnly),
	// mirroring session_messaging_wire.go's real delegate(action="cancel")
	// wiring.
	if _, err := al.InterruptSessionHard(grandchildSessionID, ScopeSelfOnly, "test cleanup"); err != nil {
		t.Fatalf("InterruptSessionHard (grandchild): %v", err)
	}
	select {
	case <-hop2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: hop 2 spawnSubTurn did not return after canceling the grandchild")
	}
	require.True(t, driver2.ctxCanceled.Load(), "the grandchild's external-cli driver ctx must have been canceled")
	// FR-033 consequential-semantics proof for the grandchild (US-18) —
	// mirrors TestApprovalGrant_FullChainSpawnInheritDeny's step 7.
	if al.ApprovalGrants().IsAllowed(grandchildSessionID, "grandchild-agent", "bash", nil) {
		t.Fatal("expected grandchild-agent's inherited grant to be cleared once its turn ended (FR-033 CloseSession)")
	}

	if _, err := al.InterruptSessionHard(childSessionID, ScopeSelfOnly, "test cleanup"); err != nil {
		t.Fatalf("InterruptSessionHard (child): %v", err)
	}
	select {
	case <-hop1Done:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: hop 1 spawnSubTurn did not return after canceling the child")
	}
	require.True(t, driver1.ctxCanceled.Load(), "the child's external-cli driver ctx must have been canceled")
}
