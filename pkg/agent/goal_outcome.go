// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_outcome.go writes the goal outcome entry — the one lasting line a
// goal's ending leaves in the chat thread (founder decision 2026-09-14;
// contracts/components/schemas/GoalOutcome.yaml).
//
// Every ending of a session-bound goal that has an outcome line goes through
// clearGoalWithOutcome, the sibling of clearGoalStatus that knows HOW the goal
// ended. clearGoal's signature is deliberately frozen and clearGoalStatus only
// knows a free-text note, so the call site that knows the ending — met, round
// bound, bare-claim round bound, user clear, idle expiry, agent deleted —
// passes the ending kind, the tries used and the deciding verdict explicitly.
// Only once the terminal transition has been saved (a goal was actually
// ended, ok == true) does it append ONE `type: system, system_subtype:
// goal_outcome` transcript entry and, right after that entry is saved, send
// the live goal_outcome frame. The entry id is minted once from the goal id
// and the terminal transition's own timestamp, and the live frame, the
// replayed frame (pkg/gateway/replay.go) and a cold REST load all carry it,
// so the thread shows exactly one line for the ending.
//
// The entry's Content is the plain handover text those call sites used to
// write as a separate free-text system entry. That separate write is gone:
// the ending is recorded once, not twice.
//
// A task-owned goal that ends because its TASK reached a terminal status never
// passes through clearGoalStatus — its single terminal writer is
// tools.TerminateTaskGoalRecord, in a package that cannot reach this one.
// recordTaskGoalOutcome is installed as that writer's after-transition hook
// (InstallTaskGoalOutcomeRecorder, wired at gateway boot), so the task's run
// session — which the task panel opens as a chat thread ("Open in Chat") —
// gets the same line.
package agent

import (
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// goalNoReasonRecorded is goalVerdictReasonText's internal "no reason" sentinel.
// It is fine as steering text but must never reach the outcome line as a judge
// reason (GoalOutcome.judge_reason: "MUST omit the field rather than send a
// placeholder").
const goalNoReasonRecorded = "(no reason recorded)"

// goalOutcomeInput is what a call site knows about HOW its goal ended.
type goalOutcomeInput struct {
	// ending is the outcome kind (GoalOutcome.ending).
	ending generated.GoalOutcomeEnding
	// roundsUsed is the tries count shown to the user. Always a real,
	// persisted count — never defaulted to the round limit.
	roundsUsed int
	// maxRounds is the round limit to report; <= 0 takes the ended record's.
	maxRounds int
	// verdict is the deciding Judge verdict, when one exists. Its
	// per-criterion count becomes criteria_total; nil omits that field.
	verdict *task.JudgeVerdict
	// judgeReason is the Judge's most recent reason. Empty, or the internal
	// goalNoReasonRecorded sentinel, omits the field — the contract forbids
	// sending a placeholder.
	judgeReason string
	// agentID attributes the transcript entry ("" for an engine-side ending).
	agentID string
	// content is the plain handover text stored as the entry's Content.
	content string
}

// clearGoalWithOutcome ends sessionID's active goal exactly as clearGoalStatus
// does — same terminal transition, same deferral on a refused store write,
// same terminal pill — and, only when a goal was actually ended and that end
// was saved, records its outcome line (recordGoalOutcome). The reply and ok
// are clearGoalStatus's own.
func (al *AgentLoop) clearGoalWithOutcome(
	sessionID string, store *session.UnifiedStore, note string, in goalOutcomeInput,
) (string, bool) {
	reply, ended, ok := al.endActiveGoal(sessionID, store, note)
	if ok && ended != nil {
		al.recordGoalOutcome(store, sessionID, ended.rec, ended.endedAt, in)
	}
	return reply, ok
}

// clearGoalByUser is `/goal clear` and its aliases: the user deliberately
// stopped the goal. The outcome line reads "stopped by you" with the rounds
// the goal had actually completed.
func (al *AgentLoop) clearGoalByUser(sessionID string, store *session.UnifiedStore, agentID string) string {
	in := goalOutcomeInput{ending: generated.GoalOutcomeEndingStoppedByUser, agentID: agentID}
	if rec := activeGoalForSession(sessionID); rec != nil {
		in.roundsUsed = rec.Round
		in.maxRounds = rec.MaxRounds
		in.content = fmt.Sprintf("Goal %q was stopped by the user after %d of %d round(s).",
			rec.Prompt, rec.Round, rec.MaxRounds)
	}
	reply, _ := al.clearGoalWithOutcome(sessionID, store, goalClearNoteUser, in)
	return reply
}

// recordGoalOutcome appends the goal outcome entry for rec's ending into
// sessionID's transcript and, only once that entry is saved, sends the live
// goal_outcome frame carrying the same id. A failed save is logged and counted
// and NO frame is sent: a line the thread would lose on the next reload is
// worse than the terminal pill alone, which the ending already emitted.
func (al *AgentLoop) recordGoalOutcome(
	store *session.UnifiedStore, sessionID string, rec *goal.Goal, endedAt time.Time, in goalOutcomeInput,
) {
	if rec == nil || sessionID == "" {
		return
	}
	if store == nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("agent", "goal: outcome line not recorded — no session store resolves the goal's session",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID, "ending": string(in.ending)})
		return
	}
	endedAt = endedAt.UTC()
	outcome := buildGoalOutcome(rec, endedAt, in)
	messageID := goalOutcomeMessageID(rec.GoalID, endedAt)
	content := strings.TrimSpace(in.content)
	if content == "" {
		content = fmt.Sprintf("Goal %q ended.", outcome.GoalText)
	}
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:            messageID,
		Type:          session.EntryTypeSystem,
		Role:          "system",
		Content:       content,
		AgentID:       in.agentID,
		Timestamp:     endedAt,
		SystemSubtype: session.SystemSubtypeGoalOutcome,
		GoalOutcome:   &outcome,
	}); err != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("agent", "goal: outcome entry could not be saved — no outcome line was sent",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID,
				"ending": string(in.ending), "error": err.Error()})
		return
	}
	logger.InfoCF("agent", "goal: outcome line recorded",
		map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID,
			"ending": string(in.ending), "rounds_used": outcome.RoundsUsed, "message_id": messageID})
	al.emitEvent(EventKindGoalOutcome, EventMeta{Source: "goal_loop"}, GoalOutcomePayload{
		SessionID: sessionID,
		MessageID: messageID,
		Outcome:   outcome,
	})
}

// goalOutcomeMessageID mints the id shared by the persisted entry and every
// frame for this ending. goal_id alone is not unique per ending (a task-owned
// goal can re-run and end again), so it carries the terminal transition's own
// timestamp; it is never a per-process counter, so it survives a restart.
func goalOutcomeMessageID(goalID string, endedAt time.Time) string {
	return fmt.Sprintf("goal-outcome-%s-%d", goalID, endedAt.UnixNano())
}

// buildGoalOutcome assembles the wire outcome for rec's ending, enforcing the
// contract's bounds: goal_text and max_rounds are never empty/zero, rounds_used
// is never negative, and the two optional fields are omitted rather than
// filled with a placeholder.
func buildGoalOutcome(rec *goal.Goal, endedAt time.Time, in goalOutcomeInput) generated.GoalOutcome {
	text := strings.TrimSpace(rec.Prompt)
	if text == "" {
		text = strings.TrimSpace(rec.Definition)
	}
	if text == "" {
		text = rec.GoalID
	}
	maxRounds := in.maxRounds
	if maxRounds < 1 {
		maxRounds = rec.MaxRounds
	}
	if maxRounds < 1 {
		maxRounds = config.DefaultGoalMaxRounds
	}
	rounds := in.roundsUsed
	if rounds < 0 {
		rounds = 0
	}
	out := generated.GoalOutcome{
		GoalId:     rec.GoalID,
		GoalText:   text,
		Ending:     in.ending,
		RoundsUsed: rounds,
		MaxRounds:  maxRounds,
		EndedAt:    endedAt,
	}
	if reason := strings.TrimSpace(in.judgeReason); reason != "" && reason != goalNoReasonRecorded {
		out.JudgeReason = &reason
	}
	if in.verdict != nil && len(in.verdict.PerCriterion) > 0 {
		n := len(in.verdict.PerCriterion)
		out.CriteriaTotal = &n
	}
	return out
}

// InstallTaskGoalOutcomeRecorder installs recordTaskGoalOutcome as the
// after-transition hook of tools.TerminateTaskGoalRecord — the one writer
// every task terminal path (task executor, plan engine, REST, system agent)
// shares — so a task-owned goal that ends with its task leaves the same
// outcome line in the task's run session. Called once at gateway boot. The
// hook is process-wide, like the goal trigger singleton: one AgentLoop per
// process.
func (al *AgentLoop) InstallTaskGoalOutcomeRecorder() {
	tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome)
}

// recordTaskGoalOutcome writes the outcome line for a task-owned goal that
// tools.TerminateTaskGoalRecord has just ended. ended is the record as saved;
// its LastActivityAt is the terminal transition's own timestamp.
func (al *AgentLoop) recordTaskGoalOutcome(ended goal.Goal, taskStatus task.Status) {
	sessionID := ended.ActiveSessionID
	if sessionID == "" {
		logger.WarnCF("agent", "goal: task goal ended with no session to show its outcome line in",
			map[string]any{"component": "goal", "goal_id": ended.GoalID, "task_id": ended.OwnerID})
		return
	}
	al.recordGoalOutcome(al.goalSessionStoreFor(sessionID), sessionID, &ended, ended.LastActivityAt,
		taskGoalOutcomeInput(&ended, taskStatus))
}

// taskGoalOutcomeInput maps a task-owned goal's terminal state onto the
// outcome kinds: met stays met; a user stop (the record's `cleared`) is
// stopped_by_user; a failed task is rounds_exhausted only when the goal really
// used its whole round limit, and `other` otherwise (a task can fail for
// reasons that have nothing to do with rounds).
func taskGoalOutcomeInput(ended *goal.Goal, taskStatus task.Status) goalOutcomeInput {
	in := goalOutcomeInput{
		roundsUsed: ended.Round,
		maxRounds:  ended.MaxRounds,
		content:    fmt.Sprintf("Goal %q ended with its task (%s): %s", ended.Prompt, taskStatus, ended.TerminalReason),
	}
	switch ended.State {
	case generated.GoalStateMet:
		in.ending = generated.GoalOutcomeEndingMet
		in.verdict = ended.LatestVerdict
	case generated.GoalStateCleared:
		in.ending = generated.GoalOutcomeEndingStoppedByUser
	default:
		if ended.MaxRounds > 0 && ended.Round >= ended.MaxRounds {
			in.ending = generated.GoalOutcomeEndingRoundsExhausted
			in.verdict = ended.LatestVerdict
		} else {
			in.ending = generated.GoalOutcomeEndingOther
		}
		in.judgeReason = ended.LatestReason
	}
	return in
}
