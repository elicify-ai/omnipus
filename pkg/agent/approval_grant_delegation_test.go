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
// Scope note (read before extending): the "child's own deny policy overrides
// an inherited grant" ordering property is implemented in
// wsApprovalHook.ApproveTool (pkg/gateway/ws_approval.go) — policy is
// resolved FIRST and short-circuits before the grant store is ever consulted.
// That property is already independently verified by
// pkg/gateway/ws_approval_grants_test.go (TestApproveTool_PolicyDenyOverridesGrant,
// TestApproveTool_PolicyAllowNeverPrompts, TestApproveTool_AskWithGrantAutoApproves)
// and is UNCHANGED by this spec (task instructions explicitly forbid modifying
// ws_approval.go as part of this merge). pkg/gateway imports pkg/agent — so a
// pkg/agent test file importing pkg/gateway back would pull the entire gateway
// package (and its goolm/matrix build weight the project explicitly guards
// against linking gratuitously, see CLAUDE.md's CI guidance) into every
// `go test ./pkg/agent/...` run. These two tests therefore prove the NEW part
// specific to this merge — that Inherit fires correctly via the real
// spawnSubTurn call path, including transitively across three real hops — and
// rely on the pre-existing pkg/gateway tests for the policy-ordering half.
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

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
				Workspace: tmpDir,
				ModelName: "test-model",
			},
			List: []config.AgentConfig{
				{
					ID:        "child-agent",
					Type:      config.AgentTypeWorker,
					Workspace: tmpDir,
					Model:     &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
				{
					ID:        "grandchild-agent",
					Type:      config.AgentTypeWorker,
					Workspace: tmpDir,
					Model:     &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
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

	// 4. The child's own `deny` policy for "bash" would still deny the call
	// despite this inherited grant — that ordering (policy resolved FIRST,
	// short-circuiting before IsAllowed is ever consulted) lives in
	// wsApprovalHook.ApproveTool (pkg/gateway/ws_approval.go), is unchanged by
	// this spec, and is independently verified by
	// TestApproveTool_PolicyDenyOverridesGrant (pkg/gateway/ws_approval_grants_test.go).
	// See the file-level scope note for why it is not re-verified here.
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
		t.Fatal("expected grandchild-agent to inherit the grandparent's 'bash' grant transitively across two real spawnSubTurn hops")
	}
}
