// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Issue #890 — a chat whose own lifecycle record is terminal (failed,
// failed_reason interrupted) cannot delegate. The launch writes the child
// under the parent's record lock and the store refuses that write.
//
// These tests are option-independent: the ADR that chooses whether a
// terminal parent may still launch a child has not landed. Oracles are the
// issue text, not the current implementation.
//
//   - TestLaunch_TerminalParent_CurrentFailureIsImmutableRecord is a
//     characterization of the failure the founder hit. Its exact error
//     assertion flips when that ADR changes the launch outcome.
//   - TestDelegateRun_TerminalParentRefusal_HidesInvariantAndAsksForNewChat
//     is the desired contract for defect 2, and it fails on today's code.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// issue890WorkspaceID is an arbitrary workspace stamp so the parent record
// matches the chat session the launch reads. Issue #890 does not depend on
// a particular workspace id.
const issue890WorkspaceID = "ws-issue-890"

// issue890FailedReason is the parent's recorded reason in issue #890
// ("gen=1 state=failed failed_reason=interrupted").
const issue890FailedReason = "interrupted"

// issue890ForbiddenUserText is internal store wording issue #890 says must
// not reach the model or the user. The issue quotes each of these in the
// failure the founder was shown, and the RED brief lists them as the
// invariant text a refusal must not contain.
var issue890ForbiddenUserText = []string{
	"terminal record is immutable",
	"resumed_from",
	"follow_up/Play",
	"generation",
}

// issue890RequiredRefusalText is what a refusal for the parent's lifecycle
// must tell the caller. Issue #890 Expected: the user is told the
// conversation needs to be restarted. Defect 3: the only workaround is a
// new chat, and the product never says so. The RED brief states the
// combined requirement: the conversation itself can no longer start
// delegations, and a new chat is needed.
var issue890RequiredRefusalText = []string{
	"conversation",
	"can no longer start delegations",
	"new chat",
}

// issue890ObservedLaunchError is the launch failure issue #890 quotes,
// minus the "delegate: launch: " prefix the delegate tool adds when it
// forwards the store error. The issue wraps that one error across lines;
// the words themselves contain no line break.
func issue890ObservedLaunchError(parentSessionID string) string {
	return "session: lifecycle: terminal record is immutable: session " +
		strconv.Quote(parentSessionID) +
		" generation 1 is terminal (failed); a follow_up/Play must mint generation 2 via resumed_from"
}

// issue890TerminalParent is a real chat session whose lifecycle tail is
// already terminal, on the real stores newSteerAL builds under a temp dir.
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

func (h *issue890TerminalParent) launchRequest() steer.LaunchRequest {
	return steer.LaunchRequest{
		SteeringSessionID: h.parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "Prepare the spreadsheet",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-issue-890"},
	}
}

func (h *issue890TerminalParent) sessionIDs(t *testing.T) map[string]struct{} {
	t.Helper()
	listed, err := h.al.GetSessionStore().ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	ids := make(map[string]struct{}, len(listed))
	for _, meta := range listed {
		if meta == nil || meta.ID == "" {
			continue
		}
		ids[meta.ID] = struct{}{}
	}
	return ids
}

// TestLaunch_TerminalParent_CurrentFailureIsImmutableRecord drives the real
// steered launch against a parent whose lifecycle record is terminal
// (failed, interrupted) and pins the failure issue #890 records.
//
// characterization test — not a statement of the desired outcome. The exact
// error assertion below flips once the ADR decides whether a terminal parent
// may still launch a child. Do not edit it to follow an implementation
// change that has not landed that decision.
func TestLaunch_TerminalParent_CurrentFailureIsImmutableRecord(t *testing.T) {
	h := newIssue890TerminalParent(t)
	before := h.sessionIDs(t)

	result, err := NewSteerLauncher(h.al).Launch(context.Background(), h.launchRequest())

	// FINAL ASSERTION — flips when the ADR changes this launch outcome.
	// Until then it must match the failure issue #890 quotes, naming the
	// parent session (not the delegation target) at generation 1, state failed.
	want := issue890ObservedLaunchError(h.parentID)
	if err == nil {
		t.Fatalf("Launch succeeded with child %q; issue #890's current failure is %q", result.SessionID, want)
	}
	if !errors.Is(err, session.ErrLifecycleTerminalImmutable) {
		t.Fatalf("Launch error = %v, want it to wrap ErrLifecycleTerminalImmutable", err)
	}
	if err.Error() != want {
		t.Fatalf("Launch error =\n%s\nwant (issue #890):\n%s", err.Error(), want)
	}
	if result.SessionID != "" || result.Generation != 0 {
		t.Fatalf("Launch result = %+v, want a zero result when the launch is refused", result)
	}

	parent, loadErr := h.al.GetSessionLifecycleStore().Load(h.parentID)
	if loadErr != nil {
		t.Fatalf("Load(parent) after refused launch: %v", loadErr)
	}
	if parent.Generation != 1 || parent.State != session.LifecycleFailed || parent.FailedReason != issue890FailedReason {
		t.Fatalf("parent after refused launch = gen %d state %q reason %q, want gen 1 failed/%s (the write is refused)",
			parent.Generation, parent.State, parent.FailedReason, issue890FailedReason)
	}
	records, listErr := h.al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if listErr != nil {
		t.Fatalf("List lifecycle: %v", listErr)
	}
	if len(records) != 1 || records[0].SessionID != h.parentID {
		ids := make([]string, 0, len(records))
		for _, rec := range records {
			ids = append(ids, rec.SessionID)
		}
		t.Fatalf("lifecycle records = %v, want only the parent %s (issue #890: the target is never reached)", ids, h.parentID)
	}
	after := h.sessionIDs(t)
	if len(after) != len(before) {
		t.Fatalf("session count %d before launch, %d after; issue #890: the refused launch leaves no child session", len(before), len(after))
	}
	for id := range after {
		if _, ok := before[id]; !ok {
			t.Fatalf("launch left session %s, which did not exist before the refused launch", id)
		}
	}
}

// TestDelegateRun_TerminalParentRefusal_HidesInvariantAndAsksForNewChat is
// issue #890 defect 2. When delegate(action=run) is refused because the
// calling conversation's own lifecycle record is terminal, the text handed
// back to the model and the user must not repeat the store invariant, and
// must say this conversation can no longer start delegations and a new chat
// is needed.
//
// Issue #890 Expected also allows the delegation to succeed. That branch is
// not a refusal, so the wording contract does not apply to it. A success
// must still be a real launched session, not an empty non-error.
func TestDelegateRun_TerminalParentRefusal_HidesInvariantAndAsksForNewChat(t *testing.T) {
	h := newIssue890TerminalParent(t)
	tool := tools.NewDelegateTool("test-model", 0, 0)
	tool.SetSessionLauncher(NewSteerLauncher(h.al))
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *tools.DelegationDenial { return nil })

	ctx := tools.WithToolCallID(
		tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), h.parentID), testDefaultAgentID),
		"call-issue-890",
	)
	result := tool.Execute(ctx, map[string]any{
		"action":   "run",
		"agent_id": testDefaultAgentID,
		"task":     "Prepare the spreadsheet",
	})
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}

	if !result.IsError {
		assertIssue890SuccessIsARealLaunch(t, result.ForLLM, h.parentID)
		return
	}

	// The model reads ForLLM. ForUser is the user channel when set.
	// Err is internal (not serialized) and is not the contract surface.
	surfaces := []struct {
		name string
		text string
	}{
		{name: "ForLLM", text: result.ForLLM},
	}
	if result.ForUser != "" {
		surfaces = append(surfaces, struct {
			name string
			text string
		}{name: "ForUser", text: result.ForUser})
	}
	for _, surface := range surfaces {
		assertIssue890RefusalText(t, surface.name, surface.text, h.parentID)
	}
}

// assertIssue890SuccessIsARealLaunch is the other half of issue #890
// Expected: the delegation succeeds. A non-error still has to be a launched
// child session, and it must not carry the store-invariant wording.
func assertIssue890SuccessIsARealLaunch(t *testing.T, forLLM, parentSessionID string) {
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
	for _, forbidden := range []string{"terminal record is immutable", "resumed_from", "follow_up/Play"} {
		if strings.Contains(forLLM, forbidden) {
			t.Fatalf("success payload still contains internal text %q\n%s", forbidden, forLLM)
		}
	}
}

// assertIssue890RefusalText checks one returned string against issue #890
// defect 2. A harness that never reached the parent's lifecycle refusal
// fails here too, so a setup error cannot pass by accident.
func assertIssue890RefusalText(t *testing.T, surface, text, parentSessionID string) {
	t.Helper()
	problems := issue890RefusalProblems(text, parentSessionID)
	if len(problems) == 0 {
		return
	}
	const reachedBug = "session: lifecycle: terminal record is immutable"
	if !strings.Contains(text, reachedBug) {
		t.Fatalf("%s did not reach the parent-lifecycle refusal and does not meet the user-facing contract.\ntext: %s\nproblems:\n- %s",
			surface, text, strings.Join(problems, "\n- "))
	}
	t.Fatalf("%s still exposes the internal lifecycle refusal (issue #890 defect 2).\ntext: %s\nproblems:\n- %s",
		surface, text, strings.Join(problems, "\n- "))
}

func issue890RefusalProblems(text, parentSessionID string) []string {
	var problems []string
	for _, forbidden := range issue890ForbiddenUserText {
		if strings.Contains(text, forbidden) {
			problems = append(problems, "contains internal text "+strconv.Quote(forbidden))
		}
	}
	if parentSessionID != "" && strings.Contains(text, parentSessionID) {
		problems = append(problems, "names the parent session "+strconv.Quote(parentSessionID)+" (issue #890: the error names a session the user did not reference)")
	}
	for _, required := range issue890RequiredRefusalText {
		if !strings.Contains(text, required) {
			problems = append(problems, "missing "+strconv.Quote(required))
		}
	}
	return problems
}
