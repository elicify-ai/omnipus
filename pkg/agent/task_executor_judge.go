// task_executor_judge.go: Claim, verdict and completion contract for a finished attempt

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// supersedeTaskSession closes out a retry-attempt's own session when the
// goal loop moves on to a fresh attempt (M6, UAT 2026-07-31):
// consumeTaskAttempt's restart mints a BRAND NEW session for the
// next attempt (createTaskSessionSync's sessStore.NewSession, reached again
// when the caller's deferred closure re-enters ExecuteTask) but never closed
// out the PREVIOUS attempt's session — only the FINAL attempt's session was
// ever touched, by completeTaskWithResult's direct SetMeta(StatusArchived) +
// finalizeTaskLifecycle. Every intermediate, superseded attempt kept
// session.StatusActive permanently, misleading anything that counts or
// reconciles active work (the sessions list, usage accounting, orphan
// sweeps).
//
// This is a DIRECT SetMeta call — the same shape completeTaskWithResult uses
// for its own terminal write — rather than relying solely on
// transitionTaskLifecycle's mediator (session.TransitionSession):
// transitionTaskLifecycle no-ops ENTIRELY (including its UnifiedMeta mirror)
// when te.getLifecycleStore() returns nil, which a TaskExecutor can validly
// run without (test harnesses, and any caller that has not wired the S2
// durable lifecycle store) — a fix that only worked when that store happens
// to be configured would silently fail to close out the session in exactly
// the configurations most likely to go unnoticed. The direct SetMeta below
// is called unconditionally (whenever sessStore is available at all); the
// transitionTaskLifecycle call alongside it is best-effort, mirroring the
// durable S2 record too when that store IS wired, exactly like
// completeTaskWithResult's own belt-and-suspenders dual write.
//
// session.StatusInterrupted (via LifecycleCancelled — mirrors to
// StatusInterrupted per lifecycle_bridge.go's canonical mapping) rather than
// StatusArchived: this attempt didn't error out and the TASK itself has not
// been judged failed (nextStatus is `next`, not `failed`) — the session's
// own life simply ended in favor of a new attempt, the same "terminated, not
// cleanly completed" shape a genuine execution error or a user Stop already
// use StatusInterrupted for, rather than the "intentionally closed" shape
// StatusArchived captures for a task that actually reached a terminal
// outcome.
func (te *TaskExecutor) supersedeTaskSession(agentID, taskSessionID string) {
	if taskSessionID == "" {
		return
	}
	if sessStore := te.agentLoop.GetAgentStore(agentID); sessStore != nil {
		statusInterrupted := session.StatusInterrupted
		if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusInterrupted}); setErr != nil {
			logger.WarnCF("task_executor",
				"goal-loop: could not mark superseded attempt's session interrupted",
				map[string]any{"session_id": taskSessionID, "error": setErr.Error()})
		}
	}
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleCancelled, "")
}

// buildSteeringText renders the feedback fed forward into the next attempt.
func buildSteeringText(claimSummary string, verdict *task.JudgeVerdict) string {
	if verdict == nil {
		// No-signal case: the composed "no completion signal" reason IS the
		// steering — there is no per-criterion breakdown to report.
		return claimSummary
	}
	var sb strings.Builder
	sb.WriteString("The judge reviewed your last attempt and found it UNMET:\n")
	for _, c := range verdict.PerCriterion {
		if !c.Met {
			fmt.Fprintf(&sb, "- criterion %s: %s\n", c.CriterionID, c.Reason)
		}
	}
	return sb.String()
}

// wakeOwnerAttemptsExhausted wakes the task's owning agent via the
// async-notifier (FR-044/async_notifier.go) once the goal loop's attempts
// are exhausted. Falls back to a "system"/"task:<id>" destination when the
// task has no SourceChannel/SourceChatID (e.g. a board/REST-created task) —
// AsyncNotifier.Notify rejects an empty destination outright (FR-N7).
func (te *TaskExecutor) wakeOwnerAttemptsExhausted(t *task.Task, taskSessionID, handover string) {
	channel, chatID := t.SourceChannel, t.SourceChatID
	if channel == "" || chatID == "" {
		channel, chatID = "system", "task:"+t.ID
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content := fmt.Sprintf(
		"Task %q (%s) exhausted its goal-loop attempts and needs your attention.\n\n%s",
		t.Title, t.ID, handover,
	)
	if notifyErr := te.agentLoop.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:             channel,
		ChatID:              chatID,
		AgentID:             t.AgentID,
		TranscriptSessionID: taskSessionID,
		SourceKind:          "task_goal_loop",
		Content:             content,
	}); notifyErr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not wake owner on attempts-exhausted",
			map[string]any{"task_id": t.ID, "error": notifyErr.Error()})
	}
}

// writeJudgeVerdictTranscript writes verdict as a dedicated judge_verdict
// transcript entry (FR-056, EntryTypeJudgeVerdict) alongside the worker's own
// ADR-043 completion marker so the two can never silently disagree (ADR §6).
func (te *TaskExecutor) writeJudgeVerdictTranscript(t *task.Task, taskSessionID string, verdict *task.JudgeVerdict) {
	if taskSessionID == "" || verdict == nil {
		return
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		return
	}
	payload, merr := json.Marshal(verdict)
	if merr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not marshal judge verdict for transcript",
			map[string]any{"task_id": t.ID, "error": merr.Error()})
		return
	}
	if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-judge-%d", t.ID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("task_executor", "goal-loop: judge verdict transcript write failed",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
		return
	}
	// Live push, ONLY once the entry above is durably saved (mirrors
	// recordGoalOutcome's ordering, goal_outcome.go): the WS forwarder
	// (websocket.go's EventKindJudgeVerdict case) turns this into a live
	// generated.JudgeVerdictFrame carrying taskSessionID as session_id, so
	// the SPA can anchor the card in this task's run session thread — not
	// just the GLOBAL ActivityPanel.
	te.agentLoop.emitEvent(EventKindJudgeVerdict, EventMeta{Source: "task_executor", AgentID: t.AgentID},
		JudgeVerdictPayload{SessionID: taskSessionID, Verdict: *verdict})
}

// completeTaskWithResult marks task t terminal — Done when success is true,
// Failed otherwise — with the given result text, archives its session (if
// any), and runs the shared post-completion hooks (status-changed event,
// parent follow-up, and — for a Done status only, per onTaskComplete's own
// gate — blocked-dependent advance) plus source-channel notification. It is
// the one terminal writer for a task run's outcome (task_run_loop.go): an
// upheld claim (done); a blocked or waiting claim, a Judge that cannot run or
// a run that was never dispatched (failed, no attempt used); and the handover
// once the task's attempts are spent (failed).
//
// The success parameter is deliberately a plain bool, not a task.Status
// (review C1): completeTaskWithResult only ever writes one of the two
// terminal statuses, so narrowing the signature to "success or not" makes
// writing a non-terminal status here a compile error instead of a
// reviewable-but-possible mistake.
//
// expected is the on-disk status the caller believes t is CURRENTLY at
// (ADR-052 FR-014/§6.4(b) TOCTOU fix, 7-reviewer + architect gate): every
// call site reached this function after some earlier, possibly-unlocked
// work (a judge/verifier turn, an attempt-increment write) during which a
// concurrent Stop could have moved the task out from under it. The write
// below is a compare-and-swap (UpdateIfStatus) against expected rather than
// a plain Update — on a conflict (the task is no longer at expected, most
// commonly because StopTask/StopPlan already moved it to
// failed+stopped_by_user) the completion is DROPPED: logged, no session
// archive, no onTaskComplete/notifySourceChannel side effects, and — via
// the returned bool — no owner-wake either at call sites that gate one on
// it. This is the same "drop the stale outcome, never resurrect or
// silently overwrite a Stop" contract taskVerdictStillApplicable's
// pre-existing fast-path already documents; the CAS makes it authoritative
// (belt-and-suspenders) rather than relying solely on that earlier,
// separately-timed re-check. Returns whether the write actually landed.
func (te *TaskExecutor) completeTaskWithResult(
	t *task.Task, taskSessionID string, expected task.Status, success bool, result string, run *activeRun,
) (applied bool) {
	status := task.StatusDone
	if !success {
		status = task.StatusFailed
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	now := time.Now().UTC().Format(time.RFC3339)
	final, uerr := te.store.UpdateIfStatus(t.ID, expected, task.Patch{
		Status:      &status,
		Result:      &result,
		CompletedAt: &now,
	})
	if uerr != nil {
		if errors.Is(uerr, task.ErrStatusConflict) {
			logger.WarnCF("task_executor",
				"goal-loop: dropping completion outcome — task left its expected status concurrently "+
					"(Stop landed); the task's own outcome is authoritative",
				map[string]any{"task_id": t.ID, "expected_status": string(expected), "target_status": string(status)})
			// M5: close the run here too, with the outcome this function was
			// asked to write — the Task mirror write was dropped (a concurrent
			// Stop is authoritative), but run-history has nowhere else to
			// record this execution's own completion, and there is no reaper
			// backstop to fall back on if it is left in_progress.
			te.closeRun(t.ID, run, status, result)
			return false
		}
		// Known, accepted limitation (ADR-043 §3): the task is left stuck at
		// whatever non-terminal status it had before this call (typically
		// in_progress) — we do not retry and we do not force a second write
		// with a synthesized failure status. A persistent store failure here
		// (disk full, permissions, corrupt file) would very likely fail a
		// retry identically, and forcing a follow-up write risks compounding
		// a partially-written/corrupted task file rather than recovering it.
		// An operator must notice this ERROR log and manually resolve the
		// stuck task (e.g. via a direct store fix or `omnipus` CLI update).
		logger.ErrorCF("task_executor", "Completion update failed",
			map[string]any{"task_id": t.ID, "status": string(status), "error": uerr.Error()})
		// M5: close the run here too — unlike the Task mirror, run-history has
		// nowhere else to record this completion, and there is no reaper
		// backstop to fall back on if we leave it in_progress.
		te.closeRun(t.ID, run, status, result)
		return false
	}
	if taskSessionID != "" && sessStore != nil {
		statusArchived := session.StatusArchived
		if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusArchived}); setErr != nil {
			logger.WarnCF("task_executor", "Meta update failed",
				map[string]any{"task_id": t.ID, "error": setErr.Error()})
		}
	}
	// FR-118/G-13: this is completeTaskWithResult's own terminal write —
	// mirror it onto the durable lifecycle record (see finalizeTaskLifecycle's
	// doc comment). Placed AFTER the CAS write above lands (never on the
	// dropped-conflict early returns), exactly like the UnifiedMeta archive
	// this line sits next to.
	te.finalizeTaskLifecycle(taskSessionID, status)
	// GOAL-FR-015/FR-027/FR-028: end the paired goal record with its task.
	// Placed with finalizeTaskLifecycle — AFTER the CAS write above lands and
	// never on the dropped-conflict early returns, because a dropped write
	// means some OTHER writer owns this task's outcome and will end the goal
	// record itself. `final` is read back from the store, so CancelReason is
	// whatever actually persisted rather than whatever this call was handed.
	terminateTaskGoalRecord(t.ID, final.Status, final.CancelReason, result)
	te.closeRun(t.ID, run, status, result)
	te.recordEvidenceBoundary(final)
	te.onTaskComplete(final)
	te.notifySourceChannel(final)
	return true
}

// recordEvidenceBoundary takes the write-set-scoped boundary commit for a task
// that has just reached a terminal state (D13/G-12, E.4). This is the PRODUCER
// half of Play-from-commit: without it LastMemberCommit resolves "" forever and
// Play silently degrades to a fresh attempt.
//
// Deliberately best-effort and non-fatal — the task has ALREADY been written
// terminal by the caller, so a broken evidence repo must not retroactively fail
// it. Every outcome is logged so an operator can tell "no evidence recorded"
// from "evidence recorded", which is exactly the signal whose absence made the
// unwired state invisible.
func (te *TaskExecutor) recordEvidenceBoundary(t *task.Task) {
	evidence := te.getEvidenceCommitter()
	if evidence == nil || t == nil {
		return
	}
	res, recorded, err := evidence.CommitTaskBoundary(t)
	switch {
	case err != nil:
		logger.WarnCF("task_executor", "evidence boundary commit failed — Play will fall back to a fresh attempt",
			map[string]any{"task_id": t.ID, "workspace_id": t.WorkspaceID, "error": err.Error()})
	case !recorded:
		// Nothing to record (no workspace, no write set, unmaterialized work
		// dir). Normal for every non-plan-member task — stay quiet at debug.
		logger.DebugCF("task_executor", "evidence boundary: nothing to record",
			map[string]any{"task_id": t.ID})
	case res.Skipped:
		logger.InfoCF("task_executor", "evidence boundary skipped",
			map[string]any{"task_id": t.ID, "reason": res.SkipReason})
	default:
		logger.InfoCF("task_executor", "evidence boundary commit recorded",
			map[string]any{
				"task_id": t.ID, "commit": res.Hash, "files": len(res.Committed),
				"contention": len(res.Contention), "excluded_for_secret": len(res.ExcludedForSecret),
			})
	}
}

// SetEvidenceCommitter installs the boundary-commit producer (D13/G-12). Wired
// at the gateway boot seam next to PlanEngine.SetCommitResolver; leaving it
// unset disables evidence recording without affecting task execution.
//
// Guarded by mu (fix-wave finding #3): the gateway starts dispatch (via
// newTaskExecutor) before this late-binding boot-seam call lands, so a
// concurrent recordEvidenceBoundary read on another goroutine must never race
// this write — see the evidence field's own doc comment.
func (te *TaskExecutor) SetEvidenceCommitter(c evidenceCommitter) {
	te.mu.Lock()
	te.evidence = c
	te.mu.Unlock()
}

// getEvidenceCommitter returns the installed evidence committer (nil if
// unset), guarded by mu so a concurrent SetEvidenceCommitter never races a
// goroutine reading it mid-dispatch. Mirrors getLifecycleStore exactly.
func (te *TaskExecutor) getEvidenceCommitter() evidenceCommitter {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.evidence
}

// notifySourceChannel sends a compact task result back to the originating
// channel. Only sends for terminal statuses.
func (te *TaskExecutor) notifySourceChannel(t *task.Task) {
	if t.SourceChannel == "" || t.SourceChatID == "" {
		return
	}
	if te.agentLoop.bus == nil {
		logger.WarnCF("task_executor", "Cannot notify source channel — message bus is nil",
			map[string]any{"task_id": t.ID, "channel": t.SourceChannel})
		return
	}
	if !task.IsTerminal(t.Status) {
		return
	}

	msg := fmt.Sprintf("**%s** — %s", t.Title, t.Status)
	if t.Result != "" {
		result := t.Result
		if len(result) > 500 {
			result = result[:497] + "..."
		}
		msg += "\n\n" + result
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := te.agentLoop.bus.PublishOutbound(notifyCtx, bus.OutboundMessage{
		Channel: t.SourceChannel,
		ChatID:  t.SourceChatID,
		Content: msg,
	}); err != nil {
		logger.WarnCF("task_executor", "Could not notify source channel",
			map[string]any{"task_id": t.ID, "channel": t.SourceChannel, "error": err.Error()})
	}
}

// onTaskComplete handles post-completion logic: parent notification + the
// blocked_by auto-advance (dispatch tasks whose deps are now all done).
func (te *TaskExecutor) onTaskComplete(t *task.Task) {
	te.emitStatusChanged(t, t.Status)

	if t.ParentTaskID != "" {
		te.notifyParentIfAllSiblingsDone(t.ParentTaskID)
	}

	// Only a `done` task unblocks downstream tasks (a `failed` dep does not).
	if t.Status != task.StatusDone {
		return
	}
	// Move dependents blocked→next, then attempt to dispatch the ready ones.
	if _, err := te.store.AdvanceBlockedDependents(t.ID); err != nil {
		logger.WarnCF("task_executor", "Could not advance blocked dependents",
			map[string]any{"completed_task_id": t.ID, "error": err.Error()})
	}
	te.advanceBlockedTasks(context.Background(), t.ID)
}

// notifyParentIfAllSiblingsDone resumes the parent agent once every child task
// of parentID has reached a terminal state. Safe under concurrent sibling
// completions via the atomic FollowedUp claim.
func (te *TaskExecutor) notifyParentIfAllSiblingsDone(parentID string) {
	siblings, err := te.store.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
	if err != nil {
		logger.WarnCF("task_executor", "Could not list siblings",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	for _, s := range siblings {
		if !task.IsTerminal(s.Status) {
			return
		}
	}

	parent, err := te.store.Get(parentID)
	if err != nil {
		logger.WarnCF("task_executor", "Could not load parent task",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	if parent.Status != task.StatusInProgress {
		return
	}

	claimed, claimErr := te.store.ClaimParentFollowUp(parent.ID)
	if claimErr != nil {
		logger.WarnCF("task_executor", "Could not claim parent follow-up",
			map[string]any{"parent_id": parent.ID, "error": claimErr.Error()})
		return
	}
	if !claimed {
		return
	}

	if te.parentFollowUp != nil {
		te.parentFollowUp(parent.ID)
		return
	}

	summary := te.buildChildSummary(siblings)
	sessionKey := fmt.Sprintf("agent:%s:task:%s", parent.AgentID, parent.ID)
	followUp := fmt.Sprintf("All child tasks of task %q have completed.\n\n%s", parent.ID, summary)
	parentChatID := "task:" + parent.ID
	// Fix-wave finding #1: this goroutine calls processTaskDirect — a real
	// agent turn that reads/writes session and transcript stores — exactly
	// like runTask/runTaskFromInProgress, but until now it was launched with
	// NO wg tracking at all, so Drain's wg.Wait could never see it and it
	// could still be writing through stores Close() had already torn down.
	// Add(1) before `go` (mirroring the other two dispatch sites); Done() is
	// the FIRST defer registered inside the goroutine so it fires LAST (after
	// the panic-recovery defer below, which is registered second and thus
	// runs first) — Drain only ever sees this goroutine as "done" once it has
	// genuinely finished, panic or not.
	//
	// Routed through enterDispatch, NOT a bare Add: unlike runTask and
	// runTaskFromInProgress, this launch site does NOT run while its caller
	// already holds a wg entry. It is reached from the task_update tool via
	// AgentLoop's SetOnComplete hook — an ordinary agent turn that holds no
	// count of its own. A bare Add here can therefore take the counter 0 -> 1
	// while Drain has a waiter parked, which is the sync.WaitGroup panic this
	// gate exists to prevent (see dispatchGate's doc comment). Refusing while
	// draining is also the correct BEHAVIOUR, not merely the safe one: Close()
	// is already tearing down the session and transcript stores this follow-up
	// turn would write through.
	if !te.enterDispatch() {
		return
	}
	go func() {
		defer te.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("task_executor", "Panic in parent follow-up",
					map[string]any{"parent_id": parent.ID, "panic": r})
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, ferr := te.agentLoop.processTaskDirect(ctx, parent.AgentID, followUp, sessionKey, parentChatID)
		if ferr != nil {
			logger.WarnCF("task_executor", "Parent follow-up failed",
				map[string]any{"parent_id": parent.ID, "error": ferr.Error()})
		}
	}()
}

// readyBlockedCandidates returns the IDs of all `next` tasks that list
// completedTaskID as a blocker AND whose ENTIRE blocked_by set is now `done`.
func (te *TaskExecutor) readyBlockedCandidates(completedTaskID string) []string {
	// Scan BOTH `blocked` and `next` — either is a legitimate resting state for
	// a dependent, and this used to scan only `next`.
	//
	//   blocked → a dependency was unmet, so the S2 UAT fix (pkg/task/store.go
	//             derives `blocked` at Create and at the end of every
	//             updateLocked) persisted it as blocked. This is the common
	//             case here and scanning only `next` MISSED IT ENTIRELY,
	//             silently killing this advance path for exactly the tasks it
	//             exists to advance. CI caught it via
	//             TestOrchestratorAdvance_StillBlockedWhenDepNotComplete.
	//   next    → every dependency was already `done` when the task was
	//             created, so the recompute never blocked it. Still a valid
	//             candidate, and dropping it would break
	//             TestOrchestratorAdvance_UnblockedTaskFoundAfterDep.
	//
	// The allSatisfied loop below re-verifies the FULL dependency set either
	// way, so this filter is only a pre-narrowing — it must not be the thing
	// that decides readiness.
	var candidates []task.Task
	for _, st := range []task.Status{task.StatusBlocked, task.StatusNext} {
		batch, listErr := te.store.List(task.Filter{
			Status:      st,
			BlockedByID: completedTaskID,
		})
		if listErr != nil {
			logger.WarnCF("task_executor", "Orchestrator: could not scan blocked tasks",
				map[string]any{"completed_task_id": completedTaskID, "status": string(st), "error": listErr.Error()})
			return nil
		}
		candidates = append(candidates, batch...)
	}
	var ready []string
	for i := range candidates {
		t := &candidates[i]
		allSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			ready = append(ready, t.ID)
		}
	}
	return ready
}

// advanceBlockedTasks dispatches every `next` task whose full dependency set is
// now satisfied by the completion of completedTaskID.
//
// ExecuteTask's own requirePlanExecuting gate (see its doc) is what prevents
// this from re-opening the Stop leak the S1 fix was written to close: an
// in-flight plan member that finishes (landing here via onTaskComplete) after
// its plan was already Stopped/failed(stopped_by_user) must not have this
// function dispatch its now-unblocked dependents just because they satisfy
// their BlockedBy set — ExecuteTask itself now refuses that dispatch.
func (te *TaskExecutor) advanceBlockedTasks(ctx context.Context, completedTaskID string) {
	for _, taskID := range te.readyBlockedCandidates(completedTaskID) {
		if err := te.ExecuteTask(ctx, taskID, nil); err != nil {
			if !isRoutineAutoDispatchRefusal(err) {
				logger.WarnCF("task_executor", "Orchestrator: advance dispatch failed",
					map[string]any{"task_id": taskID, "error": err.Error()})
			}
		} else {
			logger.InfoCF("task_executor", "Orchestrator: advanced blocked task",
				map[string]any{"task_id": taskID, "unblocked_by": completedTaskID})
		}
	}
}

// buildChildSummary produces a markdown summary of all child task results.
func (te *TaskExecutor) buildChildSummary(children []task.Task) string {
	var sb strings.Builder
	sb.WriteString("## Child Task Results\n\n")
	for _, c := range children {
		fmt.Fprintf(&sb, "- **%s** (status: %s)", c.Title, c.Status)
		if c.Result != "" {
			fmt.Fprintf(&sb, ": %s", c.Result)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// failTask marks a task as failed with the given reason.
func (te *TaskExecutor) failTask(taskID, reason string) {
	now := time.Now().UTC().Format(time.RFC3339)
	failed := task.StatusFailed
	updated, err := te.store.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &reason,
		CompletedAt: &now,
	})
	if err != nil {
		logger.ErrorCF("task_executor", "Could not mark task failed",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return
	}
	// GOAL-FR-015: terminal disposition — end the paired goal record too. See
	// terminateTaskGoalRecord's doc comment for why all three terminal writers
	// must call it (this one has no chokepoint in common with the other two).
	terminateTaskGoalRecord(taskID, updated.Status, updated.CancelReason, reason)
	te.emitStatusChanged(updated, task.StatusFailed)
}
