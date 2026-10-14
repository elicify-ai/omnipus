// goal_child_completion.go closes GitHub #947 defect 1 ("the hang"): a
// goal-bearing delegated (steered) child whose turn had ended stayed at
// lifecycle state `running` for ever — the parent never re-entered, the goal
// record stayed active for the keeper to iterate forever.
//
// The design (the approved design note, decisions (a)-(e); founder FD2=A):
// the goal path keeps DECIDING; the ADR-091 completion path owns FINISHING.
// One completion tail — Deliver, then terminalise — serves every exit
// (goal met, rounds exhausted, judge unavailable, session cancelled, boot
// recovery). MAJ-003 (sub-agent control plane) retires the FD1=A pair-end:
// NO terminal session write ends the session-owned goal — only natural
// met/round-exhaustion adjudication and an explicit clear (/goal clear,
// authorized clear_goal) do. The judge-unavailable arm of the
// deferred adjudication is re-driven a bounded number of times inside the
// already-off-critical-path goroutine, then the child is failed visibly.
//
// This file deliberately does NOT add a wall-clock reaper (FD2=A): completion
// is event-driven, and the boot sweep repairs crash residuals.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// goalJudgeRedriveAttempts bounds the re-drive of a judge-unavailable
// deferred adjudication (decision (c)): three attempts, the first plus two
// re-drives, after which the child is failed visibly through the completion
// tail. The goroutine is already off the turn's critical path; this bound is
// what keeps it from being unbounded in TIME.
const goalJudgeRedriveAttempts = 3

// goalJudgeRedriveBackoff is the short backoff between re-drive attempts
// (design note (c): "short backoff (e.g. 5s/15s/30s)"). It is deliberately
// NOT the verifier's own judgeRetryBackoff table (60s/120s/300s): that one
// spaces the retries INSIDE one judge round; this one spaces whole rounds.
// Waits run through judgeSleepFn — the same seam the verifier's backoff uses
// — so a test can collapse both the in-round retries and these waits exactly
// as it collapses the retries alone.
var goalJudgeRedriveBackoff = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second} //nolint:gochecknoglobals

// redriveGoalAdjudication is decision (c)'s bounded re-drive: it wraps the
// single shared adjudication body (runGoalAdjudication) so a judge-unavailable
// round no longer strands the pair. Each attempt is bounded by
// goalJudgeRoundTimeout exactly as the pre-fix dispatch was; between attempts
// the wrapper waits through judgeSleepFn, treating a failed wait as "retry
// now" (a cancelled wait only means the process is going away — the boot
// sweep covers the residual).
//
// On exhaustion the child is failed through the SAME completion tail the met
// arm uses (completeSteeredTurnAfterGoal): terminal `failed` with a reason
// naming the judge and a wake-eligible error to the parent. MAJ-003: the
// terminal write itself is NOT a goal ending — the goal record stays active
// and only natural adjudication or an explicit clear ends it. The claim needs
// no re-capture: it is persisted on the goal record, and every attempt re-reads
// the record fresh, so a goal cleared or restated mid-re-drive is simply not
// adjudicated further.
//
// A registry concurrency back-off (#984) is NOT re-driven: runGoalAdjudication
// resolves JudgeCriteriaResult.ConcurrencyBackoff to (false, false) — a
// final, silent stop for THIS wrapper, with no retry (the in-flight
// adjudication resolves the goal) and no child failure. Only a genuine
// judge outage (unavailable=true, reason != the concurrency sentinel) is
// retried and, on exhaustion, fails the child visibly.
func (al *AgentLoop) redriveGoalAdjudication(work *goalDeferredAdjudicationWork) {
	if al == nil || work == nil || work.sessionID == "" {
		return
	}
	store := al.GetSessionStore()
	if store == nil {
		logger.WarnCF("agent", "goal: deferred adjudication re-drive failed — no session store",
			map[string]any{"session_id": work.sessionID})
		return
	}
	for attempt := 1; attempt <= goalJudgeRedriveAttempts; attempt++ {
		if attempt > 1 {
			al.waitGoalRedriveBackoff(attempt - 1)
		}
		// The goal may have been cleared, restated or its session cancelled
		// since the last attempt — re-read fresh, exactly as the dispatch
		// does before its own single call today.
		rec, err := activeGoalForSession(work.sessionID)
		if err != nil {
			al.reportGoalReadError(work.sessionID, "deferred adjudication re-drive", err)
			return
		}
		if rec == nil {
			logger.InfoCF("agent", "goal: deferred adjudication re-drive stopped — no active goal on this session any more",
				map[string]any{"session_id": work.sessionID, "attempt": attempt})
			return
		}
		if work.goalID == "" {
			work.goalID, work.goalPrompt = rec.GoalID, rec.Prompt
		}
		if stale := work.staleFor(rec); stale != "" {
			// Issue #1306 R2-2: the claim belongs to the goal it was made
			// against. A replacement or restated goal is never judged on it.
			logger.InfoCF("agent", "goal: deferred adjudication re-drive stopped — "+stale,
				map[string]any{"session_id": work.sessionID, "goal_id": work.goalID, "active_goal_id": rec.GoalID, "attempt": attempt})
			return
		}
		// Issue #1306 R2-4: the outer round clock is the Judge's own check time
		// limit too — a later attempt inheriting this earlier deadline must be
		// reported as ErrJudgeTimeLimit, never as a provider timeout.
		ctx, cancel := context.WithTimeoutCause(withGoalProducer(context.Background(), work.producer), goalJudgeRoundTimeout, ErrJudgeTimeLimit)
		met, unavailable := al.runGoalAdjudication(ctx, work.agentInst, work.workspaceID, work.sessionID, store, rec, work.claimText,
			al.idleSteerDeliverer(work.sessionID, rec.GoalID, goalClaimDeferredSourceKind))
		cancel()
		if met || !unavailable {
			return
		}
		logger.WarnCF("agent", "goal: judge unavailable on a re-drive attempt — retrying while attempts remain",
			map[string]any{"session_id": work.sessionID, "goal_id": rec.GoalID,
				"attempt": attempt, "max_attempts": goalJudgeRedriveAttempts})
	}
	reason := fmt.Sprintf("judge unavailable after %d attempts: the completion claim could not be adjudicated", goalJudgeRedriveAttempts)
	// Issue #1306 (founder): the Judge must conclude. The conclusion names the
	// verdict AND carries the worker's claim, so the parent (or the chat user)
	// gets the actual work result with "not verified" — never a bare failure.
	conclusion := notVerifiedConclusion(reason,
		"The goal stays active: claim completion again once the Judge is available.", work.claimText)
	// SFH F1: the audience is decided from the lifecycle record; a read
	// failure is retried and then reported loudly — it never silently picks
	// the interactive (no-parent) audience.
	steered, audErr := al.goalConclusionAudience(work.sessionID)
	if audErr != nil {
		al.reportUndeliverableGoalConclusion(store, work, conclusion, audErr)
		return
	}
	if !steered {
		al.concludeInteractiveGoalNotVerified(store, work, conclusion)
		return
	}
	logger.ErrorCF("agent", "goal: "+reason+" — failing the goal-bearing child visibly",
		map[string]any{"session_id": work.sessionID})
	al.completeSteeredTurnAfterGoal(withGoalProducer(context.Background(), work.producer), work.sessionID, "", errors.New(conclusion))
}

// goalConclusionAudienceAttempts bounds the lifecycle re-reads before an
// unreadable audience is reported as undeliverable.
const goalConclusionAudienceAttempts = 3

// goalConclusionAudience reports whether sessionID is a steered child (its
// parent receives the conclusion) or a parentless chat (the user does).
// Parentless is decided only from positive evidence: no lifecycle store
// wired, no lifecycle record at all (a steered child's record is written
// atomically at launch, so a plain chat without one is parentless by
// construction), or a record with no steering edge. A record that exists but
// cannot be read is retried and, if it stays unreadable, returned as an
// error — never guessed (SFH F1).
func (al *AgentLoop) goalConclusionAudience(sessionID string) (steered bool, err error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, nil
	}
	for attempt := 1; ; attempt++ {
		rec, loadErr := lifecycle.Load(sessionID)
		switch {
		case loadErr == nil && rec != nil:
			return rec.SteeredBy != nil, nil
		case errors.Is(loadErr, session.ErrLifecycleNotFound):
			return false, nil
		case loadErr == nil:
			err = fmt.Errorf("the lifecycle store returned no record and no error for session %q", sessionID)
		default:
			err = fmt.Errorf("read lifecycle record for session %q: %w", sessionID, loadErr)
		}
		if attempt >= goalConclusionAudienceAttempts {
			return false, err
		}
		logger.WarnCF("agent", "goal: could not read who should receive the Judge conclusion — retrying",
			map[string]any{"session_id": sessionID, "attempt": attempt, "error": err.Error()})
		al.waitGoalRedriveBackoff(attempt)
	}
}

// reportUndeliverableGoalConclusion is the loud arm of SFH F1: the conclusion
// could not be routed because the session's lifecycle record stayed
// unreadable. It is recorded durably in the session's own transcript with the
// reason it was not delivered, and raised as a visible error — never dropped,
// never sent to a guessed audience.
func (al *AgentLoop) reportUndeliverableGoalConclusion(store *session.UnifiedStore, work *goalDeferredAdjudicationWork, conclusion string, audErr error) {
	note := conclusion + "\n\nThis conclusion could not be delivered: the session's record could not be read (" + audErr.Error() + ")."
	logger.ErrorCF("agent", "goal: the Judge conclusion could not be delivered — the conclusion audience is unreadable",
		map[string]any{"session_id": work.sessionID, "error": audErr.Error()})
	if werr := appendGoalConclusionFn(al, store, work.sessionID, goalWorkAgentID(work), note); werr != nil {
		logger.ErrorCF("agent", "goal: the undeliverable Judge conclusion could not be saved either",
			map[string]any{"session_id": work.sessionID, "error": werr.Error()})
	}
	al.emitEvent(EventKindError, EventMeta{Source: "goal_loop", SessionKey: work.sessionID}, ErrorPayload{
		Stage: "goal_conclusion", Code: string(CodeUnknown), SessionID: work.sessionID,
		Message: "The Judge's conclusion could not be delivered: " + audErr.Error(),
	})
}

// appendGoalConclusionFn writes a Judge conclusion to the session transcript
// and reports whether it was saved. A variable only so a test can make the
// write fail; production always uses writeGoalSystemTranscriptErr.
var appendGoalConclusionFn = func(al *AgentLoop, store *session.UnifiedStore, sessionID, agentID, content string) error {
	return al.writeGoalSystemTranscriptErr(store, sessionID, agentID, content)
}

func goalWorkAgentID(work *goalDeferredAdjudicationWork) string {
	if work.agentInst != nil {
		return work.agentInst.ID
	}
	return ""
}

// concludeInteractiveGoalNotVerified is the interactive (/goal chat) arm of
// the Judge-unavailable conclusion: there is no parent, the user is the
// audience, wherever they started the goal.
//
//   - Durable first (SFH F4): the conclusion is written to the chat
//     transcript AND to the goal record's latest reason (which a reconnect
//     rehydrates). A failed write is retried once and then raised as a
//     visible error — the live delivery below never stands in for it.
//   - Then delivered (CR F3): to the goal's recorded channel/chat route as
//     an ordinary outbound message (Telegram, Slack, … and webchat alike —
//     no new agent turn), plus the live goal pill for the web card.
//
// The worker's answer was already published before adjudication started;
// the goal stays active so the work can be claimed again.
func (al *AgentLoop) concludeInteractiveGoalNotVerified(store *session.UnifiedStore, work *goalDeferredAdjudicationWork, conclusion string) {
	logger.ErrorCF("agent", "goal: the Judge could not conclude — recording a not-verified conclusion",
		map[string]any{"session_id": work.sessionID, "goal_id": work.goalID})
	saveErr := appendGoalConclusionFn(al, store, work.sessionID, goalWorkAgentID(work), conclusion)
	if saveErr != nil {
		saveErr = appendGoalConclusionFn(al, store, work.sessionID, goalWorkAgentID(work), conclusion)
	}
	// Issue #1306 R2-2: the transcript keeps the conclusion as history, but
	// the goal's own reason and pill change only for the goal this claim was
	// made against, re-checked under that goal's lock — a goal cleared,
	// replaced or restated while the conclusion was being saved is untouched.
	concluded, recErr := work.recordConclusionOnGoal(conclusion)
	if recErr != nil {
		saveErr = errors.Join(saveErr, fmt.Errorf("goal record: %w", recErr))
	}
	if saveErr != nil {
		logger.ErrorCF("agent", "goal: the Judge conclusion could not be saved durably",
			map[string]any{"session_id": work.sessionID, "error": saveErr.Error()})
		al.emitEvent(EventKindError, EventMeta{Source: "goal_loop", SessionKey: work.sessionID}, ErrorPayload{
			Stage: "goal_conclusion", Code: string(CodeUnknown), SessionID: work.sessionID,
			Message: "The Judge's conclusion could not be saved to this chat: " + saveErr.Error(),
		})
	}
	al.deliverGoalConclusionToRoute(work.sessionID, conclusion)
	if concluded != nil {
		al.emitGoalStatusFrame(work.sessionID, concluded.GoalID, concluded.Prompt, concluded.Round, concluded.MaxRounds, conclusion, goalPillJudgeUnavailable)
	}
}

// staleFor names why the active goal rec is no longer the goal this claim
// was made against, or returns "" while it still is (issue #1306 R2-2): a
// different goal bound to the session, the same goal restated (the UAT E-1
// rule: a changed prompt), or a newer claim recorded on it since.
func (work *goalDeferredAdjudicationWork) staleFor(rec *goal.Goal) string {
	switch {
	case rec == nil || !rec.IsActive():
		return "the goal has ended"
	case rec.GoalID != work.goalID:
		return "the goal was replaced by another goal"
	case rec.ActiveSessionID != "" && rec.ActiveSessionID != work.sessionID:
		return "the goal is no longer bound to this session"
	case strings.TrimSpace(rec.Prompt) != strings.TrimSpace(work.goalPrompt):
		return "the goal was restated, so the claim was made against a superseded definition"
	case !work.claimRecordedAt.IsZero() && rec.LatestClaim != nil && rec.LatestClaim.ClaimedAt.After(work.claimRecordedAt):
		return "a newer claim was recorded on the goal"
	}
	return ""
}

// recordConclusionOnGoal writes conclusion as the goal's latest reason only
// while the goal is still the one this claim was made against, checked under
// the goal's own lock. It returns the updated goal when written, nil when the
// goal was no longer applicable (not an error: the transcript keeps the
// conclusion as history), and an error when the record could not be updated.
func (work *goalDeferredAdjudicationWork) recordConclusionOnGoal(conclusion string) (*goal.Goal, error) {
	if work.goalID == "" {
		return nil, errors.New("the adjudicated goal was never identified")
	}
	applied := false
	updated, err := resolveGoalRecordStore().Update(work.goalID, func(cur *goal.Goal) error {
		if stale := work.staleFor(cur); stale != "" {
			logger.InfoCF("agent", "goal: the Judge conclusion is kept in the transcript only — "+stale,
				map[string]any{"session_id": work.sessionID, "goal_id": work.goalID})
			return nil
		}
		cur.LatestReason = conclusion
		applied = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, nil //nolint:nilnil // nil goal + nil error means "no longer applicable", documented above.
	}
	return updated, nil
}

// deliverGoalConclusionToRoute publishes the conclusion to the channel/chat
// the goal was started from (CR F3). A route that cannot be read, or a
// publish that fails, is logged and raised — the durable copy already exists.
func (al *AgentLoop) deliverGoalConclusionToRoute(sessionID, conclusion string) {
	route, err := goalTriggers().routeFor(sessionID)
	if err == nil && (route.channel == "" || route.chatID == "") {
		err = errors.New("the goal has no recorded channel route")
	}
	if err == nil && al.bus == nil {
		err = errors.New("no message bus is wired")
	}
	if err == nil {
		err = al.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
			Channel:   route.channel,
			ChatID:    route.chatID,
			SessionID: sessionID,
			Content:   conclusion,
		})
	}
	if err != nil {
		logger.ErrorCF("agent", "goal: the Judge conclusion could not be delivered to the goal's chat",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		al.emitEvent(EventKindError, EventMeta{Source: "goal_loop", SessionKey: sessionID}, ErrorPayload{
			Stage: "goal_conclusion", Code: string(CodeUnknown), SessionID: sessionID,
			Message: "The Judge's conclusion could not be delivered to this chat's channel: " + err.Error(),
		})
	}
}

// waitGoalRedriveBackoff spaces two re-drive attempts through judgeSleepFn.
// A failed wait never aborts the re-drive: judgeSleepFn's error means only
// that the wait did not run its course (the substituted test seam, or a
// process shutdown) — the attempts are each timeout-bounded on their own, so
// retrying immediately is the honest interpretation.
func (al *AgentLoop) waitGoalRedriveBackoff(previousAttempt int) {
	idx := previousAttempt - 1
	if idx > len(goalJudgeRedriveBackoff)-1 {
		idx = len(goalJudgeRedriveBackoff) - 1
	}
	delay := goalJudgeRedriveBackoff[idx]
	waitCtx, cancel := context.WithTimeout(context.Background(), delay)
	defer cancel()
	if err := judgeSleepFn(waitCtx, delay); err != nil {
		logger.InfoCF("agent", "goal: re-drive backoff did not run its course — retrying now",
			map[string]any{"delay": delay.String()})
	}
}

// goalParkUpwardText picks the parent-facing text for a park delivery
// (decision (d)): the goal_claim tool's evidence/reason argument when the
// park came through the tool, otherwise the turn's own final content with the
// GOAL_STATUS control line stripped — the marker is control prose, not the
// worker's question to its parent.
func goalParkUpwardText(evidence, finalContent string) string {
	if strings.TrimSpace(evidence) != "" {
		return strings.TrimSpace(evidence)
	}
	lines := strings.Split(finalContent, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), goalStatusLabel+":") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// deliverGoalParkUpward is decision (d): a goal-bearing steered child that
// parks — GOAL_STATUS: waiting_on_user, or a goal_claim tool `blocked` —
// tells its parent through the ONE upward path (Deliver), so a park is a
// question the parent can answer, not silence. Both outcomes are already
// wake-eligible (steer_audience.go::validateOutcomeMessage); the keeper's
// suppression while a park holds is untouched (it is correct — nothing should
// push while a question is out).
//
// An interactive goal chat (no steering parent) is skipped silently: the user
// is already the audience of that transcript, and Deliver has no one else to
// tell.
func (al *AgentLoop) deliverGoalParkUpward(sessionID string, blocked bool, text string) {
	if al == nil || sessionID == "" {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil || rec == nil {
		return
	}
	if rec.SteeredBy == nil {
		return
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		logger.WarnCF("agent", "goal: park delivery skipped — no upward deliverer is wired",
			map[string]any{"session_id": sessionID})
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "the worker needs input before it can continue"
	}
	now := time.Now().UTC()
	parent := rec.SteeredBy.SteeringSessionID
	var (
		message generated.SessionMessage
		outcome steer.Outcome
		berr    error
	)
	if blocked {
		outcome = steer.OutcomeBlocker
		berr = message.FromSessionMessageBlocker(generated.SessionMessageBlocker{
			MessageId:       uuid.NewString(),
			SessionId:       rec.SessionID,
			ParentSessionId: &parent,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            text,
			Severity:        generated.SessionMessageBlockerSeverityMedium,
		})
	} else {
		// ADR-20261004 (locked decision 6): a question is an ordinary upward
		// message — no wait, no authority, no park. The parent replies
		// through ordinary steering.
		outcome = steer.OutcomeParkedQuestion
		berr = message.FromSessionMessageQuestion(generated.SessionMessageQuestion{
			MessageId:       uuid.NewString(),
			SessionId:       rec.SessionID,
			ParentSessionId: &parent,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            text,
			CorrelationId:   uuid.NewString(),
		})
	}
	if berr != nil {
		logger.WarnCF("agent", "goal: park delivery failed — the message could not be encoded",
			map[string]any{"session_id": sessionID, "error": berr.Error()})
		return
	}
	event := steer.UpwardEvent{ChildSessionID: rec.SessionID, Outcome: outcome, Message: message}
	delivery, derr := deliverer.Deliver(context.Background(), event)
	if derr != nil {
		logger.WarnCF("agent", "goal: park delivery failed — the parent was not told",
			map[string]any{"session_id": sessionID, "error": derr.Error()})
		return
	}
	reportUndeliveredWake("goal: park delivery", event, steerParentSessionID(rec), rec.Generation, delivery)
}

// ackMetVerdictEntry acknowledges the session-goal met verdict's inbox entry
// after the hand-back produces the parent wake (founder Q1=A, #984 follow-up).
// The met verdict is delivered with its wake suppressed
// (deliverGoalVerdictUpward's SuppressWake); the completion handback that
// follows it is the one parent wake for the whole "met". An entry delivered
// without a wake is consumed by nothing — leaving it unacked would make boot recovery
// (boot_sweep.go::unacknowledged) re-deliver AND re-wake it on every later
// restart, which is exactly the re-entry Q1=A removes.
//
// Best-effort by design: the tail (completeSteeredTurnAfterGoal) runs FIRST
// so a crash before the hand-back wake leaves both entries unacked for boot
// recovery to re-deliver. Once the hand-back has woken, acknowledging the
// wake-suppressed verdict is crash-safe because the deterministic hand-back
// is itself durable and remains unacknowledged until the parent's normal
// consumption marker retires it; a crash before consumption therefore makes
// boot recovery re-deliver that hand-back as the one durable wake carrier.
// This ack runs AFTER the tail returns, so it never races the tail's Deliver.
// No parent edge → nothing to ack (a task-owned goal has no steered edge and
// its verdict wake is load-bearing).
func (al *AgentLoop) ackMetVerdictEntry(sessionID, goalID string, round int) {
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		logger.WarnCF("agent", "goal: met verdict ack skipped — could not load the child's lifecycle record",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": err.Error()})
		return
	}
	if rec == nil || rec.SteeredBy == nil {
		return
	}
	ownerKey := deliverOwnerKey(rec)
	if ownerKey == "" {
		return
	}
	id := goalVerdictUpwardMessageID(goalID, round)
	if aerr := inbox.Ack(ownerKey, []string{id}); aerr != nil {
		logger.WarnCF("agent", "goal: met verdict ack failed — boot recovery may re-wake this verdict once",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "message_id": id, "error": aerr.Error()})
	}
}

// wakeMetVerdictEntry re-delivers the already-durable, unacknowledged met
// verdict without wake suppression when no deterministic final hand-back
// woke the parent. Deliver's duplicate handling deliberately re-runs the wake
// for an unacknowledged wake-eligible entry, so this creates no second inbox
// row. Publishing or queueing is not consumption: every outcome leaves the
// verdict unacknowledged until processSteeredSystemWake or
// consumeDequeuedSteering writes the parent's normal consumed marker. That
// preserves boot recovery's ability to re-deliver after a crash between wake
// publication and actual consumption. A missing or stopped parent produces
// DeliveryStoredNotWoken and returns an error under the same durable posture.
func (al *AgentLoop) wakeMetVerdictEntry(sessionID, goalID string, round int) error {
	inbox := al.GetMessageInboxStore()
	lifecycle := al.GetSessionLifecycleStore()
	deliverer := al.getUpwardDeliverer()
	if inbox == nil || lifecycle == nil || deliverer == nil {
		return errors.New("goal: met verdict fallback wake dependencies are not wired")
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return fmt.Errorf("load child lifecycle: %w", err)
	}
	if rec == nil || rec.SteeredBy == nil {
		return errors.New("goal: met verdict fallback wake has no steering edge")
	}
	wantID := goalVerdictUpwardMessageID(goalID, round)
	message, found, err := findUnackedInboxMessage(inbox, deliverOwnerKey(rec), sessionID, wantID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("goal: met verdict %q is not present and unacknowledged", wantID)
	}
	event := steer.UpwardEvent{ChildSessionID: sessionID, Outcome: steer.OutcomeGoalVerdict, Message: message}
	delivery, err := deliverer.Deliver(context.Background(), event)
	if err != nil {
		return fmt.Errorf("wake stored met verdict: %w", err)
	}
	reportUndeliveredWake("goal: met verdict fallback wake", event, steerParentSessionID(rec), rec.Generation, delivery)
	if !deliveryWokeRecipient(delivery.Outcome) {
		return fmt.Errorf("wake stored met verdict: delivery outcome %q did not wake the parent", delivery.Outcome)
	}
	return nil
}

func findUnackedInboxMessage(inbox *session.MessageInboxStore, ownerKey, childSessionID, messageID string) (generated.SessionMessage, bool, error) {
	cursor := ""
	for {
		messages, next, more, err := inbox.Drain(ownerKey, childSessionID, cursor, session.DefaultInboxUnackedMax)
		if err != nil {
			return generated.SessionMessage{}, false, fmt.Errorf("drain parent inbox: %w", err)
		}
		for _, message := range messages {
			if messageIDOf(message) == messageID {
				return message, true, nil
			}
		}
		if !more {
			return generated.SessionMessage{}, false, nil
		}
		cursor = next
	}
}
