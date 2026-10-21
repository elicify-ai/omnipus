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
// authorized clear_goal) do. A transient judge-unavailable outcome is re-driven
// a bounded number of times inside the already-off-critical-path goroutine,
// then the child is failed visibly. Operator-only refusals conclude at once.
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
// single shared adjudication body (runGoalAdjudicationOutcome) so a judge-unavailable
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
// adjudication resolves the goal) and no child failure. An unavailable outcome
// that needs an operator ends immediately with its safe corrective action;
// other unavailable outcomes keep the bounded retry and visible conclusion.
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
	var lastOutcome goalAdjudicationOutcome
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
		ctx, cancel := context.WithTimeoutCause(withGoalAnswer(withGoalProducer(context.Background(), work.producer), work.answer),
			goalJudgeRoundTimeout, ErrJudgeTimeLimit)
		lastOutcome = al.runGoalAdjudicationOutcome(ctx, work.agentInst, work.workspaceID, work.sessionID, store, rec, work.claimText,
			al.idleSteerDeliverer(work.sessionID, rec.GoalID, goalClaimDeferredSourceKind))
		cancel()
		if lastOutcome.met || !lastOutcome.unavailable {
			return
		}
		if lastOutcome.needsOperator {
			break // another unchanged check cannot fix the Judge's settings
		}
		logger.WarnCF("agent", "goal: judge unavailable on a re-drive attempt — retrying while attempts remain",
			map[string]any{"session_id": work.sessionID, "goal_id": rec.GoalID,
				"attempt": attempt, "max_attempts": goalJudgeRedriveAttempts})
	}
	reason := fmt.Sprintf("judge unavailable after %d attempts: the completion claim could not be adjudicated", goalJudgeRedriveAttempts)
	nextStep := "The goal stays active: claim completion again once the Judge is available."
	if lastOutcome.needsOperator {
		reason = lastOutcome.reason // already safe, provider/model-specific guidance
		nextStep = "The goal stays active: correct the Judge's settings, then claim completion again."
	}
	// Issue #1306 (founder): the Judge must conclude. The conclusion names the
	// verdict AND carries the worker's claim, so the parent (or the chat user)
	// gets the actual work result with "not verified" — never a bare failure.
	conclusion := notVerifiedConclusion(reason, nextStep, work.upwardAnswer())
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
	al.completeSteeredTurnAfterGoal(withGoalProducer(context.Background(), work.producer), work.sessionID, "", &curatedTurnError{text: conclusion})
}

// goalConclusionAudienceAttempts bounds the lifecycle re-reads before an
// unreadable audience is reported as undeliverable.
const goalConclusionAudienceAttempts = 3

// goalConclusionAudience reports whether sessionID is a steered child (its
// parent receives the conclusion) or an ordinary root (the user does).
//
// The answer comes from the canonical audience boundary
// (steer_audience.go::SteerAudienceResolver over
// steer_classify.go::SteerRecordClassifier), which reads the lifecycle record
// AND the session's metadata and requires them to agree (architect R2-A3).
// Only a positively classified ordinary root takes the human path. A damaged
// child (record lost, or edge lost while the metadata still names a parent),
// a legacy delegate or an invalid edge has no safe audience and is returned
// as an error, never guessed parentless. A read failure is retried and, if it
// persists, returned as an error too (SFH F1). Either error routes the
// conclusion to reportUndeliverableGoalConclusion.
func (al *AgentLoop) goalConclusionAudience(sessionID string) (steered bool, err error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		// SteerLauncher.Launch refuses a child without a lifecycle store, so
		// no session here can be a steered child.
		return false, nil
	}
	sessions := al.ResolveSessionStore(sessionID)
	if sessions == nil {
		sessions = al.GetSessionStore()
	}
	if sessions == nil {
		return false, fmt.Errorf("no session store to classify session %q", sessionID)
	}
	resolver := NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, sessions))
	for attempt := 1; ; attempt++ {
		audience, class, audErr := resolver.Audience(context.Background(), sessionID)
		switch {
		case audErr == nil && audience == steer.AudienceUser:
			return false, nil
		case audErr == nil && audience == steer.AudienceSteeringSession:
			return true, nil
		case audErr == nil:
			return false, fmt.Errorf("session %q is classified %s, so its conclusion has no safe audience", sessionID, class)
		}
		err = fmt.Errorf("classify the conclusion audience of session %q: %w", sessionID, audErr)
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
	note := conclusion + "\n\nThis conclusion could not be delivered: the session's records could not be read, or do not establish who should receive it (" + audErr.Error() + ")."
	logger.ErrorCF("agent", "goal: the Judge conclusion could not be delivered — the conclusion audience is unreadable",
		map[string]any{"session_id": work.sessionID, "error": audErr.Error()})
	detail := "The Judge's conclusion could not be delivered: " + audErr.Error()
	if werr := appendGoalConclusionFn(al, store, work.sessionID, goalWorkAgentID(work), note); werr != nil {
		logger.ErrorCF("agent", "goal: the undeliverable Judge conclusion could not be saved either",
			map[string]any{"session_id": work.sessionID, "error": werr.Error()})
		detail += "; saving the undeliverable note also failed: " + werr.Error()
	}
	al.emitJudgeConclusionFailed(work.sessionID, detail)
}

// emitJudgeConclusionFailed raises the judge_conclusion_failed notice for
// sessionID (issue #1306, SFH R2-3). The message is the curated catalogue
// sentence; the raw cause (paths, store errors) goes only to the scrubbed,
// Verbose-only detail. The notice ends no turn.
func (al *AgentLoop) emitJudgeConclusionFailed(sessionID, detail string) {
	al.emitEvent(EventKindError, EventMeta{Source: "goal_loop", SessionKey: sessionID}, ErrorPayload{
		Stage: "goal_conclusion", Code: string(CodeJudgeConclusionFailed), SessionID: sessionID,
		Message: UserMessageForCode(CodeJudgeConclusionFailed), Detail: detail,
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
		al.emitJudgeConclusionFailed(work.sessionID, "The Judge's conclusion could not be saved to this chat: "+saveErr.Error())
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
		if cur.GoalID != work.goalID {
			// The record was addressed by this goal's own ID, so a different
			// ID inside it is damage, not a replacement goal (a replacement
			// has its own record). Report it; never treat it as superseded.
			return fmt.Errorf("goal record %q holds goal id %q: the record is damaged", work.goalID, cur.GoalID)
		}
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
		al.emitJudgeConclusionFailed(sessionID, "The Judge's conclusion could not be delivered to this chat's channel: "+err.Error())
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

// ackMetVerdictEntry acknowledges only after the combined final-id input is
// saved as chat membership (PW-D16), or the final was already consumed by a
// poll. Publication is not durability; placement is a later, safe-fence step.
// The suppressed verdict never owns a second wake id (PW-D17).
func (al *AgentLoop) ackMetVerdictEntry(sessionID, goalID string, round int) error {
	inbox, lifecycle, store := al.GetMessageInboxStore(), al.GetSessionLifecycleStore(), al.GetSessionStore()
	if inbox == nil || lifecycle == nil || store == nil {
		return errors.New("goal: met verdict acknowledgement dependencies are not wired")
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return err
	}
	if rec.SteeredBy == nil {
		return nil
	}
	ownerKey := deliverOwnerKey(rec)
	finalID := fmt.Sprintf("%s:%d:final", sessionID, rec.Generation)
	addr, _, err := store.SavedChatInput(ownerKey, "saved-input-"+finalID)
	if err != nil {
		return err
	}
	if addr.EntryID == "" {
		consumed, readErr := al.wakeInputConsumed(ownerKey, finalID)
		if readErr != nil {
			return readErr
		}
		if !consumed {
			return fmt.Errorf("goal: verdict remains unread: combined input %q has not been saved", finalID)
		}
	}
	return inbox.Ack(ownerKey, []string{goalVerdictUpwardMessageID(goalID, round)})
}

// wakeMetVerdictEntry retries only a saved final-id input. With no combined
// input it deliberately suppresses the standalone verdict; a stopped parent
// remains a refusal. The committed-final pass owns recovery, never a second
// turn under a verdict-round id.
func (al *AgentLoop) wakeMetVerdictEntry(sessionID, goalID string, round int) error {
	inbox, lifecycle, store := al.GetMessageInboxStore(), al.GetSessionLifecycleStore(), al.GetSessionStore()
	if inbox == nil || lifecycle == nil || store == nil {
		return errors.New("goal: met verdict retry dependencies are not wired")
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return err
	}
	if rec.SteeredBy == nil {
		return errors.New("goal: met verdict retry has no steering edge")
	}
	parentID := deliverOwnerKey(rec)
	_, parent, err := al.wakeRecipient(parentID)
	if err != nil {
		return err
	}
	if blocked, blockErr := al.wakeBlocked(parent); blockErr != nil || blocked {
		return errors.Join(blockErr, errors.New("goal: met ending is held by Stop"))
	}
	_, found, err := findUnackedInboxMessage(inbox, parentID, sessionID, goalVerdictUpwardMessageID(goalID, round))
	if err != nil || !found {
		return err
	}
	finalID := fmt.Sprintf("%s:%d:final", sessionID, rec.Generation)
	entries, err := store.ReadTranscript(parentID)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.ID == "saved-input-"+finalID {
			_, err := al.WakeSession(withUpwardWakeSource(context.Background(), sessionID, "handback", nil), parentID, "finished", finalID, entry.Content)
			return err
		}
	}
	// A verdict without the final's saved payload is deliberately suppressed,
	// not an alternate wake identity. Boot's committed-final pass retries it.
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
