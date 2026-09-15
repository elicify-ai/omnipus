// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_outcome_test.go pins the goal outcome line (founder decision
// 2026-09-14: "a finished goal leaves a lasting chat line"), against the
// contract in contracts/components/schemas/GoalOutcome.yaml:
//
//   - every ending writes EXACTLY ONE `type: system, system_subtype:
//     goal_outcome` transcript entry, with the ending, tries, round limit,
//     judge reason and criteria count the contract assigns to that ending;
//   - the live goal_outcome event carries that entry's own id and the same
//     outcome;
//   - the free-text handover each ending used to write separately is gone —
//     the same text now lives only in the outcome entry;
//   - a placeholder judge reason is never sent.
//
// Oracles are the contract's field meanings and independent carriers of the
// same fact (the goal record, the judge_verdict transcript entry), never the
// outcome builder's own output.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// systemEntriesContaining returns sid's system entries — outcome entries
// included — whose content contains substr.
func systemEntriesContaining(t *testing.T, store *session.UnifiedStore, sid, substr string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("read transcript %q: %v", sid, err)
	}
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeSystem && strings.Contains(e.Content, substr) {
			out = append(out, e)
		}
	}
	return out
}

// requireOneGoalOutcome asserts sid holds exactly one goal outcome entry for
// goalID and returns it. The id shape and the entry's own fields are checked
// here because they are the same for every ending.
func requireOneGoalOutcome(t *testing.T, store *session.UnifiedStore, sid, goalID string) session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("read transcript %q: %v", sid, err)
	}
	var outcomes []session.TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype == session.SystemSubtypeGoalOutcome {
			outcomes = append(outcomes, e)
		}
	}
	if len(outcomes) != 1 {
		t.Fatalf("session %q holds %d goal outcome entries; want exactly 1 per ending", sid, len(outcomes))
	}
	e := outcomes[0]
	if e.Type != session.EntryTypeSystem || e.Role != "system" {
		t.Errorf("outcome entry type/role = %q/%q; want system/system", e.Type, e.Role)
	}
	if e.GoalOutcome == nil {
		t.Fatalf("outcome entry %q carries no structured outcome", e.ID)
	}
	if e.GoalOutcome.GoalId != goalID {
		t.Errorf("outcome goal_id = %q; want %q", e.GoalOutcome.GoalId, goalID)
	}
	wantID := fmt.Sprintf("goal-outcome-%s-%d", goalID, e.GoalOutcome.EndedAt.UnixNano())
	if e.ID != wantID {
		t.Errorf("outcome entry id = %q; want %q (goal id + terminal transition nanos, GoalOutcomeFrame.message_id)", e.ID, wantID)
	}
	if strings.TrimSpace(e.Content) == "" {
		t.Errorf("outcome entry has no plain content")
	}
	return e
}

// goalOutcomePayloadsFor returns the live goal outcome events for sid.
func goalOutcomePayloadsFor(c *eventCollector, sid string) []GoalOutcomePayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []GoalOutcomePayload
	for _, e := range c.events {
		if e.Kind != EventKindGoalOutcome {
			continue
		}
		if p, ok := e.Payload.(GoalOutcomePayload); ok && p.SessionID == sid {
			out = append(out, p)
		}
	}
	return out
}

// assertLiveOutcomeMatchesEntry checks the live event carries the persisted
// entry's id and exactly its outcome. Call only after events stopped.
func assertLiveOutcomeMatchesEntry(t *testing.T, events *eventCollector, sid string, entry session.TranscriptEntry) {
	t.Helper()
	live := goalOutcomePayloadsFor(events, sid)
	if len(live) != 1 {
		t.Fatalf("%d live goal outcome events for %q; want exactly 1", len(live), sid)
	}
	if live[0].MessageID != entry.ID {
		t.Errorf("live outcome message id = %q; persisted entry id = %q — they must be the same", live[0].MessageID, entry.ID)
	}
	gotJSON, _ := json.Marshal(live[0].Outcome)
	wantJSON, _ := json.Marshal(entry.GoalOutcome)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("live outcome %s differs from the persisted one %s", gotJSON, wantJSON)
	}
}

func setGoalLatestReason(t *testing.T, goalID, reason string) {
	t.Helper()
	if _, err := resolveGoalRecordStore().Update(goalID, func(cur *goal.Goal) error {
		cur.LatestReason = reason
		return nil
	}); err != nil {
		t.Fatalf("set latest reason: %v", err)
	}
}

func TestGoalOutcome_Met_OneEntryAndTheLiveEventCarriesItsID(t *testing.T) {
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "tests pass")}, nil
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)

	g.al.dispatchDeferredGoalAdjudication(g.work)
	g.stopEvents()

	rec := mustGoalRecord(t, g.goalID)
	if rec.State != generated.GoalStateMet {
		t.Fatalf("setup: goal state = %q; want met", rec.State)
	}
	e := requireOneGoalOutcome(t, g.store, g.sid, g.goalID)
	o := e.GoalOutcome
	if o.Ending != generated.GoalOutcomeEndingMet {
		t.Errorf("ending = %q; want met", o.Ending)
	}
	if o.RoundsUsed != 1 {
		t.Errorf("rounds_used = %d; want 1 (the first adjudication met the goal)", o.RoundsUsed)
	}
	if o.MaxRounds != rec.MaxRounds {
		t.Errorf("max_rounds = %d; want the goal's own limit %d", o.MaxRounds, rec.MaxRounds)
	}
	if o.GoalText != "make the tests pass" {
		t.Errorf("goal_text = %q; want the goal's own text", o.GoalText)
	}
	if o.JudgeReason != nil {
		t.Errorf("judge_reason = %q; a met ending omits it", *o.JudgeReason)
	}
	if rec.LatestVerdict == nil || o.CriteriaTotal == nil || *o.CriteriaTotal != len(rec.LatestVerdict.PerCriterion) {
		t.Errorf("criteria_total = %v; want the deciding verdict's per-criterion count", o.CriteriaTotal)
	}
	if !o.EndedAt.Equal(rec.LastActivityAt) {
		t.Errorf("ended_at = %v; the terminal transition stamped %v", o.EndedAt, rec.LastActivityAt)
	}
	assertLiveOutcomeMatchesEntry(t, g.events, g.sid, e)
}

func TestGoalOutcome_RoundBound_CarriesJudgeReasonAndReplacesTheHandover(t *testing.T) {
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(false, "the suite still fails")}, nil
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)
	maxRounds := goalRecordForSession(t, g.sid).MaxRounds
	setGoalRecordRound(t, g.sid, maxRounds-1)

	g.al.dispatchDeferredGoalAdjudication(g.work)
	g.stopEvents()

	if st := mustGoalRecord(t, g.goalID).State; st != generated.GoalStateExhausted {
		t.Fatalf("setup: goal state = %q; want exhausted", st)
	}
	e := requireOneGoalOutcome(t, g.store, g.sid, g.goalID)
	o := e.GoalOutcome
	if o.Ending != generated.GoalOutcomeEndingRoundsExhausted {
		t.Errorf("ending = %q; want rounds_exhausted", o.Ending)
	}
	if o.RoundsUsed != maxRounds || o.MaxRounds != maxRounds {
		t.Errorf("rounds_used/max_rounds = %d/%d; want %d/%d (the round that hit the limit)", o.RoundsUsed, o.MaxRounds, maxRounds, maxRounds)
	}
	if o.JudgeReason == nil || !strings.Contains(*o.JudgeReason, "the suite still fails") {
		t.Errorf("judge_reason = %v; want the Judge's last unmet reason", o.JudgeReason)
	}

	// criteria_total is the deciding verdict's count, read from the verdict's
	// own transcript entry.
	all, err := g.store.ReadTranscript(g.sid)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	var judged int
	for _, entry := range all {
		if entry.Type == session.EntryTypeJudgeVerdict {
			var v task.JudgeVerdict
			if uerr := json.Unmarshal([]byte(entry.Content), &v); uerr != nil {
				t.Fatalf("parse judge_verdict entry: %v", uerr)
			}
			judged = len(v.PerCriterion)
		}
	}
	if o.CriteriaTotal == nil || *o.CriteriaTotal != judged || judged == 0 {
		t.Errorf("criteria_total = %v; the deciding verdict judged %d criteria", o.CriteriaTotal, judged)
	}

	if lines := systemEntriesContaining(t, g.store, g.sid, "did not reach a MET verdict"); len(lines) != 1 || lines[0].ID != e.ID {
		t.Errorf("%d system lines tell the user the goal was not met; want exactly the outcome entry (no separate free-text handover)", len(lines))
	}
	assertLiveOutcomeMatchesEntry(t, g.events, g.sid, e)
}

func TestGoalOutcome_BareClaimRoundBound_NoJudgeReasonNoVerdict(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), armedGoalMaxRounds-1, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
	events, stop := recordAgentEvents(t, al)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	for i := 0; i < goalBareClaimCostThreshold; i++ {
		al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "done\nGOAL_STATUS: met"})
	}
	stop()

	if st := mustGoalRecord(t, gid).State; st != generated.GoalStateExhausted {
		t.Fatalf("setup: goal state = %q; want exhausted after a costly bare claim at the last round", st)
	}
	e := requireOneGoalOutcome(t, store, sid, gid)
	o := e.GoalOutcome
	if o.Ending != generated.GoalOutcomeEndingRoundsExhausted {
		t.Errorf("ending = %q; want rounds_exhausted", o.Ending)
	}
	if o.RoundsUsed != armedGoalMaxRounds || o.MaxRounds != armedGoalMaxRounds {
		t.Errorf("rounds_used/max_rounds = %d/%d; want %d/%d", o.RoundsUsed, o.MaxRounds, armedGoalMaxRounds, armedGoalMaxRounds)
	}
	if o.JudgeReason != nil || o.CriteriaTotal != nil {
		t.Errorf("a bare claim never reached the Judge; judge_reason=%v criteria_total=%v must both be absent", o.JudgeReason, o.CriteriaTotal)
	}
	if lines := systemEntriesContaining(t, store, sid, "round bound reached on a bare claim"); len(lines) != 1 || lines[0].ID != e.ID {
		t.Errorf("%d system lines carry the bare-claim handover; want exactly the outcome entry", len(lines))
	}
	assertLiveOutcomeMatchesEntry(t, events, sid, e)
}

func TestGoalOutcome_UserClear_StoppedByUserWithTheRealRounds(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "tidy the docs", recordedGoalCriteria("tidy the docs"), 2, time.Now())
	setGoalLatestReason(t, gid, "the index page is still missing")
	events, stop := recordAgentEvents(t, al)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	runGoalClear := func() string {
		_, _, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)
		return reply
	}
	if reply := runGoalClear(); !strings.Contains(reply, "cleared") {
		t.Fatalf("/goal clear reply = %q", reply)
	}
	if reply := runGoalClear(); !strings.Contains(reply, "No active goal") {
		t.Fatalf("second /goal clear reply = %q; want the no-goal reply", reply)
	}
	stop()

	e := requireOneGoalOutcome(t, store, sid, gid)
	o := e.GoalOutcome
	if o.Ending != generated.GoalOutcomeEndingStoppedByUser {
		t.Errorf("ending = %q; want stopped_by_user", o.Ending)
	}
	if o.RoundsUsed != 2 || o.MaxRounds != armedGoalMaxRounds {
		t.Errorf("rounds_used/max_rounds = %d/%d; want 2/%d (the rounds actually completed)", o.RoundsUsed, o.MaxRounds, armedGoalMaxRounds)
	}
	if o.JudgeReason != nil {
		t.Errorf("judge_reason = %q; a user stop carries none", *o.JudgeReason)
	}
	if e.AgentID != agentInst.ID {
		t.Errorf("outcome entry agent = %q; want %q", e.AgentID, agentInst.ID)
	}
	assertLiveOutcomeMatchesEntry(t, events, sid, e)
}

func TestGoalOutcome_IdleExpiry_OtherWithTheLatestReason(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "tidy the docs", recordedGoalCriteria("tidy the docs"), 3, time.Now().Add(-30*24*time.Hour))
	setGoalLatestReason(t, gid, "the index page is still missing")
	events, stop := recordAgentEvents(t, al)

	al.goalIdleExpirySweep(al.GetConfig().Planning, time.Now())
	stop()

	if st := mustGoalRecord(t, gid).State; st != generated.GoalStateExpired {
		t.Fatalf("setup: goal state = %q; want expired", st)
	}
	e := requireOneGoalOutcome(t, store, sid, gid)
	o := e.GoalOutcome
	if o.Ending != generated.GoalOutcomeEndingOther {
		t.Errorf("ending = %q; want other", o.Ending)
	}
	if o.RoundsUsed != 3 {
		t.Errorf("rounds_used = %d; want 3", o.RoundsUsed)
	}
	if o.JudgeReason == nil || *o.JudgeReason != "the index page is still missing" {
		t.Errorf("judge_reason = %v; want the goal's latest reason", o.JudgeReason)
	}
	if lines := systemEntriesContaining(t, store, sid, "idle-expired after"); len(lines) != 1 || lines[0].ID != e.ID {
		t.Errorf("%d system lines carry the idle-expiry handover; want exactly the outcome entry", len(lines))
	}
	assertLiveOutcomeMatchesEntry(t, events, sid, e)
}

func TestGoalOutcome_PlaceholderReasonIsNeverSent(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "tidy the docs", recordedGoalCriteria("tidy the docs"), 1, time.Now().Add(-30*24*time.Hour))
	setGoalLatestReason(t, gid, "(no reason recorded)")

	al.goalIdleExpirySweep(al.GetConfig().Planning, time.Now())

	e := requireOneGoalOutcome(t, store, sid, gid)
	if e.GoalOutcome.JudgeReason != nil {
		t.Errorf("judge_reason = %q; the internal placeholder must be omitted, not sent", *e.GoalOutcome.JudgeReason)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), `"judge_reason"`) {
		t.Errorf("persisted entry still serialises a judge_reason field: %s", raw)
	}
}

func TestGoalOutcome_AgentDeleted_OtherAndNoSeparateNote(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "tidy the docs", recordedGoalCriteria("tidy the docs"), 4, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
	events, stop := recordAgentEvents(t, al)

	ended, err := al.EndGoalsOfDeletedAgent(agentInst.ID, "Native")
	stop()
	if err != nil || ended != 1 {
		t.Fatalf("EndGoalsOfDeletedAgent: ended=%d err=%v; want 1, nil", ended, err)
	}

	e := requireOneGoalOutcome(t, store, sid, gid)
	o := e.GoalOutcome
	if o.Ending != generated.GoalOutcomeEndingOther || o.RoundsUsed != 4 {
		t.Errorf("ending/rounds_used = %q/%d; want other/4", o.Ending, o.RoundsUsed)
	}
	if lines := systemEntriesContaining(t, store, sid, "was deleted"); len(lines) != 1 || lines[0].ID != e.ID {
		t.Errorf("%d system lines tell the user the agent was deleted; want exactly the outcome entry", len(lines))
	}
	assertLiveOutcomeMatchesEntry(t, events, sid, e)
}

func TestGoalOutcome_TaskGoalEndingWritesIntoTheTaskSessionOnce(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	events, stop := recordAgentEvents(t, al)

	cases := []struct {
		name         string
		status       task.Status
		cancelReason task.CancelReason
		wantEnding   generated.GoalOutcomeEnding
	}{
		{name: "done", status: task.StatusDone, wantEnding: generated.GoalOutcomeEndingMet},
		{name: "stopped", status: task.StatusFailed, cancelReason: task.CancelReasonStoppedByUser, wantEnding: generated.GoalOutcomeEndingStoppedByUser},
		{name: "failed", status: task.StatusFailed, wantEnding: generated.GoalOutcomeEndingOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, sid := newGoalTestSession(t, al, agentInst.ID)
			taskID := "t-goal-outcome-" + tc.name
			g := seedTaskGoal(t, taskID, "the CSV has a header row", "no secrets in the export")
			activateSeededTaskGoal(t, g.GoalID, sid)
			if _, err := resolveGoalRecordStore().Update(g.GoalID, func(cur *goal.Goal) error {
				cur.Round = 2
				return nil
			}); err != nil {
				t.Fatalf("set round: %v", err)
			}

			tools.TerminateTaskGoalRecord(resolveGoalRecordStore(), taskID, tc.status, tc.cancelReason, "export shipped")
			// A second terminal write finds the goal already ended: no second line.
			tools.TerminateTaskGoalRecord(resolveGoalRecordStore(), taskID, tc.status, tc.cancelReason, "export shipped")

			e := requireOneGoalOutcome(t, store, sid, g.GoalID)
			o := e.GoalOutcome
			if o.Ending != tc.wantEnding {
				t.Errorf("ending = %q; want %q", o.Ending, tc.wantEnding)
			}
			if o.RoundsUsed != 2 || o.MaxRounds != config.DefaultGoalMaxRounds {
				t.Errorf("rounds_used/max_rounds = %d/%d; want 2/%d", o.RoundsUsed, o.MaxRounds, config.DefaultGoalMaxRounds)
			}
			if !strings.Contains(e.Content, "export shipped") {
				t.Errorf("outcome content %q does not say how the task ended", e.Content)
			}
		})
	}
	stop()
	var total int
	for _, e := range events.events {
		if e.Kind == EventKindGoalOutcome {
			total++
		}
	}
	if total != len(cases) {
		t.Errorf("%d live goal outcome events; want one per task ending (%d)", total, len(cases))
	}
}
