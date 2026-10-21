package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func isMetGoalMessage(msg generated.SessionMessage) bool {
	kind, _ := msg.Discriminator()
	if kind != "goal_status" {
		return false
	}
	verdict, err := msg.AsSessionMessageGoalStatus()
	return err == nil && verdict.Condition == generated.SessionMessageGoalStatusConditionMet
}

func upwardWakeDetails(child *session.LifecycleRecord, msg generated.SessionMessage) (string, *generated.HelperEvent) {
	kind, _ := msg.Discriminator()
	event := &generated.HelperEvent{HelperSessionId: child.SessionID, Text: deliverySummary(msg)}
	var reason string
	switch kind {
	case "handback":
		v, _ := msg.AsSessionMessageHandback()
		if v.Mode != generated.SessionMessageHandbackModeFinal {
			// A pause hand-back is a non-ending report (outcomeForKind maps it
			// to a blocker): its own row, never a goal ending (SFH-06).
			reason, event.Kind, event.CreatedAt = "blocker", generated.HelperEventKindBlocker, v.CreatedAt
			break
		}
		if child.GoalRef != "" {
			return "finished", nil
		}
		reason, event.Kind = "finished", generated.HelperEventKindFinished
		event.CreatedAt = v.CreatedAt
	case "question":
		reason, event.Kind = "question", generated.HelperEventKindQuestion
		v, _ := msg.AsSessionMessageQuestion()
		event.CreatedAt = v.CreatedAt
	case "blocker":
		reason, event.Kind = "blocker", generated.HelperEventKindBlocker
		v, _ := msg.AsSessionMessageBlocker()
		event.CreatedAt = v.CreatedAt
	case "error":
		v, _ := msg.AsSessionMessageError()
		if !v.Fatal && !strings.HasPrefix(v.Text, session.LifecycleNoticeReasonPrefixToolIterations) {
			return "", nil
		}
		reason, event.Kind, event.CreatedAt = "failed", generated.HelperEventKindFailed, v.CreatedAt
		if strings.Contains(v.Text, "Not verified") {
			reason, event.Kind, event.Text = "verdict", generated.HelperEventKindNotVerified, v.Text
		}
	case "goal_status":
		v, _ := msg.AsSessionMessageGoalStatus()
		if v.Condition == generated.SessionMessageGoalStatusConditionMet {
			return "verdict", nil
		}
		reason, event.Kind, event.CreatedAt = "verdict", generated.HelperEventKindVerdictUnmet, v.CreatedAt
		event.GoalId = &v.GoalId
		event.Text = "The goal is not met yet; the helper continues. " + deliverySummary(msg)
	default:
		return "", nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	return reason, event
}

// retainHelperEvent writes the independent repair payload before a wake can
// acknowledge its inbox entry. A failed row does not turn a saved report into
// a lost event: boot and the next delivery retry the lifecycle-journal source.
func (al *AgentLoop) retainHelperEvent(childID, destination, sourceID string, event generated.HelperEvent) error {
	event.Text = al.filterWakeText(event.Text)
	repair := session.HelperEventRepair{
		ID: "repair-" + helperEventMessageID(destination, sourceID, event.Kind), SourceID: sourceID,
		Destinations: []string{destination}, Event: event,
	}
	if err := al.GetSessionLifecycleStore().SaveHelperEventRepair(childID, repair); err != nil {
		return err
	}
	if err := al.applyHelperEventRepair(childID, repair); err != nil {
		return &helperEventRowPendingError{cause: err}
	}
	return nil
}

// preRetainUpwardHelperEvent saves the helper-event row's repair payload for
// an upward message before the message is appended to the inbox. It writes no
// row and wakes nothing; the delivery's own retainHelperEvent finds the same
// payload, applies it and retires it.
func (al *AgentLoop) preRetainUpwardHelperEvent(child *session.LifecycleRecord, destination string, msg generated.SessionMessage) error {
	_, event := upwardWakeDetails(child, msg)
	if event == nil {
		return nil
	}
	sourceID := messageIDOf(msg)
	if sourceID == "" {
		return nil
	}
	if store := al.GetSessionStore(); store == nil {
		return nil
	} else if _, err := store.GetMeta(destination); errors.Is(err, fs.ErrNotExist) {
		// A recipient without metadata is refused by the wake entry (PW-D12);
		// it gets no row, so no repair payload is owed to it.
		return nil
	} else if err != nil {
		return err
	}
	event.Text = al.filterWakeText(event.Text)
	return al.GetSessionLifecycleStore().SaveHelperEventRepair(child.SessionID, session.HelperEventRepair{
		ID: "repair-" + helperEventMessageID(destination, sourceID, event.Kind), SourceID: sourceID,
		Destinations: []string{destination}, Event: *event,
	})
}

type helperEventRowPendingError struct{ cause error }

func (e *helperEventRowPendingError) Error() string {
	return "helper event row is pending repair: " + e.cause.Error()
}
func (e *helperEventRowPendingError) Unwrap() error { return e.cause }

func (al *AgentLoop) retainPendingHelperEvent(childID, destination, sourceID string, event generated.HelperEvent) error {
	event.Text = al.filterWakeText(event.Text)
	return al.GetSessionLifecycleStore().SaveHelperEventRepair(childID, session.HelperEventRepair{
		ID: "repair-" + helperEventMessageID(destination, sourceID, event.Kind), SourceID: sourceID,
		Destinations: []string{destination}, Event: event, PendingNotice: true,
	})
}

func (al *AgentLoop) applyHelperEventRepair(childID string, repair session.HelperEventRepair) error {
	if repair.Event.GoalId != nil && strings.HasPrefix(repair.SourceID, "goal-outcome-") {
		return al.applyGoalOutcomeRepair(childID, repair)
	}
	var errs []error
	for _, destination := range repair.Destinations {
		if repair.PendingNotice {
			pending := repair.Event
			pending.Text = helperEventPendingText
			if err := al.saveHelperEventRow(destination, "repair-pending-"+helperEventMessageID(destination, repair.SourceID, repair.Event.Kind), pending); err != nil {
				errs = append(errs, err)
			}
		}
		if err := al.saveHelperEvent(destination, repair.SourceID, repair.Event); err != nil {
			al.publishHelperEventRepairPending(destination, repair.SourceID, repair.Event)
			errs = append(errs, err)
		}
	}
	if len(errs) != 0 {
		if err := al.GetSessionLifecycleStore().MarkHelperEventRepairPending(childID, repair.ID); err != nil {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	return al.GetSessionLifecycleStore().RetireHelperEventRepair(childID, repair.ID)
}

func (al *AgentLoop) repairHelperEvents(childID string) error {
	repairs, err := al.GetSessionLifecycleStore().PendingHelperEventRepairs(childID)
	if err != nil {
		return err
	}
	var errs []error
	for _, repair := range repairs {
		if err := al.applyHelperEventRepair(childID, repair); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

const helperEventPendingText = "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work."

// savedWakeReadPendingText is the chat's existing nonfatal pending line,
// worded for a helper report that is saved but could not be read at a live
// fence. The report stays queued and is retried; nothing was lost.
const savedWakeReadPendingText = "A helper report for this chat could not be read yet. Omnipus will retry. This is not a failure of the helper's work."

// publishSavedWakeReadPending (SFH-07) tells the open chat, through the same
// repair-pending helper-event line, that a saved helper report missed this
// live fence. The operator log and the queue retry are the caller's.
func (al *AgentLoop) publishSavedWakeReadPending(sessionID, outcomeID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	event := generated.HelperEvent{HelperSessionId: strings.SplitN(outcomeID, ":", 2)[0],
		Kind: generated.HelperEventKindBlocker, Text: savedWakeReadPendingText, CreatedAt: time.Now().UTC()}
	al.emitEvent(EventKindHelperEvent, EventMeta{Source: "helper_event"},
		HelperEventFrameFor(sessionID, "repair-pending-saved-input-"+outcomeID, event))
}

func (al *AgentLoop) publishHelperEventRepairPending(destination, sourceID string, event generated.HelperEvent) {
	id := "repair-pending-" + helperEventMessageID(destination, sourceID, event.Kind)
	event.Text = helperEventPendingText
	// A transient live line cannot stand in for the failed durable event. Its
	// distinct id is retained on the next repair pass before the real row.
	al.emitEvent(EventKindHelperEvent, EventMeta{Source: "helper_event"}, HelperEventFrameFor(destination, id, event))
}

func (al *AgentLoop) combinedEndingInput(child *session.LifecycleRecord, msg generated.SessionMessage) (string, error) {
	body := deliverySummary(msg)
	kind, _ := msg.Discriminator()
	if kind != "handback" || child.GoalRef == "" {
		return body, nil
	}
	if v, err := msg.AsSessionMessageHandback(); err == nil && v.Mode != generated.SessionMessageHandbackModeFinal {
		return body, nil // a pause is not a goal ending
	}
	goal, err := resolveGoalRecordStore().Get(child.GoalRef)
	if err != nil {
		return "", fmt.Errorf("wake ending: read goal %q: %w", child.GoalRef, err)
	}
	if goal == nil || goal.State != generated.GoalStateMet {
		return body, nil
	}
	var reasons []string
	if text := strings.TrimSpace(goal.LatestReason); text != "" && text != goalNoReasonRecorded {
		reasons = append(reasons, text)
	}
	if goal.LatestVerdict != nil {
		for _, criterion := range goal.LatestVerdict.PerCriterion {
			if text := strings.TrimSpace(criterion.Reason); text != "" {
				reasons = append(reasons, text)
			}
		}
	}
	var conditions []string
	for _, criterion := range goal.Criteria {
		conditions = append(conditions, criterion.Text)
	}
	for _, criterion := range goal.DoD {
		conditions = append(conditions, criterion.Text)
	}
	return fmt.Sprintf("Goal verdict: met. Goal: %s\nConditions: %s\nJudge reason: %s\n\n%s",
		goal.Prompt, strings.Join(conditions, "\n"), strings.Join(reasons, "\n"), body), nil
}

func withUpwardWakeSource(ctx context.Context, childID, publishKind string, event *generated.HelperEvent) context.Context {
	return context.WithValue(ctx, wakeSourceKey{}, wakeSource{childID: childID, event: event, publishKind: publishKind})
}
