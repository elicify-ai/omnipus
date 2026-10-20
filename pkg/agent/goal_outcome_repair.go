package agent

import (
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// repairGoalOutcome reads the terminal goal payload, never an acknowledged
// inbox body. Repeating the writer repairs either transcript without a wake.
func (al *AgentLoop) repairGoalOutcome(sessionID string) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	child, err := lifecycle.Load(sessionID)
	if err != nil || child.GoalRef == "" {
		return err
	}
	return al.repairSpecificGoalOutcome(sessionID, child.GoalRef, child.AgentID)
}

func (al *AgentLoop) repairSpecificGoalOutcome(sessionID, goalID, agentID string) error {
	rec, err := resolveGoalRecordStore().Get(goalID)
	if err != nil {
		return err
	}
	if rec == nil || rec.IsActive() {
		return nil
	}
	in := goalOutcomeInputFromRecord(rec, agentID)
	if err := al.recordGoalOutcome(al.GetSessionStore(), sessionID, rec, rec.LastActivityAt, in); err != nil {
		al.reportGoalOutcomePending(sessionID, rec, rec.LastActivityAt, err)
		return &helperEventRowPendingError{cause: err}
	}
	return nil
}

func (al *AgentLoop) reportGoalOutcomePending(sessionID string, rec *goal.Goal, endedAt time.Time, cause error) {
	logger.ErrorCF("agent", "goal outcome row is pending repair", map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": cause.Error()})
	event := generated.HelperEvent{HelperSessionId: sessionID, Kind: generated.HelperEventKindFinished,
		GoalId: &rec.GoalID, CreatedAt: endedAt.UTC(),
		Text: "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work."}
	ids := map[string]string{sessionID: goalOutcomeMessageID(rec.GoalID, endedAt)}
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		if child, err := lifecycle.Load(sessionID); err == nil && child.SteeredBy != nil {
			parentID := child.SteeringSessionID()
			ids[parentID] = fmt.Sprintf("goal-outcome-%s-%s-%d", parentID, rec.GoalID, endedAt.UnixNano())
		}
	}
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		destinations := make([]string, 0, len(ids))
		for destination := range ids {
			destinations = append(destinations, destination)
		}
		sourceID := goalOutcomeMessageID(rec.GoalID, endedAt)
		if err := lifecycle.SaveHelperEventRepair(sessionID, session.HelperEventRepair{
			ID: "repair-" + sourceID, SourceID: sourceID, Destinations: destinations, Event: event, PendingNotice: true,
		}); err != nil {
			al.emitJudgeConclusionFailed(sessionID, "The goal outcome repair notice could not be retained: "+err.Error())
		}
	}
	for destination, rowID := range ids {
		al.emitEvent(EventKindHelperEvent, EventMeta{Source: "goal_outcome"}, HelperEventFrameFor(destination, "repair-pending-"+rowID, event))
	}
}

func (al *AgentLoop) retainGoalOutcomeRepair(sessionID string, destinations []string, outcome generated.GoalOutcome) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return fmt.Errorf("goal outcome: repair journal unavailable")
	}
	sourceID := goalOutcomeMessageID(outcome.GoalId, outcome.EndedAt)
	return lifecycle.SaveHelperEventRepair(sessionID, session.HelperEventRepair{
		ID: "repair-" + sourceID, SourceID: sourceID, Destinations: destinations, PendingNotice: true,
		Event: generated.HelperEvent{HelperSessionId: sessionID, GoalId: &outcome.GoalId,
			Kind: generated.HelperEventKindFinished, Text: helperEventPendingText, CreatedAt: outcome.EndedAt},
		GoalOutcome: &outcome,
	})
}

func (al *AgentLoop) applyGoalOutcomeRepair(childID string, repair session.HelperEventRepair) error {
	child, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		return err
	}
	var outcome generated.GoalOutcome
	if repair.GoalOutcome != nil {
		outcome = *repair.GoalOutcome
	} else {
		rec, err := resolveGoalRecordStore().Get(*repair.Event.GoalId)
		if err != nil {
			return err
		}
		if rec == nil || rec.IsActive() || !rec.LastActivityAt.Equal(repair.Event.CreatedAt) {
			return fmt.Errorf("goal outcome repair %q: the original terminal payload is unavailable", repair.ID)
		}
		outcome = buildGoalOutcome(rec, repair.Event.CreatedAt, goalOutcomeInputFromRecord(rec, child.AgentID))
	}
	outcome.GoalText = al.filterWakeText(outcome.GoalText)
	if outcome.JudgeReason != nil {
		filtered := al.filterWakeText(*outcome.JudgeReason)
		outcome.JudgeReason = &filtered
	}
	for _, destination := range repair.Destinations {
		rowID := repair.SourceID
		if destination != childID {
			rowID = fmt.Sprintf("goal-outcome-%s-%s-%d", destination, *repair.Event.GoalId, repair.Event.CreatedAt.UnixNano())
		}
		pending := repair.Event
		pending.Text = helperEventPendingText
		if err := al.saveHelperEventRow(destination, "repair-pending-"+rowID, pending); err != nil {
			return err
		}
		if err := al.appendGoalOutcome(al.GetSessionStore(), destination, rowID, outcome, child.AgentID,
			fmt.Sprintf("Goal %q ended.", outcome.GoalText)); err != nil {
			return err
		}
	}
	return al.GetSessionLifecycleStore().RetireHelperEventRepair(childID, repair.ID)
}

func (al *AgentLoop) ackSavedEndingVerdict(ctxSource wakeSource, recipient, outcomeID string) error {
	if ctxSource.childID == "" {
		return nil
	}
	child, err := al.GetSessionLifecycleStore().Load(ctxSource.childID)
	if err != nil {
		return err
	}
	if child.GoalRef == "" || outcomeID != fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation) || child.SteeringSessionID() != recipient {
		return nil
	}
	rec, err := resolveGoalRecordStore().Get(child.GoalRef)
	if err != nil {
		return err
	}
	if rec == nil || rec.State != generated.GoalStateMet {
		return nil
	}
	return al.ackMetVerdictEntry(child.SessionID, rec.GoalID, rec.Round)
}

func goalOutcomeInputFromRecord(rec *goal.Goal, agentID string) goalOutcomeInput {
	in := goalOutcomeInput{roundsUsed: rec.Round, maxRounds: rec.MaxRounds,
		verdict: rec.LatestVerdict, judgeReason: rec.LatestReason, agentID: agentID}
	switch rec.State {
	case generated.GoalStateMet:
		in.ending = generated.GoalOutcomeEndingMet
	case generated.GoalStateExhausted:
		in.ending = generated.GoalOutcomeEndingRoundsExhausted
	case generated.GoalStateCleared:
		in.ending = generated.GoalOutcomeEndingStoppedByUser
	default:
		in.ending = generated.GoalOutcomeEndingOther
	}
	return in
}

func verdictReasonForOutcome(in goalOutcomeInput) string {
	if text := strings.TrimSpace(in.judgeReason); text != "" && text != goalNoReasonRecorded {
		return text
	}
	if in.verdict == nil {
		return ""
	}
	var reasons []string
	for _, criterion := range in.verdict.PerCriterion {
		if text := strings.TrimSpace(criterion.Reason); text != "" {
			reasons = append(reasons, text)
		}
	}
	return strings.Join(reasons, "\n")
}
