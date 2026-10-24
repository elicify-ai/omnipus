// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Issue #890, re-pinned to ADR-093 (docs/internal/architecture/ADR-093-open-
// conversation-must-keep-delegation.md, corrected 2026-09-26, commit
// 42b878f04) and the founder's decisions F890-1..F890-4.
//
// The ADR decided: a chat whose own lifecycle record is terminal
// (failed/interrupted) or "running but stopped" is still usable — a human
// message into it revives the record (new generation via resumed_from,
// terminal history immutable) BEFORE any tool runs, and the delegation in
// that turn succeeds. When no revival is in the call chain, the delegate tool
// refuses with the D5 sentence, never with the lifecycle store's own text,
// and the refusal leaves nothing behind.
//
// Re-pin history: the previous pin on this file was option-independent —
// TestLaunch_TerminalParent_CurrentFailureIsImmutableRecord characterised
// the failure (its own header said the error assertion "flips once the ADR
// decides") and TestDelegateRun_TerminalParentRefusal_HidesInvariantAndAsks
// ForNewChat asserted the "a new chat is needed" workaround that F890-1
// explicitly rejected. Both were replaced per the ADR's test plan.
//
// SPEC CHANGE — re-pinned again to the ADR-093 amendment of 2026-10-23
// (founder decision): D2's launch-time backstop and D5's refusal sentences
// are removed, so the 2026-09-26 re-pin's backstop-refusal test
// (TestDelegateRun_TerminalParentBackstopRefusal_SaysHowToResume) and its
// D5 text contract are retired; the file now pins the incident's regression
// instead — delegation from a terminal chat SUCCEEDS (see the replacing
// test below). TestAdr093RootRevival_… is unchanged and stays the main
// oracle. Oracles are the ADR and the founder text, not the implementation.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// issue890WorkspaceID is an arbitrary workspace stamp so the parent record
// matches the chat session the launch reads. Issue #890 does not depend on
// a particular workspace id.
const issue890WorkspaceID = "ws-issue-890"

// issue890FailedReason is the parent's recorded reason in issue #890
// ("gen=1 state=failed failed_reason=interrupted").
const issue890FailedReason = "interrupted"

// The D5 refusal-text contract (issue890ForbiddenUserText,
// issue890RequiredRefusalText, assertIssue890RefusalText,
// issue890RefusalProblems) is RETIRED.
// SPEC CHANGE — ADR-093 amendment 2026-10-23, Decision 2: D5's refusal
// sentences and their mapping machinery are deleted, and the amendment
// rules that the required-phrases list "loses its subject". The leak the
// contract guarded against — the lifecycle store's immutable-record
// sentence reaching the model — was a property of the deleted mapping
// paths; per Decision 2 that text does not return. The remaining refusal
// surface (the dispatch sentinels) is pinned short by
// TestDelegateRun_DispatchRefusal_SurfacesRawSentinelText.

// issue890TerminalParent is a real chat session whose lifecycle tail is
// already terminal (failed, interrupted — the boot sweep's shape, ADR-093
// Context answer 1), on the real stores newSteerAL builds under a temp dir.
type issue890TerminalParent struct {
	al       *AgentLoop
	parentID string
}

func newIssue890TerminalParent(t *testing.T) *issue890TerminalParent {
	t.Helper()
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, issue890WorkspaceID)
	rec := &session.LifecycleRecord{
		SessionID:      parentID,
		Generation:     1,
		State:          session.LifecycleFailed,
		FailedReason:   issue890FailedReason,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    issue890WorkspaceID,
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}
	if err := al.GetSessionLifecycleStore().Persist(rec); err != nil {
		t.Fatalf("persist terminal parent lifecycle: %v", err)
	}
	return &issue890TerminalParent{al: al, parentID: parentID}
}

// stampFailedUnifiedStatus mirrors what the boot sweep's reconciliation
// leaves behind (pkg/agent/boot_sweep.go::reconcileUnifiedMetaStatus): the
// UnifiedMeta.Status of a swept chat reads "failed" while its lifecycle
// record is failed (sub-agent control plane ADR D4/MAJ-009 retires the
// former "interrupted" wire value in favor of this one). MIN-002 needs that
// state so the revival's reset to active is observable.
func (h *issue890TerminalParent) stampFailedUnifiedStatus(t *testing.T) {
	t.Helper()
	failed := session.StatusFailed
	if err := h.al.GetSessionStore().SetMeta(h.parentID, session.MetaPatch{Status: &failed}); err != nil {
		t.Fatalf("SetMeta(parent).Status=failed: %v", err)
	}
}

func (h *issue890TerminalParent) newDelegateTool(t *testing.T) *tools.DelegateTool {
	t.Helper()
	tool := tools.NewDelegateTool("test-model", 0, 0)
	tool.SetSessionLauncher(NewSteerLauncher(h.al))
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *tools.DelegationDenial { return nil })
	return tool
}

func (h *issue890TerminalParent) delegateCallContext(callID string) context.Context {
	return tools.WithToolCallID(
		tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), h.parentID), testDefaultAgentID),
		callID,
	)
}

func (h *issue890TerminalParent) delegateRunArgs() map[string]any {
	return map[string]any{
		"action":   "run",
		"agent_id": testDefaultAgentID,
		"task":     "Prepare the spreadsheet",
	}
}

// TestAdr093RootRevival_HumanMessageRevivesTerminalChatAndDelegationSucceeds
// is ADR-093's re-pinned happy path — "The happy path is the contract now":
// a human message into a failed(interrupted) chat root starts a turn, the
// turn revives the record (generation 2, resumed_from = same id, state
// running), and a delegate call in that turn succeeds — the child lifecycle
// file exists (F890-1: delegation from a previously-interrupted chat must
// succeed; the "new chat" workaround is retired).
func TestAdr093RootRevival_HumanMessageRevivesTerminalChatAndDelegationSucceeds(t *testing.T) {
	h := newIssue890TerminalParent(t)
	h.stampFailedUnifiedStatus(t)
	wireSteerCompletionDeps(t, h.al)

	// Hold only the external provider so the D4 running/delegation assertions
	// describe the active turn, not a turn that already answered. Amended
	// ADR-093 D3 (2026-10-06): "a root chat's final answer settles its producing
	// execution as lifecycle `completed` (done)." Assert that second boundary
	// separately after releasing the root's answer.
	held := &r1CompletionProvider{
		answers: []string{"The delegation is accepted.", "The spreadsheet is prepared."},
		entered: make(chan int, 2), release: []chan struct{}{make(chan struct{}), make(chan struct{})},
	}
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered root agent missing")
	}
	inst.Provider = held
	messageDone, messageErr := make(chan struct{}), make(chan error, 1)
	t.Cleanup(func() {
		held.openAll()
		select {
		case <-messageDone:
		case <-time.After(10 * time.Second):
			t.Error("human message did not finish after provider release")
		}
	})
	msg := bus.InboundMessage{
		Channel:    "webchat",
		ChatID:     "adr093-revival-chat",
		Content:    "Now delegate the spreadsheet work for me.",
		SessionID:  h.parentID,
		SessionKey: "agent:" + testDefaultAgentID + ":session:" + h.parentID,
	}
	go func() {
		_, _, perr := h.al.processMessageAsPrimaryForTest(context.Background(), msg)
		messageErr <- perr
		close(messageDone)
	}()
	r1AwaitProvider(t, held, 0)

	// ADR-093 D4: the record is revived before any tool runs — generation 2,
	// resumed_from = this session id, state running (terminal history stays
	// immutable; the revival mints the NEXT generation, it never rewrites).
	rec, err := h.al.GetSessionLifecycleStore().Load(h.parentID)
	if err != nil {
		t.Fatalf("Load(parent) after the human message: %v", err)
	}
	if rec.Generation != 2 {
		t.Fatalf("record generation after the human message = %d, want 2 (ADR-093 D4: the human message revives the terminal record via resumed_from)", rec.Generation)
	}
	if rec.ResumedFrom != h.parentID {
		t.Fatalf("record resumed_from after the human message = %q, want %q (ADR-093 D4)", rec.ResumedFrom, h.parentID)
	}
	if rec.State != session.LifecycleRunning || rec.Terminal() {
		t.Fatalf("record state after the human message = %q, want running (ADR-093 D4: state running, failed reason cleared)", rec.State)
	}
	if rec.FailedReason != "" {
		t.Fatalf("record failed_reason after the human message = %q, want cleared (ADR-093 D4)", rec.FailedReason)
	}
	// ADR-093 D4 / MIN-002: the revival resets UnifiedMeta.Status to active
	// in the same revival, so the session list agrees with the record.
	meta, err := h.al.GetSessionStore().GetMeta(h.parentID)
	if err != nil {
		t.Fatalf("GetMeta(parent) after revival: %v", err)
	}
	if meta.Status != session.StatusActive {
		t.Fatalf("UnifiedMeta.Status after revival = %q, want active (ADR-093 D4 / MIN-002: the record must agree with the screen)", meta.Status)
	}
	// The revival must be Revive-only on the ordinary path — the steered
	// machinery's instruction write is the forbidden fingerprint (MAJ-003).
	adr093AssertNoSteeredInstructionEntry(t, h.al, h.parentID)

	// Then the delegate call in that turn succeeds and the child lifecycle
	// file exists (ADR-093 test plan, re-pin item 1).
	tool := h.newDelegateTool(t)
	result := tool.Execute(h.delegateCallContext("call-adr093-revival"), h.delegateRunArgs())
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}
	if result.IsError {
		t.Fatalf("delegate from the revived chat failed (ADR-093 re-pin item 1: it must succeed):\n%s", result.ForLLM)
	}
	childID := adr093ChildSessionIDFromResult(t, result.ForLLM, h.parentID)
	if _, loadErr := h.al.GetSessionLifecycleStore().Load(childID); loadErr != nil {
		t.Fatalf("child lifecycle record for %q missing after the successful delegation (ADR-093 re-pin item 1): %v", childID, loadErr)
	}

	// The delegate call is not the human's newer instruction (ADR-093 D4
	// "What never revives"): the parent stays on the revival's generation.
	parent, err := h.al.GetSessionLifecycleStore().Load(h.parentID)
	if err != nil {
		t.Fatalf("Load(parent) after delegation: %v", err)
	}
	if parent.Generation != 2 {
		t.Fatalf("parent generation after delegation = %d, want 2 (ADR-093 D4: a tool call never mints a generation)", parent.Generation)
	}

	held.open(0)
	select {
	case <-messageDone:
	case <-time.After(10 * time.Second):
		t.Fatal("human turn did not settle after its provider returned")
	}
	if perr := <-messageErr; perr != nil {
		t.Fatalf("processMessage (the human message that revives the terminal chat): %v", perr)
	}
	settled := rootReopenedRecord(t, h.al, h.parentID)
	if settled.State != session.LifecycleCompleted || !settled.Terminal() || settled.Generation != 2 || settled.ResumedFrom != h.parentID || settled.FailedReason != "" {
		t.Fatalf("finished root = %+v, want completed/done on revival generation 2 (amended ADR-093 D3, 2026-10-06)", settled)
	}
	visible, err := h.al.GetSessionStore().GetMeta(h.parentID)
	if err != nil || visible.Status != session.StatusActive {
		t.Fatalf("finished conversation visibility = %+v/%v, want active, not archived (amended ADR-093 D3)", visible, err)
	}
}

// TestDelegateRun_TerminalParent_DelegatePublishesChildWithoutRevivingParent
// replaces TestDelegateRun_TerminalParentBackstopRefusal_SaysHowToResume.
//
// SPEC CHANGE — ADR-093 amendment 2026-10-23 (the founder's Jarvis incident
// ruling): D2's launch-time backstop and D5's refusal sentences are REMOVED
// ("this check must be removed, a session that ended can not fire
// anything"). OLD (D2/D5): delegate(run) from a terminal chat with no
// revival in the call chain was refused with the D5 sentence, leaving no
// child behind. NEW (amendment Decision 1): the delegate SUCCEEDS — the
// child lifecycle file exists; the launch is not a revival, so the parent's
// stale terminal record is unchanged (D1 stands; only a human message
// revives — D4). This is the incident's regression test: the triage found
// exactly this call refused with "Delegation is unavailable…" while the
// conversation was visibly running.
func TestDelegateRun_TerminalParent_DelegatePublishesChildWithoutRevivingParent(t *testing.T) {
	h := newIssue890TerminalParent(t)

	tool := h.newDelegateTool(t)
	result := tool.Execute(h.delegateCallContext("call-adr093-backstop"), h.delegateRunArgs())
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}
	if result.IsError {
		t.Fatalf("delegate from a terminal chat = refusal:\n%s\n— ADR-093 amendment 2026-10-23 Decision 1: D2's launch-time backstop is removed; the delegation must succeed (old spec: D5 refusal \"Delegation is unavailable…\")", result.ForLLM)
	}
	childID := adr093ChildSessionIDFromResult(t, result.ForLLM, h.parentID)
	if _, err := h.al.GetSessionLifecycleStore().Load(childID); err != nil {
		t.Fatalf("child lifecycle record for %s missing after the delegated launch (amendment Decision 1: the child is published): %v", childID, err)
	}

	// The launch is not a revival: the parent's stale terminal record stays
	// gen 1 failed/interrupted (D1: the launch does not write the parent;
	// D4: only a human message revives).
	parent, err := h.al.GetSessionLifecycleStore().Load(h.parentID)
	if err != nil {
		t.Fatalf("Load(parent) after delegation: %v", err)
	}
	if parent.Generation != 1 || parent.State != session.LifecycleFailed || parent.FailedReason != issue890FailedReason {
		t.Fatalf("parent after delegation = gen %d state %q reason %q, want gen 1 failed/%s unchanged (D1: the launch is not a revival)",
			parent.Generation, parent.State, parent.FailedReason, issue890FailedReason)
	}
}

// adr093ChildSessionIDFromResult parses delegate(run)'s success payload —
// the first line is the DelegateSessionResponse JSON — and returns the
// launched child's session id.
func adr093ChildSessionIDFromResult(t *testing.T, forLLM, parentSessionID string) string {
	t.Helper()
	payload := forLLM
	if i := strings.IndexByte(forLLM, '\n'); i >= 0 {
		payload = forLLM[:i]
	}
	var response generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatalf("success payload is not a delegate launch response: %v\n%s", err, forLLM)
	}
	if response.SessionId == "" || response.SessionId == parentSessionID {
		t.Fatalf("success did not launch a child distinct from the parent %s: %+v", parentSessionID, response)
	}
	return response.SessionId
}
