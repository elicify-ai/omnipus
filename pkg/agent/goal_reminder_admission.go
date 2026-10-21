package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

const goalReminderNotice = "Goal reminder: continuing work toward the goal"

// An ordinary goal wake is still system input, never a human revival.
// Its generation is the generation observed BEFORE ordinary admission.
type ordinaryGoalWake struct {
	messageID  string
	goalID     string
	generation int
	runID      string
	bootSeq    uint64
}

var errOrdinaryGoalWakeIgnored = errors.New("goal reminder is obsolete or held")

func ordinaryGoalFollowUp(msg bus.InboundMessage) bool {
	return msg.Channel == "system" && msg.Sender.CanonicalID == goalLoopFollowUpSenderID &&
		inboundMetadata(msg, "goal_root_wake") == "true"
}

func (al *AgentLoop) ordinaryGoalFollowUpMetadata(sessionID, goalID, agentID string) (map[string]any, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil, errors.New("goal reminder: lifecycle store is not configured")
	}
	rec, err := lifecycle.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		gate := al.steerAdmission()
		gate.entryMu.Lock()
		rec, err = al.ensureOrdinaryRootRecord(lifecycle, sessionID, al.ResolveSessionStore(sessionID))
		gate.entryMu.Unlock()
	}
	if err != nil {
		return nil, fmt.Errorf("goal reminder: read recipient: %w", err)
	}
	if rec.SteeredBy != nil || rec.AgentID != agentID {
		return nil, errors.New("goal reminder: recipient is not the goal's ordinary owner")
	}
	previous := al.executionClaimFor(rec)
	return map[string]any{
		"goal_parent_run_id":   previous.RunID,
		"goal_parent_boot_seq": previous.BootSeq,
		"goal_root_wake":       "true",
		"goal_id":              goalID,
		"steer_message_id":     uuid.NewString(),
		"steer_generation":     rec.Generation,
	}, nil
}

func (al *AgentLoop) runOrdinaryGoalFollowUp(ctx context.Context, agent *AgentInstance, msg bus.InboundMessage, opts processOptions) (string, error) {
	generation, err := strconv.Atoi(inboundMetadata(msg, "steer_generation"))
	if err != nil || generation < 1 || inboundMetadata(msg, "steer_message_id") == "" || inboundMetadata(msg, "goal_id") == "" {
		return "", errors.New("goal reminder: missing or invalid wake identity")
	}
	if al.GetSessionLifecycleStore() == nil || opts.TranscriptStore == nil || opts.TranscriptSessionID == "" {
		return "", errors.New("goal reminder: no store-backed ordinary session")
	}
	bootSeq, bootErr := strconv.ParseUint(inboundMetadata(msg, "goal_parent_boot_seq"), 10, 64)
	if bootErr != nil {
		return "", fmt.Errorf("goal reminder: invalid parent boot identity: %w", bootErr)
	}
	opts.goalWake = &ordinaryGoalWake{
		messageID: inboundMetadata(msg, "steer_message_id"), goalID: inboundMetadata(msg, "goal_id"), generation: generation,
		runID: inboundMetadata(msg, "goal_parent_run_id"), bootSeq: bootSeq,
	}
	msg.SessionID, msg.SessionKey = opts.TranscriptSessionID, opts.SessionKey
	response, _, runErr := al.runInboundTurnWithRevival(ctx, agent, msg, opts)
	if errors.Is(runErr, errOrdinaryGoalWakeIgnored) {
		al.goalMarkIdleSettling(opts.goalWake.goalID, false)
		return "", nil
	}
	return response, runErr
}

// checkOrdinaryGoalWake runs after the predecessor wait, under entryMu, before
// revival or any identity stamp. A concurrent human turn cannot make an old
// reminder a new execution, and Stop/question holds do not consume its input.
func (al *AgentLoop) checkOrdinaryGoalWake(rec *session.LifecycleRecord, wake *ordinaryGoalWake) error {
	previous := al.executionClaimFor(rec)
	if rec.SteeredBy != nil || rec.Generation != wake.generation || previous.RunID != wake.runID || previous.BootSeq != wake.bootSeq {
		return errOrdinaryGoalWakeIgnored
	}
	store := al.ResolveSessionStore(rec.SessionID)
	if store == nil {
		return fmt.Errorf("goal reminder: transcript store for %q is unavailable", rec.SessionID)
	}
	entries, err := store.ReadTranscript(rec.SessionID)
	if err != nil {
		return fmt.Errorf("goal reminder: read acceptance receipt: %w", err)
	}
	for _, entry := range entries {
		if entry.ID == goalReminderEntryID(wake.messageID) {
			return errOrdinaryGoalWakeIgnored
		}
	}
	g, err := activeGoalForSession(rec.SessionID)
	if err != nil {
		return err
	}
	if g == nil || g.GoalID != wake.goalID || al.goalKeeperPausedByStop(rec.SessionID) ||
		al.goalIsWaitingOnUser(wake.goalID) || al.goalIsBlocked(wake.goalID) {
		return errOrdinaryGoalWakeIgnored
	}
	blocked, err := al.wakeBlocked(rec)
	if err != nil {
		return err
	}
	open, err := al.openQuestionCard(rec.SessionID)
	if err != nil {
		return err
	}
	if blocked || open || rec.State == session.LifecycleNeedsInput {
		return errOrdinaryGoalWakeIgnored
	}
	return nil
}

func goalReminderEntryID(messageID string) string { return "goal-reminder-" + messageID }

// The immutable chat event is also the ordinary wake's durable acceptance
// receipt. One append avoids a private instruction masquerading as a user
// bubble, a raw steering-marker bubble, and a two-write receipt/notice gap.
func (al *AgentLoop) recordOrdinaryGoalReminder(ts *turnState) (*generated.ReplayMessageFrame, error) {
	wake := ts.opts.goalWake
	entry := session.TranscriptEntry{
		ID: goalReminderEntryID(wake.messageID), Type: session.EntryTypeSystem, Role: "system",
		Content: goalReminderNotice, AgentID: ts.agent.ID, GoalID: wake.goalID, Timestamp: time.Now().UTC(),
	}
	written, err := ts.transcriptStore.AppendTranscriptOnce(ts.transcriptSessionID, entry)
	if err != nil {
		return nil, fmt.Errorf("goal reminder: save chat event: %w", err)
	}
	if !written {
		return nil, errOrdinaryGoalWakeIgnored
	}
	stamp := entry.Timestamp.Format(time.RFC3339Nano)
	return &generated.ReplayMessageFrame{
		Type: string(generated.WsFrameTypeReplayMessage), SessionId: ts.transcriptSessionID,
		Id: &entry.ID, Role: entry.Role, Content: entry.Content, AgentId: &entry.AgentID, GoalId: &entry.GoalID, Timestamp: &stamp,
	}, nil
}
