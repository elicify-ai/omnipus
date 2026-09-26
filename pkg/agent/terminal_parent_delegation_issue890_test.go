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
// explicitly rejected. Both are replaced below, per the ADR's test plan.
// Oracles are the ADR and the founder text, not the implementation.

package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

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

// issue890ForbiddenUserText is internal machinery wording that must never
// reach the model or the user, per issue #890 and ADR-093 D5 / the ADR test
// plan's re-pin item 2: no store invariant, no revival recipe (follow_up /
// Play / resumed_from), no generation arithmetic, no session id, and —
// F890-1 — never the "new chat" workaround. "generation" is named in that
// re-pin item and must be on this list.
var issue890ForbiddenUserText = []string{
	"terminal record is immutable",
	"resumed_from",
	"follow_up",
	"play",
	"generation",
	"new chat",
}

// issue890RequiredRefusalText is the D5 contract's required phrases
// (ADR-093 D5): the result is an instruction to the agent that names the
// conversation and says a new message resumes it. The ADR's literal sentence
// is: "Delegation is unavailable because this conversation is not active
// right now. Tell the user that sending a new message in this conversation
// resumes it, and that their request has not been started."
var issue890RequiredRefusalText = []string{
	"conversation",
	"resumes",
}

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

// stampInterruptedUnifiedStatus mirrors what the boot sweep's reconciliation
// leaves behind (pkg/agent/boot_sweep.go::reconcileUnifiedMetaStatus): the
// UnifiedMeta.Status of a swept chat reads "interrupted" while its lifecycle
// record is failed. MIN-002 needs that state so the revival's reset to active
// is observable.
func (h *issue890TerminalParent) stampInterruptedUnifiedStatus(t *testing.T) {
	t.Helper()
	interrupted := session.StatusInterrupted
	if err := h.al.GetSessionStore().SetMeta(h.parentID, session.MetaPatch{Status: &interrupted}); err != nil {
		t.Fatalf("SetMeta(parent).Status=interrupted: %v", err)
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
	h.stampInterruptedUnifiedStatus(t)

	// The ordinary inbound-turn admission path — the path a human message
	// into the chat takes (ADR-093 D4: "the turn revives the record before
	// any tool runs"). The mock provider answers immediately; the turn runs
	// to completion inside the call.
	msg := bus.InboundMessage{
		Channel:    "webchat",
		ChatID:     "adr093-revival-chat",
		Content:    "Now delegate the spreadsheet work for me.",
		SessionID:  h.parentID,
		SessionKey: "agent:" + testDefaultAgentID + ":session:" + h.parentID,
	}
	if _, _, perr := h.al.processMessage(context.Background(), msg); perr != nil {
		t.Fatalf("processMessage (the human message that must revive the terminal chat): %v", perr)
	}

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
	if _, err := h.al.GetSessionLifecycleStore().Load(childID); err != nil {
		t.Fatalf("child lifecycle record for %q missing after the successful delegation (ADR-093 re-pin item 1): %v", childID, err)
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
}

// TestDelegateRun_TerminalParentBackstopRefusal_SaysHowToResume is the
// re-pinned defect-2 contract (ADR-093 D5): when delegate(action=run) cannot
// launch because the calling conversation's own lifecycle record is terminal
// and no revival is in the call chain (D2's backstop), the text handed back
// to the model must be the D5 sentence — an instruction to the agent that
// names the conversation and says a new message resumes it — never the
// lifecycle store's own refusal, and never the "new chat" workaround
// (F890-1 retired it).
func TestDelegateRun_TerminalParentBackstopRefusal_SaysHowToResume(t *testing.T) {
	h := newIssue890TerminalParent(t)

	tool := h.newDelegateTool(t)
	result := tool.Execute(h.delegateCallContext("call-adr093-backstop"), h.delegateRunArgs())
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}
	if !result.IsError {
		t.Fatalf("delegate from a terminal chat with no revival in the call chain must be refused (ADR-093 D2), got success:\n%s", result.ForLLM)
	}

	// The model reads ForLLM. ForUser is the user channel when set.
	surfaces := []struct{ name, text string }{{name: "ForLLM", text: result.ForLLM}}
	if result.ForUser != "" {
		surfaces = append(surfaces, struct{ name, text string }{name: "ForUser", text: result.ForUser})
	}
	for _, surface := range surfaces {
		assertIssue890RefusalText(t, surface.name, surface.text, h.parentID)
	}

	// D2's backstop leaves nothing behind: no child lifecycle record, no new
	// unified session, and the parent's record is unchanged (a refused launch
	// is not a revival).
	records, err := h.al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List lifecycle after refused delegation: %v", err)
	}
	if len(records) != 1 || records[0].SessionID != h.parentID {
		ids := make([]string, 0, len(records))
		for _, rec := range records {
			ids = append(ids, rec.SessionID)
		}
		t.Fatalf("lifecycle records after the refused delegation = %v, want only the parent %s (ADR-093 D2: the refusal produces no child lifecycle file)", ids, h.parentID)
	}
	parent, err := h.al.GetSessionLifecycleStore().Load(h.parentID)
	if err != nil {
		t.Fatalf("Load(parent) after refused delegation: %v", err)
	}
	if parent.Generation != 1 || parent.State != session.LifecycleFailed || parent.FailedReason != issue890FailedReason {
		t.Fatalf("parent after refused delegation = gen %d state %q reason %q, want gen 1 failed/%s unchanged (ADR-093: a refused launch is not a revival)",
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

// assertIssue890RefusalText checks one returned string against ADR-093 D5.
// A harness that never reached the launch refusal fails here too, so a
// setup error cannot pass by accident.
func assertIssue890RefusalText(t *testing.T, surface, text, parentSessionID string) {
	t.Helper()
	problems := issue890RefusalProblems(text, parentSessionID)
	if len(problems) == 0 {
		return
	}
	t.Fatalf("%s does not meet ADR-093 D5's refusal contract.\ntext: %s\nproblems:\n- %s",
		surface, text, strings.Join(problems, "\n- "))
}

func issue890RefusalProblems(text, parentSessionID string) []string {
	var problems []string
	lower := strings.ToLower(text)
	for _, forbidden := range issue890ForbiddenUserText {
		if strings.Contains(lower, forbidden) {
			problems = append(problems, "contains forbidden text "+strconv.Quote(forbidden)+" (ADR-093 D5 / F890-1)")
		}
	}
	if parentSessionID != "" && strings.Contains(text, parentSessionID) {
		problems = append(problems, "names the parent session "+strconv.Quote(parentSessionID)+" (ADR-093 D5: the result must not contain a session id)")
	}
	for _, required := range issue890RequiredRefusalText {
		if !strings.Contains(lower, required) {
			problems = append(problems, "missing "+strconv.Quote(required)+" (ADR-093 D5's required phrases)")
		}
	}
	return problems
}
