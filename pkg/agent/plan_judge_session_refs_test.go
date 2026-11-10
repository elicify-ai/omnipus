package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Regression for the founder's 2026-10-21 session-reference dispatch: the
// plan Judge must be able to use the reference it sees, without widening
// inspect_session's existing plan-member scope. Only the model is a double.
func TestPlanJudgeMemberSessionReferences(t *testing.T) {
	al, judge := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	members, evidence, foreign := seedPlanJudgeReferenceSessions(t, al)
	claim := buildPlanClaimText(members, nil)
	provider := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: `{"criteria":[{"id":"plan-criterion","met":false,"reason":"fixture verdict"}],"met":false}`}, nil
	}}
	judge.Provider = provider
	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope: task.VerdictScopePlan, PlanID: "reference-plan", AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{proseCriterion("plan-criterion", "member evidence is inspected")},
		Attempt:  1, ClaimText: claim,
	})
	require.False(t, result.Unavailable, "the real Judge pipeline must dispatch: %s", result.Reason)
	require.NotNil(t, result.Verdict)
	require.False(t, result.Verdict.Met, "adding references must not override the model's fixed unmet verdict")
	require.Equal(t, 1, provider.callCount(), "one adjudication is enough; this regression needs no retry")
	ctx := provider.capturedCtx()
	require.NotNil(t, ctx, "use the actual authorization carried into the Judge's provider call")
	provider.mu.Lock()
	messages := append([]providers.Message(nil), provider.lastMessages...)
	provider.mu.Unlock()
	var judgeInput string
	for _, message := range messages {
		if message.Role == "user" && strings.Contains(message.Content, "Member task outcomes:\n") {
			require.Empty(t, judgeInput, "the claim must be supplied once")
			judgeInput = message.Content
		}
	}
	require.NotEmpty(t, judgeInput, "the claim must reach the Judge, not only a renderer test")
	inspect := tools.NewInspectSessionTool(al.GetSessionStore())

	t.Run("each_rendered_reference_is_readable", func(t *testing.T) {
		references := planJudgeReferencesFromClaim(t, claim)
		require.Equal(t, []string{members[0].SessionID, members[1].SessionID}, references)
		for i, member := range members {
			block := fmt.Sprintf("- %s [%s] (%s): %s\n  session_id=%q\n",
				member.Title, member.ID, member.Status, member.Result, member.SessionID)
			require.Contains(t, judgeInput, block, "each reference must remain attached to the correct member")
			read := inspect.Execute(ctx, map[string]any{"session_id": references[i]})
			require.False(t, read.IsError, "a Judge-style inspection of the supplied reference must work: %s", read.ForLLM)
			assertPlanJudgeInspectionEvidence(t, read.ForLLM, references[i], evidence[references[i]])
		}
	})

	for _, denied := range []string{members[0].ID, members[1].ID, foreign} {
		t.Run("refuses_"+denied, func(t *testing.T) {
			read := inspect.Execute(ctx, map[string]any{"session_id": denied})
			require.True(t, read.IsError, "task IDs and foreign sessions must remain refused")
			require.Equal(t, fmt.Sprintf("inspect_session refused: session %q is outside the scope authorized for this verification", denied), read.ForLLM)
			require.NotContains(t, read.ForLLM, "FOREIGN-PRIVATE-EVIDENCE")
		})
	}
	t.Run("no_scope_still_refuses_a_real_member", func(t *testing.T) {
		read := inspect.Execute(context.Background(), map[string]any{"session_id": members[0].SessionID})
		require.True(t, read.IsError)
		require.Equal(t, fmt.Sprintf("inspect_session refused: session %q is outside the scope authorized for this verification", members[0].SessionID), read.ForLLM)
	})
	t.Run("superseded_result_stays_withheld_but_reference_is_available", func(t *testing.T) {
		text := buildPlanClaimText(members, map[string]bool{members[0].ID: true})
		require.Equal(t, []string{members[0].SessionID, members[1].SessionID}, planJudgeReferencesFromClaim(t, text))
		require.Contains(t, text, "outcome is WITHHELD and must not count as evidence for or against any criterion")
		require.NotContains(t, text, members[0].Result, "the superseded result must not re-enter the evidence")
	})
	t.Run("missing_session_is_not_fabricated", func(t *testing.T) {
		text := buildPlanClaimText([]task.Task{{ID: "task-without-session", Title: "not started"}}, nil)
		require.NotContains(t, text, "session_id=", "a task ID is never a substitute for a missing session")
	})
	t.Run("empty_plan_is_unchanged", func(t *testing.T) {
		require.Equal(t, "(this plan has no member tasks)", buildPlanClaimText(nil, nil))
	})
}

func seedPlanJudgeReferenceSessions(t *testing.T, al *AgentLoop) ([]task.Task, map[string]string, string) {
	t.Helper()
	store := al.GetSessionStore()
	require.NotNil(t, store)
	members := make([]task.Task, 0, 2)
	evidence := make(map[string]string, 2)
	var foreign string
	for i, marker := range []string{"member-alpha-evidence", "member-beta-evidence", "FOREIGN-PRIVATE-EVIDENCE"} {
		meta, err := store.NewSession(session.SessionTypeChat, "webchat", "native-agent")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
			ID: fmt.Sprintf("evidence-%d", i), Role: "assistant", Content: marker,
			Timestamp: time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC),
		}))
		planID := "reference-plan"
		if i == 2 {
			planID = "foreign-plan"
			foreign = meta.ID
		}
		member := task.Task{ID: fmt.Sprintf("reference-task-%d", i), Title: fmt.Sprintf("member-%d", i),
			AgentID: "native-agent", WorkspaceID: "test-ws", PlanID: planID, SessionID: meta.ID,
			Status: task.StatusDone, Result: marker}
		require.NoError(t, GetTaskStore(al).Create(&member))
		if i < 2 {
			members = append(members, member)
			evidence[meta.ID] = marker
		}
	}
	return members, evidence, foreign
}

func planJudgeReferencesFromClaim(t *testing.T, claim string) []string {
	t.Helper()
	matches := regexp.MustCompile(`(?m)^  session_id=("[^"]+")$`).FindAllStringSubmatch(claim, -1)
	references := make([]string, 0, len(matches))
	for _, match := range matches {
		reference, err := strconv.Unquote(match[1])
		require.NoError(t, err)
		references = append(references, reference)
	}
	return references
}

func assertPlanJudgeInspectionEvidence(t *testing.T, raw, sessionID, marker string) {
	t.Helper()
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	require.Equal(t, strconv.Quote(sessionID), string(result["session_id"]))
	var entries []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(result["entries"], &entries))
	require.Len(t, entries, 1, "inspection must return the real stored evidence")
	require.Equal(t, strconv.Quote(marker), string(entries[0]["content"]))
}
