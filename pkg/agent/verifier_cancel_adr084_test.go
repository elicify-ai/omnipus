// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_cancel_adr084_test.go — ADR-084 revision 7/9, JUDGE-FR-082 and
// FR-103 (wave T4, round 7 of the ADR-084/085/086 joint delivery plan §3).
//
// FR-082: a tool-using verifier turn now runs up to 420s in the background,
// AFTER delivery (D13). A cancel against the adjudicating verifier session
// MUST interrupt the turn mid-tool-call and produce no verdict — the
// cancelled adjudication is discarded whole.
//
// FR-103: the handle a cancel reaches the verifier turn THROUGH is the
// verifier_registry entry runVerifierAdjudication registers before dispatch
// — the SAME handle StopPlan/StopTask/`/goal clear` already fan out to via
// RequestCancelForSession. A cancel addressed to the operator's own CHAT
// session must NOT be expected to reach it — there is no chat turn to
// interrupt once the adjudication is deferred past delivery.
//
// Method: this deliberately does NOT block inside the verifier's own Chat()
// call (verifier_cancel_key_test.go already proves cancellation reaches
// that). It blocks inside a REAL Judge tool call (a "read_file" double
// registered onto the Judge's own tool registry, mirroring judge_test.go's
// fakeBashTool pattern) so the assertion matches the BDD scenario's own
// wording — "a background verifier turn mid-`read_file`" — literally, not
// by analogy.
//
// Oracle discipline: every assertion is derived from the judge spec's own
// BDD scenarios ("a cancelled verifier turn produces no verdict", Traces to
// FR-082; "a background adjudication is cancellable through the verifier
// registry", Traces to FR-103/FR-082/E-34) and from FR-082/FR-103's own
// prose, never from reading runVerifierAdjudication's behaviour back.
//
// D-B / C-01 / C-18 compliance: cancellation produces NO verdict, so there
// is no criterion outcome to mis-assert anywhere in this file. No
// `unable_to_verify`, no fourth status, no `outcome` field appears here.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// blockingReadFileTool is a Tool double standing in for the Judge's real
// read_file grant. Execute blocks until its OWN ctx is done, signalling
// `started` exactly once (a test waits on it to know the tool call is
// genuinely in flight before firing a cancel) and reporting the ctx error
// it observed on `ctxErr` — proof the SAME context that reached the tool
// call, not merely some unrelated one, was the one interrupted.
type blockingReadFileTool struct {
	startedOnce sync.Once
	started     chan struct{}
	ctxErr      chan error
}

func newBlockingReadFileTool() *blockingReadFileTool {
	return &blockingReadFileTool{started: make(chan struct{}), ctxErr: make(chan error, 8)}
}

func (b *blockingReadFileTool) Name() string { return "read_file" }
func (b *blockingReadFileTool) Description() string {
	return "blocking read_file test double (ADR-084 T4)"
}
func (b *blockingReadFileTool) Parameters() map[string]any { return nil }
func (b *blockingReadFileTool) Scope() tools.ToolScope     { return tools.ScopeGeneral }
func (b *blockingReadFileTool) Category() tools.ToolCategory {
	return tools.CategoryFilesystem
}

func (b *blockingReadFileTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	b.startedOnce.Do(func() { close(b.started) })
	<-ctx.Done()
	err := ctx.Err()
	select {
	case b.ctxErr <- err:
	default:
	}
	return &tools.ToolResult{ForLLM: "interrupted", IsError: true, Err: err}
}

// allowReadFilePolicy grants agentInst's "read_file" tool policy "allow" —
// mirrors judge_test.go's allowBashPolicy for the one extra tool these
// tests need the Judge to actually be permitted to call (these minimal test
// configs have no policy entries at all, which resolves to deny fail-closed
// per CLAUDE.md hard constraint 6).
func allowReadFilePolicy(agentInst *AgentInstance) {
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow},
	})
}

// alwaysReadFileToolCall builds the ONE scripted tool-call response every
// verifier LLM turn in this file's tests returns — every attempt (including
// any same-process retry that might slip in before an outer-ctx cancel
// lands) asks to read_file again, so the tool double blocks on ITS OWN
// fresh ctx every time and no attempt can ever manufacture a real verdict.
func alwaysReadFileToolCall(int) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:        "verifier-read-adr084-t4",
			Name:      "read_file",
			Arguments: map[string]any{"path": "README.md"},
		}},
	}, nil
}

// setUpBlockedVerifierTurn wires a Judge whose own tool-using turn is
// genuinely blocked mid-`read_file`, and returns the running
// JudgeCriteria call's result channel, the blocking tool double, the
// outer-ctx cancel func (call it AFTER confirming cancellation reached the
// tool, to make the retry loop converge deterministically without ever
// producing a real verdict), and the verifier-session registry's real
// handle for looking up what got registered.
func setUpBlockedVerifierTurn(t *testing.T, taskID string) (
	al *AgentLoop,
	resultCh chan JudgeCriteriaResult,
	blockTool *blockingReadFileTool,
	cancelOuter context.CancelFunc,
	registry VerifierSessionRegistry,
) {
	t.Helper()

	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error {
		return ctx.Err() // never a real sleep; give up the instant ctx is cancelled
	}

	var judgeInst *AgentInstance
	al, judgeInst = newGoalLoopTestLoop(t, &mockProvider{}, nil)
	allowReadFilePolicy(judgeInst)

	blockTool = newBlockingReadFileTool()
	judgeInst.Tools.RegisterReplacing(blockTool) // overwrites the real ReadFileTool entry

	fake := &fakeJudgeProvider{chatFn: alwaysReadFileToolCall}
	judgeInst.Provider = fake

	outerCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelOuter = cancel

	resultCh = make(chan JudgeCriteriaResult, 1)
	go func() {
		resultCh <- al.JudgeCriteria(outerCtx, JudgeCriteriaInput{
			Scope:           task.VerdictScopeTask,
			TaskID:          taskID,
			AssigneeAgentID: "native-agent",
			WorkspaceID:     "ws-adr084-t4-cancel",
			Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the file was reviewed")},
			Attempt:         1,
			ClaimText:       "done",
		})
	}()

	select {
	case <-blockTool.started:
		// the tool call is genuinely mid-flight
	case <-time.After(10 * time.Second):
		t.Fatal("verifier turn never reached read_file (tool call never dispatched)")
	}

	seam := currentVerifierSessionRegistry()
	richer, ok := seam.(VerifierSessionRegistry)
	require.Truef(t, ok, "verifier-session registry (%T) does not implement VerifierSessionRegistry (no Lookup/SessionsFor)", seam)
	return al, resultCh, blockTool, cancelOuter, richer
}

// TestVerifierTurn_CancelDuringToolCallProducesNoVerdict is the Test
// Matrix's named oracle for FR-082
// (judge-active-reviewer-spec.md's "TestVerifierTurn_CancelDuringToolCallProducesNoVerdict").
func TestVerifierTurn_CancelDuringToolCallProducesNoVerdict(t *testing.T) {
	const taskID = "t-fr082-cancel-mid-tool"
	unitID := verifierUnitForTask(taskID)

	al, resultCh, blockTool, cancelOuter, registry := setUpBlockedVerifierTurn(t, taskID)

	sessions := registry.SessionsFor(unitID)
	require.Lenf(t, sessions, 1, "exactly one verifier session must be registered for %q while its turn is mid-tool-call", unitID)
	registeredSessionID := sessions[0]

	fired, _, err := al.RequestCancelForSession(context.Background(), registeredSessionID, "tester", "test")
	require.NoError(t, err)
	require.Truef(t, fired, "RequestCancelForSession(%q) must fire against the registered verifier session (FR-103)", registeredSessionID)

	select {
	case gotErr := <-blockTool.ctxErr:
		assert.ErrorIs(t, gotErr, context.Canceled,
			"FR-082: the read_file tool call's OWN ctx must be cancelled — the turn is interrupted "+
				"without waiting for the tool call to finish")
	case <-time.After(5 * time.Second):
		t.Fatal("FR-082: RequestCancelForSession fired, but the in-flight read_file tool call's ctx was never interrupted")
	}

	// Deterministic convergence: tear down the outer ctx too, so the
	// same-process retry loop's backoff wait (judgeSleepFn, stubbed above
	// to return ctx.Err() with no real sleep) gives up on its NEXT
	// iteration rather than dispatching another blocked read_file forever.
	cancelOuter()

	select {
	case result := <-resultCh:
		assert.Truef(t, result.Unavailable, "FR-082: a cancelled adjudication must be Unavailable, got %+v", result)
		assert.Nilf(t, result.Verdict, "FR-082: no verdict may be produced from a cancelled turn, got %+v", result.Verdict)
	case <-time.After(10 * time.Second):
		t.Fatal("JudgeCriteria never returned after cancellation")
	}

	_, held := registry.Lookup(unitID)
	assert.Falsef(t, held, "FR-082: the cancelled adjudication must be discarded whole — no live session left registered for %q", unitID)
}

// TestBackgroundAdjudication_CancelledViaVerifierRegistry is the Test
// Matrix's named oracle for FR-103
// (judge-active-reviewer-spec.md's "TestBackgroundAdjudication_CancelledViaVerifierRegistry"):
// the positive half (a cancel via the registered verifier session reaches a
// background adjudication) AND the negative half in the SAME BDD scenario
// ("a cancel addressed to the operator's chat session does not reach it —
// there is no chat turn to interrupt").
func TestBackgroundAdjudication_CancelledViaVerifierRegistry(t *testing.T) {
	const taskID = "t-fr103-registry-cancel"
	unitID := verifierUnitForTask(taskID)

	al, resultCh, blockTool, cancelOuter, registry := setUpBlockedVerifierTurn(t, taskID)

	sessions := registry.SessionsFor(unitID)
	require.Lenf(t, sessions, 1, "exactly one verifier session must be registered for %q", unitID)
	registeredSessionID := sessions[0]

	// Negative half FIRST (Rule 4): a cancel addressed to an unrelated
	// session id — standing in for "the operator's own chat session", which
	// this deferred-dispatch adjudication has no live turn under at all —
	// must NOT fire and must NOT reach the blocked tool call.
	const unrelatedOperatorChatSessionID = "operator-chat-session-unrelated-to-verifier"
	fired, _, err := al.RequestCancelForSession(context.Background(), unrelatedOperatorChatSessionID, "tester", "test")
	require.NoError(t, err)
	assert.Falsef(t, fired,
		"FR-103: RequestCancelForSession against an unrelated (operator-chat-shaped) session id must NOT "+
			"fire — there is no chat turn for a deferred, background adjudication to interrupt")
	select {
	case gotErr := <-blockTool.ctxErr:
		t.Fatalf("FR-103: the read_file tool call must NOT have been interrupted by the unrelated cancel, got ctx err: %v", gotErr)
	case <-time.After(300 * time.Millisecond):
		// still blocked, as required — no signal within the settle window
	}

	// Positive half: the SAME registered verifier session id DOES reach it.
	fired, _, err = al.RequestCancelForSession(context.Background(), registeredSessionID, "tester", "test")
	require.NoError(t, err)
	require.Truef(t, fired, "FR-103: RequestCancelForSession(%q) must fire against the registered verifier session", registeredSessionID)

	select {
	case gotErr := <-blockTool.ctxErr:
		assert.ErrorIs(t, gotErr, context.Canceled,
			"FR-103: the verifier registry's registered session id must be the handle that actually reaches the in-flight tool call")
	case <-time.After(5 * time.Second):
		t.Fatal("FR-103: cancel fired against the registered verifier session but never reached the in-flight tool call")
	}

	cancelOuter()

	select {
	case result := <-resultCh:
		assert.True(t, result.Unavailable, "FR-103: a cancelled background adjudication must be Unavailable")
		assert.Nil(t, result.Verdict, "FR-103: no verdict may be produced")
	case <-time.After(10 * time.Second):
		t.Fatal("JudgeCriteria never returned after cancellation")
	}
}

// TestVerifierBudget_ResumeStartsAFreshBudget is FR-082's replacement for
// the deleted TestVerifierBudget_CountersSurviveResume (revision 7 deletes
// the "caps are honoured across a resume rather than reset by it" clause —
// see this wave's own Must-NOT-touch note and the judge spec's Test Matrix
// row: "~~TestVerifierBudget_CountersSurviveResume~~ DELETED (F10) …
// Replaced by TestVerifierBudget_ResumeStartsAFreshBudget"). Stated
// positively: a resumed adjudication is a NEW turn with a NEW turnID, and
// VerifierBudget's registry is keyed purely by turnID (verifier_budget.go's
// own doc comment) — so a fresh registration for the new turnID cannot
// inherit the exhausted counters of the turn it resumed from, because
// nothing links the two turnIDs at all.
func TestVerifierBudget_ResumeStartsAFreshBudget(t *testing.T) {
	const firstTurnID = "judge-turn-adr084-t4-original"
	const resumedTurnID = "judge-turn-adr084-t4-resumed"
	t.Cleanup(func() {
		UnregisterVerifierBudget(firstTurnID)
		UnregisterVerifierBudget(resumedTurnID)
	})

	// The FIRST adjudication's budget is exhausted (cap of 1, one call
	// already recorded).
	original := NewVerifierBudget(1, 0, 0, 0)
	RegisterVerifierBudget(firstTurnID, original)
	original.RecordToolCall(10)
	_, cappedOriginal := original.CheckCap()
	require.True(t, cappedOriginal, "setup: the original turn's budget must actually be exhausted before this test proves anything")
	UnregisterVerifierBudget(firstTurnID) // the original turn returned — this is what runVerifierAdjudication's own defer does

	// A "resumed" adjudication is dispatched as an ordinary NEW turn — same
	// caps, a BRAND NEW *VerifierBudget instance, registered under its own
	// (different) turnID, exactly as runVerifierAdjudication's per-attempt
	// loop would do for a later call into JudgeCriteria for the same unit.
	resumed := NewVerifierBudget(1, 0, 0, 0)
	RegisterVerifierBudget(resumedTurnID, resumed)
	defer UnregisterVerifierBudget(resumedTurnID)

	_, cappedResumed := verifierBudgetForTurn(resumedTurnID).CheckCap()
	assert.Falsef(t, cappedResumed,
		"FR-082: a resumed adjudication must start a FRESH budget — its own tool-call cap must not "+
			"already read as reached just because a DIFFERENT (unrelated) prior turn's budget was "+
			"exhausted. No surface carries VerifierBudget counters across turnIDs (FR-030's capture is "+
			"per-adjudication in-memory only; the Deployment section states no stateful change at all).")

	// Differentiation: the resumed budget's OWN counters do still work
	// normally — this is not "capping is broken", it is specifically "the
	// OLD turn's counters do not leak forward".
	verifierBudgetForTurn(resumedTurnID).RecordToolCall(10)
	_, cappedAfterOwnCall := verifierBudgetForTurn(resumedTurnID).CheckCap()
	assert.True(t, cappedAfterOwnCall, "the resumed budget's own cap must still enforce normally once ITS OWN call is recorded")

	// And the original turnID, now unregistered, must be gone entirely —
	// UnregisterVerifierBudget really removed it, not merely marked it.
	assert.Nilf(t, verifierBudgetForTurn(firstTurnID), "the original (returned) turn's budget must be unregistered, not merely exhausted")
}
