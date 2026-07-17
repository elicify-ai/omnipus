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

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
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
// binaries are never invoked — each test installs a fake driver
// (withFakeDriver) before dispatching to either worker.
func newGrantChainAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Provider:  "mock",
				Home:      tmpDir,
				ModelName: "test-model",
			},
			List: []config.AgentConfig{
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

// injectFakeCompletion sends a minimal successful output+end event pair into
// fr, then cancels it (closing its event channel) — the same fire-and-forget
// pattern pkg/agent/external_dispatch_test.go uses to let
// runExternalCLISubTurn's drain loop terminate promptly.
func injectFakeCompletion(fr *runner.FakeRunner, text string) {
	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: text}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()
}

// TestApprovalGrant_FullChainSpawnInheritDeny is the FR-D5 regression test:
// it chains Record -> a REAL (not mocked) spawnSubTurn call -> Inherit,
// proving the grant a parent holds is visible to the child it delegates to
// via the actual production delegation path (spawnSubTurn), not merely a
// hand-rolled Inherit() call as pkg/security/approvalgrants_test.go already
// exercises in isolation.
//
// Traces to: agent-delegation-spec.md FR-D5, BDD "The combined end-to-end path".
func TestApprovalGrant_FullChainSpawnInheritDeny(t *testing.T) {
	al := newGrantChainAgentLoop(t)
	fr, restore := withFakeDriver(t)
	defer restore()

	const sessionID = "S1"
	parentAgent := al.GetRegistry().GetDefaultAgent()
	if parentAgent == nil {
		t.Fatal("test setup: no default agent")
	}

	// 1. Parent records an "Always Allow" grant for "bash" in session S1.
	al.ApprovalGrants().Record(sessionID, parentAgent.ID, "bash")
	if !al.ApprovalGrants().IsAllowed(sessionID, parentAgent.ID, "bash") {
		t.Fatal("setup: parent grant must be recorded before delegating")
	}

	// Sanity: the child must NOT already show the grant before any delegation
	// happens — otherwise the assertion below would be vacuous.
	if al.ApprovalGrants().IsAllowed(sessionID, "child-agent", "bash") {
		t.Fatal("setup invariant broken: child-agent must not already have the grant")
	}

	// 2. The parent delegates to "child-agent" (a subagent_3p external-cli
	// worker) via a REAL spawnSubTurn call — async=false / await mode,
	// matching delegate(async=false) exactly (the same function delegate's
	// async mode also funnels through).
	injectFakeCompletion(fr, "hop done")
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-grant-chain",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parentAgent,
		transcriptSessionID: sessionID,
		agentID:             parentAgent.ID,
	}
	ctx := withSpawnToolCallID(context.Background(), "grant-chain-call-1")
	_, err := spawnSubTurn(ctx, al, parentTS, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "do the thing",
		TargetAgentID: "child-agent",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn: %v", err)
	}

	// 3. child-agent must now show the inherited "bash" grant — proving
	// Inherit fired correctly through the REAL production spawnSubTurn path
	// (parentTS.transcriptSessionID / parentTS.agentID -> the resolved
	// child's agent.ID, which subturn.go's external-cli identity-fix rebases
	// onto the TARGET's own ID), not just in a unit test that calls Inherit
	// directly.
	if !al.ApprovalGrants().IsAllowed(sessionID, "child-agent", "bash") {
		t.Fatal("expected child-agent to inherit the parent's 'bash' grant via the real spawnSubTurn -> Inherit call")
	}

	// 4. THE END-TO-END PROOF (ADR-036 §3.4, FR-D5): drive the actual approval
	// DECISION through AgentLoop.CheckGrantOrRequestApproval — the SOLE
	// consultation point now that the legacy WS-frame gate is gone — using the
	// child's REAL post-inheritance identity (sessionID, "child-agent"). A
	// neverCallApprover that fails the test if invoked proves the inherited
	// grant auto-approves "bash" without ever re-prompting.
	al.SetToolApprover(&neverCallApprover{t: t})
	approved, denialReason := al.CheckGrantOrRequestApproval(
		context.Background(), sessionID, "child-agent", "bash", "grant-chain-verify-1", "verify-turn-1", nil,
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
		context.Background(), sessionID, "child-agent", "read_file", "grant-chain-verify-2", "verify-turn-1", nil,
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
}

// TestApprovalGrant_TransitiveAcrossThreeLevels is the FR-D8 regression test:
// a grant held by a grandparent must be visible to a grandchild two
// delegation hops away, proven via two REAL (not mocked) spawnSubTurn calls
// chained end-to-end — grandparent delegates to parent (inherits), parent
// delegates to grandchild (inherits transitively, since Inherit copies the
// parent's ENTIRE current bucket, which already includes what it inherited).
//
// Traces to: agent-delegation-spec.md FR-D8, BDD "A grant flows transitively
// across a three-level delegation chain".
func TestApprovalGrant_TransitiveAcrossThreeLevels(t *testing.T) {
	al := newGrantChainAgentLoop(t)

	const sessionID = "S1"
	grandparentAgent := al.GetRegistry().GetDefaultAgent()
	if grandparentAgent == nil {
		t.Fatal("test setup: no default agent")
	}

	// 1. Grandparent (the default agent) records an "Always Allow" grant for
	// "bash" in session S1.
	al.ApprovalGrants().Record(sessionID, grandparentAgent.ID, "bash")

	// 2. Hop 1 — grandparent delegates to "child-agent" via a REAL
	// spawnSubTurn call (external-cli dispatch via a fake driver, so
	// child-agent's own identity is genuinely distinct from the grandparent's
	// — see the file-level DISCOVERY note on why native dispatch cannot be
	// used to prove this).
	fr1, restore1 := withFakeDriver(t)
	defer restore1()
	injectFakeCompletion(fr1, "hop1 done")

	hop1Parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "grandparent-turn",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               grandparentAgent,
		transcriptSessionID: sessionID,
		agentID:             grandparentAgent.ID,
	}
	hop1Ctx := withSpawnToolCallID(context.Background(), "grant-chain-hop-1")
	_, err := spawnSubTurn(hop1Ctx, al, hop1Parent, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "hop 1: delegate to child-agent",
		TargetAgentID: "child-agent",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("hop 1 spawnSubTurn: %v", err)
	}

	// Confirm hop 1's inheritance landed before proceeding to hop 2 — this is
	// the same assertion TestApprovalGrant_FullChainSpawnInheritDeny makes,
	// re-checked here because hop 2's correctness depends on it.
	if !al.ApprovalGrants().IsAllowed(sessionID, "child-agent", "bash") {
		t.Fatal("hop 1: child-agent must inherit the grandparent's grant before hop 2 can prove transitivity")
	}

	// 3. Hop 2 — "child-agent" (now itself a delegator) delegates onward to
	// "grandchild-agent" via a SECOND real spawnSubTurn call, using a fresh
	// fake driver (a FakeRunner's event channel is single-shot; its Cancel in
	// hop 1 closed it permanently). The turnState here represents the real
	// child turn hop 1 produced: same session, agentID "child-agent" (the
	// identity hop 1's external-cli rebase assigned it), depth 1.
	childAgentInstance, ok := al.GetRegistry().GetAgent("child-agent")
	if !ok {
		t.Fatal("child-agent must be registered")
	}
	fr2, restore2 := withFakeDriver(t)
	defer restore2()
	injectFakeCompletion(fr2, "hop2 done")

	hop2Parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "child-turn",
		depth:               1,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               childAgentInstance,
		transcriptSessionID: sessionID,
		agentID:             "child-agent",
	}
	hop2Ctx := withSpawnToolCallID(context.Background(), "grant-chain-hop-2")
	_, err = spawnSubTurn(hop2Ctx, al, hop2Parent, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "hop 2: delegate to grandchild-agent",
		TargetAgentID: "grandchild-agent",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("hop 2 spawnSubTurn: %v", err)
	}

	// 4. THE PROOF: grandchild-agent — two hops from the original grantor —
	// must show the inherited "bash" grant, with NO new inheritance logic
	// required (Inherit's copy-at-spawn semantics already carry it, since hop
	// 2's Inherit call copies child-agent's ENTIRE current bucket, which by
	// this point already includes what it inherited from the grandparent in hop 1).
	if !al.ApprovalGrants().IsAllowed(sessionID, "grandchild-agent", "bash") {
		t.Fatal(
			"expected grandchild-agent to inherit the grandparent's 'bash' grant transitively across two real spawnSubTurn hops",
		)
	}

	// 5. THE END-TO-END PROOF (ADR-036 §3.4): drive the actual approval
	// DECISION for grandchild-agent through AgentLoop.CheckGrantOrRequestApproval
	// — the SOLE consultation point now that the legacy WS-frame gate is gone
	// — proving the transitively-inherited grant auto-approves "bash" two
	// delegation hops away from the original grantor, without ever reaching
	// the interactive approver.
	al.SetToolApprover(&neverCallApprover{t: t})
	approved, denialReason := al.CheckGrantOrRequestApproval(
		context.Background(),
		sessionID,
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
}
