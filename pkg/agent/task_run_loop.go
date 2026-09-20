// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_loop.go implements the two-level model every task run executes
// under (founder decision 2026-09-14; issue #710 tracks whether the two
// limits should ever be merged — until then they stay separate):
//
//	INNER — goal tries. Within ONE run, in ONE session, the worker works the
//	task's goal the way a chat goal is worked: it calls goal_claim(met) when
//	it believes the work is done; a Judge ruling of "not met" does NOT restart
//	the run — the worker is steered, in the same session, with the Judge's
//	feedback and keeps working. A turn that ends without a judgeable claim
//	(no claim, a bare marker, malformed tool output, a reasoning-only reply)
//	also spends a try and the worker is re-prompted in the same session.
//	Bounded by the goal record's max_rounds (Settings -> Tries per goal,
//	snapshotted when the run activated the goal); the counter is the goal
//	record's Round.
//
//	OUTER — task attempts. The RUN fails as a whole when (1) its goal ends
//	not met after all its tries, (2) the run breaks (an execution error), or
//	(3) two consecutive tries produced nothing but reasoning, cut off at the
//	output-token limit. A failed run consumes one task attempt
//	(Task.AttemptCount, written only by consumeTaskAttempt) and the task
//	restarts AUTOMATICALLY in a fresh run — a new session, the goal ended and
//	then reactivated against that session with a fresh try budget — up to the
//	task attempt limit (the task's own max_attempts, else
//	planning.task_max_attempts, default 3). Past that the task ends Failed
//	with the reason. The 2x hard ceiling guards this outer counter.
//
//	AttemptCount counts only runs that genuinely started and failed. A run
//	that never ran the work — a dispatch refusal wrapped in
//	ErrTaskRunNotDispatched — ends the task without touching it.
//
//	A run whose turn is refused for a reason only an operator can fix
//	(classifyOperatorOnlyTurnError: rejected credentials, an unknown provider,
//	no model, an unknown context window, no workspace, an unusable working
//	folder) is not a failed attempt either (founder decision 2026-09-15): a
//	fresh run is refused the same way until a setting changes, so the task
//	ends Failed at once with the reason and the fix — no attempt, no restart.
//	Temporary errors (a rate limit, a network or provider outage, a stalled
//	stream, a timeout) still break the run and restart it.
//
//	A stopped turn (a Stop, /cancel, or shutdown cancelling the run) is not a
//	failed run either: the task ends Failed "Stopped: <why>" with no attempt
//	used and no restart.
//
//	A run whose assigned agent cannot finish the task as configured never
//	starts (founder decision 2026-09-15, task_assignee_readiness.go): a native
//	worker denied goal_claim, or a machine check its bash policy cannot run.
//	Before the first turn the task ends Failed with the fix — no attempt, no
//	restart, no model call.
//
//	goal_claim(blocked) is not a failed run: the task ends Failed with
//	"Blocked: <why>" — no attempt consumed, no Judge call, no restart.
//	goal_claim(waiting_on_user) has no operator reply channel on a task run,
//	so it ends the same way with "Needs the operator: <what>".
//
// ONE claim mechanism, ONE Judge consumer per goal: a native worker claims
// only through the goal_claim tool, and a task-owned goal is adjudicated only
// here — checkGoalLoopAfterTurn ignores task runs and task-owned goals, and
// the idle keeper stands down while the executor holds a run. The prose
// completion marker survives only for subagent_3p workers (their CLI cannot
// call Omnipus tools); a marker claim becomes the same claim shape —
// success-with-evidence is a met claim, failure is a blocked claim.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ErrTaskRunNotDispatched marks a task run that never ran the work: the
// dispatch itself was refused before any worker turn started (the agent's
// hooks or MCP servers could not initialise, the agent is gone, its executor
// could not be resolved). Wrap it (fmt.Errorf("...: %w", ErrTaskRunNotDispatched))
// from any dispatch step that refuses before work begins. Such a run is NOT a
// failed attempt: the task ends without its AttemptCount moving.
var ErrTaskRunNotDispatched = errors.New("the task run was not dispatched")

// runStep is one turn's outcome inside a task run.
type runStep int

const (
	// runStepContinue keeps the run going: the next turn goes to the SAME
	// session with nextPrompt.
	runStepContinue runStep = iota
	// runStepEnded ends the run without a restart: the task reached a
	// terminal status (done, a no-attempt Failed, or another writer's Stop),
	// or it is paused on a store fault with its run left open (EC-6).
	runStepEnded
	// runStepFailedRun ends the run as failed — consumeTaskAttempt already
	// decided between an automatic restart and the terminal Failed write.
	runStepFailedRun
)

// taskRunState is the in-memory bookkeeping for one run. A fresh run starts
// clean.
type taskRunState struct {
	// claimWatermark: goal_claim calls recorded at or before this instant
	// have been resolved already and are never re-adjudicated.
	claimWatermark time.Time
	// innerTries mirrors the tries this run spent. The goal record's Round is
	// the authoritative counter; this bounds the run even if a write of that
	// counter fails, so a store fault can never loop a run forever.
	innerTries int
	// reasoningOnlyStreak counts CONSECUTIVE tries that produced no answer
	// because the output-token budget went to reasoning. Two in a row fail
	// the run.
	reasoningOnlyStreak int
	// turns counts worker turns dispatched in this run.
	turns int
}

// judgeUnavailableRetryBound bounds how many times one claim is re-sent to a
// Judge that keeps answering "unavailable" for a transient reason, each try
// under goalJudgeRoundTimeout — the same consecutive-unavailable bound the
// plan judge uses (plan.MaxConsecutiveJudgeUnavailable).
const judgeUnavailableRetryBound = plan.MaxConsecutiveJudgeUnavailable

// executeTaskRun drives one task run to its resolution. turn executes ONE
// worker turn (processTaskDirect in production). Returns the task id to
// re-dispatch when the run failed and an attempt remains, else "".
func (te *TaskExecutor) executeTaskRun(
	ctx context.Context,
	t *task.Task,
	taskSessionID, logSuffix string,
	run *activeRun,
	turn func(prompt string) (string, error),
) (redispatchTaskID string) {
	if reason := te.preRunCannotFinishReason(t, taskSessionID); reason != "" {
		te.endTaskAssigneeCannotFinish(t, taskSessionID, reason, run)
		return ""
	}
	state := &taskRunState{claimWatermark: time.Now().UTC()}
	firstPrompt := te.buildPrompt(t)
	prompt := firstPrompt
	externalCLI := te.dispatchesExternalCLI(t.AgentID)
	for {
		state.turns++
		resp, err := turn(prompt)
		step, next, redispatch := te.finishRunTurn(ctx, t, taskSessionID, resp, err, logSuffix, run, state)
		switch step {
		case runStepContinue:
			if externalCLI {
				// An external CLI keeps no conversation between invocations:
				// each turn must carry the whole task again, with the feedback.
				prompt = firstPrompt + "\n\n## Feedback from your previous try in this run:\n" + next
			} else {
				prompt = next
			}
		case runStepFailedRun:
			return redispatch
		default:
			return ""
		}
	}
}

// runClaim is what a worker's turn claimed.
type runClaim struct {
	status   string // tools.GoalClaimStatus*, claimBareMarker, or "" for none
	evidence string // the worker's one-line statement, or the reason
}

// claimBareMarker is an external-CLI success marker with no evidence line: not
// yet a claim the Judge may be asked about.
const claimBareMarker = "bare_marker"

// resolveRunClaim finds what the worker's turn claimed. A native worker claims
// ONLY through goal_claim (ADR-084 D12; the last successful call since the
// watermark wins, JUDGE-FR-092/E-27). A subagent_3p worker's TASK_STATUS
// marker feeds the same claim shape (ADR-043, narrowed to external-CLI
// workers): success with a [goal:evidence] line is a met claim, failure is a
// blocked claim.
func (te *TaskExecutor) resolveRunClaim(t *task.Task, taskSessionID, resp string, state *taskRunState) (runClaim, error) {
	if !te.dispatchesExternalCLI(t.AgentID) {
		store := te.agentLoop.GetAgentStore(t.AgentID)
		if store == nil || taskSessionID == "" {
			return runClaim{}, nil
		}
		found, status, evidence, _, readErr := te.agentLoop.resolveToolClaim(store, taskSessionID, state.claimWatermark)
		if readErr != nil {
			return runClaim{}, readErr
		}
		if !found {
			return runClaim{}, nil
		}
		return runClaim{status: status, evidence: evidence}, nil
	}
	signal := parseTaskCompletionSignal(resp)
	if !signal.Found() {
		return runClaim{}, nil
	}
	if signal.Status() == task.StatusFailed {
		return runClaim{status: tools.GoalClaimStatusBlocked, evidence: signal.Result}, nil
	}
	if gate := checkEvidenceMarkerGate(resp); gate.Applicable && !gate.Honored {
		return runClaim{status: claimBareMarker, evidence: gate.SteeringText}, nil
	}
	return runClaim{status: tools.GoalClaimStatusMet, evidence: markerEvidenceText(resp)}, nil
}

// markerEvidenceText returns the [goal:evidence] text immediately before the
// last genuine TASK_STATUS line, using the marker family's shared primitives.
func markerEvidenceText(resp string) string {
	lines := strings.Split(resp, "\n")
	fenced := computeFencedLines(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if fenced[i] {
			continue
		}
		trimmedR := strings.TrimRight(lines[i], "\r")
		if isExcludedMarkerLine(trimmedR) || !taskStatusLineRe.MatchString(trimmedR) {
			continue
		}
		txt, _ := findEvidenceImmediatelyBefore(lines, fenced, i)
		return txt
	}
	return ""
}

// finishRunTurn resolves ONE completed worker turn: it decides the run's next
// step, adjudicates a met claim, spends inner tries, and returns the prompt
// for the next turn when the run goes on.
func (te *TaskExecutor) finishRunTurn(
	ctx context.Context,
	t *task.Task,
	taskSessionID, resp string, turnErr error,
	logSuffix string,
	run *activeRun,
	state *taskRunState,
) (step runStep, nextPrompt, redispatchTaskID string) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)

	if turnErr != nil {
		// The one place a turn error's raw text is kept: the operator's log,
		// where registered credentials are scrubbed (logger's
		// sensitiveValueReplacer). Everything a person or model reads below
		// is built from its plain message instead.
		logger.ErrorCF("task_executor", "Agent execution failed"+logSuffix,
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "error": turnErr.Error(),
				"code": string(TranslateTurnError(turnErr).Code)})

		if errors.Is(turnErr, ErrTaskRunNotDispatched) {
			te.appendRunErrorTranscript(t, taskSessionID, sessStore, fmt.Sprintf("Task execution failed: %v", turnErr))
			te.transitionTaskLifecycle(taskSessionID, session.LifecycleFailed, "not_dispatched")
			te.endTaskWithoutAttempt(t, taskSessionID, fmt.Sprintf("The task could not be started: %v", turnErr), run)
			return runStepEnded, "", ""
		}

		// A stopped turn ends the task: no attempt, no restart. Both classifiers
		// below inherit TranslateTurnError's precedence so that a Stop racing a
		// refusal or a malformed tool call "still ends the way a Stop does" —
		// this is where it ends. Read as a broken run instead, a stop restarted
		// the task in a fresh run: a Stop or /cancel on a task's session started
		// it over, and StopTask (which cancels the session BEFORE it writes the
		// task Failed) could lose that race to consumeTaskAttempt the same way.
		// completeTaskWithResult writes only over in_progress, so a concurrent
		// Stop's own outcome still stands; RequestCancel has already marked the
		// session's lifecycle cancelled. A timeout is not a stop: it still breaks
		// the run and restarts it.
		if TranslateTurnError(turnErr).Code == CodeTurnCanceled {
			reason := "Stopped: " + turnErrorUserText(turnErr)
			logger.InfoCF("task_executor", "task run: the worker's turn was stopped — ending the task, no attempt used, no restart",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
			te.endTaskWithoutAttempt(t, taskSessionID, reason, run)
			return runStepEnded, "", ""
		}

		// Founder decision 2026-09-15: a refusal only an operator can fix fails
		// every fresh run the same way, so it ends the task at once — no attempt,
		// no restart. The reason is built from the classification and the
		// agent's configuration only; the raw error (which can carry a provider
		// response body) never reaches the task, its transcript or its goal.
		if code, cause := classifyOperatorOnlyTurnError(turnErr); cause != operatorFixNone {
			reason := te.taskOperatorFixReason(t.AgentID, turnErr, cause)
			logger.ErrorCF("task_executor", "task run: refused for a reason only an operator can fix — failing the task, no attempt used",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "code": string(code)})
			te.appendRunErrorTranscript(t, taskSessionID, sessStore, reason)
			te.transitionTaskLifecycle(taskSessionID, session.LifecycleFailed, "operator_action_required")
			te.endTaskWithoutAttempt(t, taskSessionID, reason, run)
			return runStepEnded, "", ""
		}

		// Malformed tool-call output is the model's own output going wrong;
		// another turn in the same session routinely clears it, so it spends
		// a try rather than failing the run.
		if code, ok := attemptRecoverableTurnErrorCode(turnErr); ok && te.taskVerdictStillApplicable(t.ID) {
			steer := malformedToolOutputSteering(code)
			if exhausted, reason := te.spendInnerTry(t, taskSessionID, "a try that ended on malformed tool-call output", state); exhausted {
				return te.failedRunStep(ctx, t, taskSessionID, reason, run)
			}
			te.appendRunSystemTranscript(t, taskSessionID, sessStore, steer)
			return runStepContinue, steer, ""
		}

		// A temporary error breaks the run and restarts it. Everything built from
		// it below — the run's transcript, and the reason the run failed, which
		// becomes the task result, the restarted run's first prompt, the goal
		// record's reason, the goal outcome line and the owner's wake — states
		// the error in the contract's plain words for its typed code, never in
		// the error's own text: a provider error's text carries the provider's
		// raw response body, which can echo a credential or the request. The
		// raw error stays only in the ERROR log line above, where registered
		// credentials are scrubbed.
		plain := turnErrorUserText(turnErr)
		te.appendRunErrorTranscript(t, taskSessionID, sessStore, "Task execution failed: "+plain)
		te.transitionTaskLifecycle(taskSessionID, session.LifecycleFailed, "execution_error")
		return te.failedRunStep(ctx, t, taskSessionID, "execution error: "+plain, run)
	}

	if taskSessionID != "" && resp != "" && sessStore != nil {
		if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
			ID:        fmt.Sprintf("%s-response-%d", t.ID, state.turns),
			Role:      "assistant",
			Content:   resp,
			Timestamp: time.Now().UTC(),
		}); appendErr != nil {
			taskGoalTranscriptWriteFailures.Add(1)
			logger.WarnCF("task_executor", "Transcript write failed",
				map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
		}
	}

	current, lerr := te.store.Get(t.ID)
	if lerr != nil {
		logger.WarnCF("task_executor", "Could not re-read task after execution",
			map[string]any{"task_id": t.ID, "error": lerr.Error()})
		te.closeRun(t.ID, run, task.StatusFailed,
			fmt.Sprintf("execution finished but the task record could not be re-read to resolve its outcome: %v", lerr))
		return runStepEnded, "", ""
	}

	// Another writer (a Stop) already ended the task; its outcome stands.
	if task.IsTerminal(current.Status) {
		if taskSessionID != "" && sessStore != nil {
			archived := session.StatusArchived
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &archived}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		te.finalizeTaskLifecycle(taskSessionID, current.Status)
		te.closeRun(t.ID, run, current.Status, current.Result)
		te.notifySourceChannel(current)
		return runStepEnded, "", ""
	}

	// A set_todos checklist card never enters the goal loop (FR-048).
	if current.Scratchpad {
		signal := parseTaskCompletionSignal(resp)
		if !signal.Found() {
			reason := "agent finished without a completion signal — review the run transcript; raw output follows:\n\n" +
				outputOrPlaceholder(resp)
			te.completeTaskWithResult(current, taskSessionID, task.StatusInProgress, false, reason, run)
			return runStepEnded, "", ""
		}
		te.completeTaskWithResult(current, taskSessionID, task.StatusInProgress, signal.Status() == task.StatusDone, signal.Result, run)
		return runStepEnded, "", ""
	}

	claim, claimErr := te.resolveRunClaim(current, taskSessionID, resp, state)
	if claimErr != nil {
		// A transcript read fault must neither spend a try nor end the task:
		// it pauses with the run open and the reason visible (EC-6 shape).
		logger.ErrorCF("task_executor", "goal: could not read the worker's claim from the run transcript",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": claimErr.Error()})
		te.writeTaskReason(current, "The worker's claim could not be read from the run transcript: "+claimErr.Error())
		return runStepEnded, "", ""
	}
	state.claimWatermark = time.Now().UTC()
	if claim.status != "" {
		state.reasoningOnlyStreak = 0
	}

	switch claim.status {
	case tools.GoalClaimStatusBlocked:
		te.recordRunClaim(current, taskSessionID, generated.GoalLatestClaimStatusBlocked, claim.evidence)
		reason := "Blocked" + reasonSuffix(claim.evidence, "the worker reported it cannot proceed")
		te.endTaskWithoutAttempt(current, taskSessionID, reason, run)
		return runStepEnded, "", ""

	case tools.GoalClaimStatusWaitingOnUser:
		te.recordRunClaim(current, taskSessionID, generated.GoalLatestClaimStatusWaitingOnUser, claim.evidence)
		reason := "Needs the operator" + reasonSuffix(claim.evidence, "the worker is waiting on an answer")
		te.endTaskWithoutAttempt(current, taskSessionID, reason, run)
		return runStepEnded, "", ""

	case tools.GoalClaimStatusMet:
		return te.adjudicateRunClaim(ctx, current, taskSessionID, claim.evidence, run, state)

	case claimBareMarker:
		if exhausted, reason := te.spendInnerTry(current, taskSessionID, "a completion marker with no evidence line", state); exhausted {
			return te.failedRunStep(ctx, current, taskSessionID, reason, run)
		}
		te.appendRunSystemTranscript(current, taskSessionID, sessStore, claim.evidence)
		return runStepContinue, claim.evidence, ""
	}

	// No claim. A reasoning-only try is counted on its own streak.
	if turnWasReasoningOnly(taskSessionID, sessStore, resp) {
		state.reasoningOnlyStreak++
		logger.WarnCF("task_executor",
			"task run: a try produced no answer — the output-token limit went to reasoning",
			map[string]any{"task_id": t.ID, "consecutive": state.reasoningOnlyStreak})
		if state.reasoningOnlyStreak >= 2 {
			return te.failedRunStep(ctx, current, taskSessionID, reasoningOnlyFailureReason, run)
		}
		if exhausted, reason := te.spendInnerTry(current, taskSessionID, "a try that produced no answer", state); exhausted {
			return te.failedRunStep(ctx, current, taskSessionID, reason, run)
		}
		te.appendRunSystemTranscript(current, taskSessionID, sessStore, reasoningOnlySteering)
		return runStepContinue, reasoningOnlySteering, ""
	}
	state.reasoningOnlyStreak = 0

	steer := noClaimSteeringPrompt(te.dispatchesExternalCLI(current.AgentID))
	if exhausted, reason := te.spendInnerTry(current, taskSessionID, "a turn that ended without a completion claim", state); exhausted {
		return te.failedRunStep(ctx, current, taskSessionID, reason, run)
	}
	te.appendRunSystemTranscript(current, taskSessionID, sessStore, steer)
	return runStepContinue, steer, ""
}

// reasoningOnlyFailureReason is the plain reason a run fails with after two
// reasoning-only tries in a row (founder rule).
const reasoningOnlyFailureReason = "The model kept reasoning without producing an answer; the task may be too hard for this model."

// reasoningOnlySteering is sent after ONE reasoning-only try.
const reasoningOnlySteering = "Your last turn ended at the output-token limit with no answer at all: the whole budget went to " +
	"reasoning. Produce the answer itself this turn — do the work, keep each tool call small — and claim with " +
	"goal_claim (status \"met\") once it is verified."

// adjudicateRunClaim is the ONE Judge dispatch for a task goal's met claim.
// Met: the task is done. Not met: the goal spent a try (RecordVerdict advances
// its Round) and the worker is steered in the same session; a spent try
// budget fails the run. A Judge only an operator can fix, or one still
// unavailable after judgeUnavailableRetryBound tries, ends the task Failed
// with a plain reason and no attempt consumed — restarting cannot help.
func (te *TaskExecutor) adjudicateRunClaim(
	ctx context.Context,
	t *task.Task,
	taskSessionID, evidence string,
	run *activeRun,
	state *taskRunState,
) (runStep, string, string) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if strings.TrimSpace(evidence) == "" {
		steer := "A completion claim needs your own one-line statement of what you verified. Verify the work, then claim again with that line."
		if exhausted, reason := te.spendInnerTry(t, taskSessionID, "a completion claim with no evidence", state); exhausted {
			return te.failedRunStep(ctx, t, taskSessionID, reason, run)
		}
		te.appendRunSystemTranscript(t, taskSessionID, sessStore, steer)
		return runStepContinue, steer, ""
	}

	// The claim is judged against the criteria on the task's goal record — the
	// same record a chat goal is judged from and the one the task card reads
	// (founder decision 2026-09-14, issue #710: one claim mechanism, one Judge
	// pipeline). The task's own copy and the soft tier are fallbacks only for a
	// record that carries none.
	rec := activeGoalForSession(taskSessionID)
	var criteria []task.AcceptanceCriterion
	if rec != nil {
		criteria = rec.Criteria
	}
	if len(criteria) == 0 {
		criteria = t.Criteria
	}
	if len(criteria) == 0 {
		if soft := SoftTierCriterion(t.Title, t.Description, t.Prompt); soft != nil {
			criteria = []task.AcceptanceCriterion{*soft}
		}
	}
	if len(criteria) == 0 {
		return te.failedRunStep(ctx, t, taskSessionID,
			"the task has no acceptance criteria and no title, description or prompt to judge against", run)
	}
	dod, dodErr := taskGoalDoD(t.ID)
	if dodErr != nil {
		// Fails closed (review finding C2): the claim cannot be judged against
		// the gate the operator authored. The run pauses with its reason
		// visible; no try and no attempt are spent on a store fault.
		logger.ErrorCF("task_executor", "goal: cannot adjudicate — the task's Definition of Done could not be read",
			map[string]any{"task_id": t.ID, "reason": "dod_unreadable", "error": dodErr.Error()})
		te.writeTaskReason(t, "The claim could not be judged: the task's Definition of Done could not be read ("+dodErr.Error()+").")
		return runStepEnded, "", ""
	}
	judged := criteria
	if len(dod) > 0 {
		judged = make([]task.AcceptanceCriterion, 0, len(criteria)+len(dod))
		judged = append(judged, criteria...)
		judged = append(judged, dod...)
	}

	if needsJudgeAgent(judged) {
		if _, ok := te.agentLoop.GetRegistry().GetAgent(string(coreagent.IDJudge)); !ok {
			te.endTaskWithoutAttempt(t, taskSessionID,
				"The Judge could not run: the Judge agent is not registered, so this claim cannot be checked.", run)
			return runStepEnded, "", ""
		}
	}

	tryNo := state.innerTries + 1
	if rec != nil && rec.Round+1 > tryNo {
		tryNo = rec.Round + 1
	}
	te.recordRunClaim(t, taskSessionID, generated.GoalLatestClaimStatusMet, evidence)

	var result JudgeCriteriaResult
	for judgeTry := 1; ; judgeTry++ {
		judgeCtx, cancel := context.WithTimeout(ctx, goalJudgeRoundTimeout)
		result = te.agentLoop.JudgeCriteria(judgeCtx, JudgeCriteriaInput{
			Scope:           task.VerdictScopeTask,
			TaskID:          t.ID,
			AssigneeAgentID: t.AgentID,
			Criteria:        judged,
			Attempt:         tryNo,
			ClaimText:       evidence,
			WorkspaceID:     t.WorkspaceID,
		})
		cancel()
		if !result.Unavailable {
			break
		}
		if !te.taskVerdictStillApplicable(t.ID) {
			return runStepEnded, "", "" // a Stop ended the task while the Judge was out
		}
		if judgeUnavailableNeedsOperator(result.Reason) {
			logger.ErrorCF("task_executor", "goal: the Judge cannot run until an operator fixes it — failing the task, no attempt used",
				map[string]any{"task_id": t.ID, "reason": result.Reason})
			te.endTaskWithoutAttempt(t, taskSessionID, "The Judge could not run: "+judgeOperatorFix(result.Reason), run)
			return runStepEnded, "", ""
		}
		logger.WarnCF("task_executor", "goal: the Judge is unavailable — retrying the same claim, no try used",
			map[string]any{"task_id": t.ID, "judge_try": judgeTry, "reason": result.Reason})
		if judgeTry >= judgeUnavailableRetryBound {
			te.endTaskWithoutAttempt(t, taskSessionID, fmt.Sprintf(
				"The Judge could not check this task's work after %d tries (%s). Re-run the task once the Judge is available.",
				judgeTry, result.Reason), run)
			return runStepEnded, "", ""
		}
		te.writeTaskReason(t, fmt.Sprintf(
			"The Judge could not finish checking the worker's claim (%s). Trying again (try %d of %d).",
			result.Reason, judgeTry+1, judgeUnavailableRetryBound))
	}

	if !te.taskVerdictStillApplicable(t.ID) {
		logger.InfoCF("task_executor",
			"judge verdict dropped: task left in_progress during adjudication (Stop landed concurrently)",
			map[string]any{"task_id": t.ID})
		return runStepEnded, "", ""
	}

	verdict := result.Verdict
	te.writeJudgeVerdictTranscript(t, taskSessionID, verdict)

	// One verdict -> criterion-status writer (verdict_projection.go): the task
	// criteria half here, the goal record's lists inside recordTaskGoalVerdict.
	projectedCriteria, projectedDoD, pstats := projectGoalVerdict(t.Criteria, dod, verdict)
	if pstats.Applied > 0 {
		if _, perr := te.store.Update(t.ID, task.Patch{Criteria: &projectedCriteria}); perr != nil {
			logger.WarnCF("task_executor", "goal: could not persist the verdict projection onto task criteria",
				map[string]any{"task_id": t.ID, "error": perr.Error()})
		} else {
			t.Criteria = projectedCriteria
		}
		if len(dod) > 0 {
			persistTaskGoalDoDProjection(t.ID, projectedDoD)
		}
	}
	recordTaskGoalVerdict(t.ID, verdict, result.Reason)
	state.innerTries++

	if verdict.Met {
		te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, evidence, run)
		return runStepEnded, "", ""
	}

	maxTries := te.runGoalMaxTries(taskSessionID)
	if te.innerTriesUsed(taskSessionID, state) >= maxTries {
		reason := fmt.Sprintf("The goal did not reach a met verdict within %d tries. Latest judge feedback:\n%s",
			maxTries, goalVerdictReasonText(verdict))
		return te.failedRunStep(ctx, t, taskSessionID, reason, run)
	}
	steer := buildSteeringText(evidence, verdict) +
		"\nKeep working on what is unmet, then claim again with goal_claim when it is verified."
	te.appendRunSystemTranscript(t, taskSessionID, sessStore, steer)
	return runStepContinue, steer, ""
}

// needsJudgeAgent reports whether judging set requires the Judge agent's LLM
// turn (any prose criterion; machine checks and behaviour scans do not).
func needsJudgeAgent(set []task.AcceptanceCriterion) bool {
	for _, c := range set {
		if c.Kind != task.KindCheck && c.Kind != task.KindBehavior {
			return true
		}
	}
	return false
}

// runGoalMaxTries resolves the INNER limit for the run bound to taskSessionID:
// the goal try limit snapshotted onto its goal record at activation, else the
// live Settings value.
func (te *TaskExecutor) runGoalMaxTries(taskSessionID string) int {
	if rec := activeGoalForSession(taskSessionID); rec != nil && rec.MaxRounds >= 1 {
		return rec.MaxRounds
	}
	return goalTryLimit(te.agentLoop)
}

// innerTriesUsed is the larger of the goal record's Round (authoritative) and
// the run's in-memory mirror, so a failed counter write can never unbound it.
func (te *TaskExecutor) innerTriesUsed(taskSessionID string, state *taskRunState) int {
	used := state.innerTries
	if rec := activeGoalForSession(taskSessionID); rec != nil && rec.Round > used {
		used = rec.Round
	}
	return used
}

// spendInnerTry spends one goal try WITHOUT a verdict — the turn ended without
// a judgeable claim. It advances the goal record's Round and reports whether
// the try budget is now spent, with the reason when it is.
func (te *TaskExecutor) spendInnerTry(t *task.Task, taskSessionID, why string, state *taskRunState) (exhausted bool, reason string) {
	state.innerTries++
	maxTries := te.runGoalMaxTries(taskSessionID)
	if rec := activeGoalForSession(taskSessionID); rec != nil {
		if _, uerr := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
			cur.Round++
			cur.LatestReason = why
			cur.LastActivityAt = time.Now().UTC()
			return nil
		}); uerr != nil {
			logger.WarnCF("task_executor", "goal: could not persist a spent try on the goal record; the run's own count still bounds it",
				map[string]any{"task_id": t.ID, "goal_id": rec.GoalID, "error": uerr.Error()})
		}
	}
	if te.innerTriesUsed(taskSessionID, state) >= maxTries {
		return true, fmt.Sprintf("The goal did not reach a met verdict within %d tries; the last try was %s.", maxTries, why)
	}
	return false, ""
}

// failedRunStep is the single outer-failure entry point.
func (te *TaskExecutor) failedRunStep(
	ctx context.Context, t *task.Task, taskSessionID, reason string, run *activeRun,
) (runStep, string, string) {
	return runStepFailedRun, "", te.consumeTaskAttempt(ctx, t, taskSessionID, reason, run)
}

// consumeTaskAttempt is the ONLY writer of Task.AttemptCount (the outer
// counter). It runs only for a run that genuinely started and failed. It
// advances the count and either restarts the task in a fresh run — ending the
// run's goal first (endRunGoalForRestart), so the restart's activateTaskGoal
// reactivates it against the new session with a fresh try budget — or, at the
// attempt limit, ends the task Failed with a handover and wakes the owner. A
// concurrent Stop wins.
func (te *TaskExecutor) consumeTaskAttempt(
	ctx context.Context, t *task.Task, taskSessionID, reason string, run *activeRun,
) (redispatchTaskID string) {
	_ = ctx
	maxAttempts := te.resolveTaskMaxAttempts(t)
	hardCeiling := taskAttemptHardCeiling(maxAttempts)

	newAttempt := t.AttemptCount + 1
	nextStatus := task.StatusNext
	updated, uerr := te.store.UpdateIfStatus(t.ID, task.StatusInProgress, task.Patch{AttemptCount: &newAttempt, Status: &nextStatus})
	if uerr != nil {
		if errors.Is(uerr, task.ErrStatusConflict) {
			logger.WarnCF("task_executor",
				"goal: dropping a failed-run outcome — the task left in_progress first (Stop landed); not restarting",
				map[string]any{"task_id": t.ID})
			return ""
		}
		logger.ErrorCF("task_executor", "goal: could not persist the attempt increment; failing the run closed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
		failReason := fmt.Sprintf("could not persist the attempt increment: %v", uerr)
		te.failTask(t.ID, failReason)
		te.closeRun(t.ID, run, task.StatusFailed, failReason)
		return ""
	}

	if newAttempt < maxAttempts && newAttempt <= hardCeiling {
		endRunGoalForRestart(updated.ID, reason)
		restartNote := fmt.Sprintf("The previous run of this task failed (attempt %d of %d): %s", newAttempt, maxAttempts, reason)
		if _, serr := te.store.Update(updated.ID, task.Patch{Result: &restartNote}); serr != nil {
			logger.WarnCF("task_executor", "goal: could not persist why the previous run failed",
				map[string]any{"task_id": updated.ID, "error": serr.Error()})
		}
		te.appendRunSystemTranscript(updated, taskSessionID, te.agentLoop.GetAgentStore(updated.AgentID), restartNote)
		te.supersedeTaskSession(updated.AgentID, taskSessionID)
		logger.InfoCF("task_executor", "goal: run failed — restarting the task in a fresh run",
			map[string]any{"task_id": updated.ID, "attempt": newAttempt, "max_attempts": maxAttempts, "reason": reason})
		return updated.ID
	}

	handover := buildTaskAttemptHandover(updated, reason, maxAttempts)
	if te.completeTaskWithResult(updated, taskSessionID, task.StatusNext, false, handover, run) {
		te.wakeOwnerAttemptsExhausted(updated, taskSessionID, handover)
	}
	return ""
}

// endRunGoalForRestart ends the goal of a run that failed as a whole while its
// task restarts, so the fresh run's activateTaskGoal can Goal.Reactivate it —
// archiving this run's tries and verdict into TerminalHistory. It deliberately
// does NOT go through tools.TerminateTaskGoalRecord: that shared writer's hook
// records the goal outcome line of a task that has ENDED, and this task has not
// — its one outcome line is written when it does (completeTaskWithResult). A
// store fault is logged; the restart's activateTaskGoal then finds the record
// still active and says so.
func endRunGoalForRestart(taskID, reason string) {
	gstore := resolveGoalRecordStore()
	g, err := gstore.GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if !errors.Is(err, goal.ErrOwnerNotFound) {
			logger.WarnCF("task_executor", "goal: could not read the run's goal record to end it before the restart",
				map[string]any{"task_id": taskID, "error": err.Error()})
		}
		return
	}
	if !g.IsActive() {
		return
	}
	endReason := "the run failed and the task restarts in a fresh run: " + reason
	if _, uerr := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
		if !cur.IsActive() {
			return nil
		}
		return cur.Terminate(generated.GoalStateExhausted, endReason, time.Now().UTC())
	}); uerr != nil {
		logger.WarnCF("task_executor", "goal: could not end the run's goal record before the restart",
			map[string]any{"task_id": taskID, "goal_id": g.GoalID, "error": uerr.Error()})
	}
}

// buildTaskAttemptHandover renders the terminal wind-down written to the task
// Result when the attempt limit is reached.
func buildTaskAttemptHandover(t *task.Task, reason string, maxAttempts int) string {
	return fmt.Sprintf("Task failed after %d attempt(s) (max %d).\n\nWhy the last run failed:\n%s\n\n"+
		"Progress/remaining/blockers: review the run transcript for details; the task has been marked failed and its owner notified.",
		t.AttemptCount, maxAttempts, reason)
}

// recordRunClaim records a task run worker's claim on the goal bound to the
// run's session, through recordGoalClaim — the one claim writer the chat path
// uses too (GOAL-FR-013). evidence is the worker's own one-line statement, the
// same text a chat claim records, never the task's composed result.
func (te *TaskExecutor) recordRunClaim(
	t *task.Task, taskSessionID string, status generated.GoalLatestClaimStatus, evidence string,
) {
	rec := activeGoalForSession(taskSessionID)
	if rec == nil {
		return // a task with no active goal record has no claim to keep
	}
	if cerr := recordGoalClaim(rec.GoalID, status, evidence); cerr != nil {
		logger.WarnCF("task_executor", "goal: could not record the worker's claim onto the goal record",
			map[string]any{"task_id": t.ID, "goal_id": rec.GoalID, "status": string(status), "error": cerr.Error()})
	}
}

// endTaskWithoutAttempt ends a running task Failed with reason, consuming no
// attempt and never restarting it. A claim that led here is recorded by the
// caller first (recordRunClaim). The goal itself ends in
// completeTaskWithResult, through tools.TerminateTaskGoalRecord — the one
// shared task-to-goal ending writer, whose after-transition hook writes the
// goal's outcome line into the run's session exactly once. Ending the goal
// here instead would bypass that hook and leave no outcome line.
func (te *TaskExecutor) endTaskWithoutAttempt(t *task.Task, taskSessionID, reason string, run *activeRun) {
	logger.InfoCF("task_executor", "goal: task ended Failed with no attempt used",
		map[string]any{"task_id": t.ID, "reason": reason})
	te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, false, reason, run)
}

// holdsRun reports whether the executor currently holds taskID's run.
func (te *TaskExecutor) holdsRun(taskID string) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	_, ok := te.running[taskID]
	return ok
}

// turnWasReasoningOnly reports whether a try produced no usable answer because
// the output-token limit went to reasoning: the reply is empty AND the
// session's last assistant entry is stamped truncated at the output-token
// limit with no content (ADR-087 D4a).
func turnWasReasoningOnly(taskSessionID string, sessStore *session.UnifiedStore, resp string) bool {
	if strings.TrimSpace(resp) != "" || sessStore == nil || taskSessionID == "" {
		return false
	}
	entries, err := sessStore.ReadTranscript(taskSessionID)
	if err != nil {
		return false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Role != "assistant" || e.Type == session.EntryTypeToolCall {
			continue
		}
		return e.Truncated && e.TruncationReason == truncationReasonMaxOutputTokens && strings.TrimSpace(e.Content) == ""
	}
	return false
}

// writeTaskReason puts an operator-visible reason on a task that stays
// in_progress. Result is a progress field; the terminal write replaces it.
func (te *TaskExecutor) writeTaskReason(t *task.Task, reason string) {
	if _, err := te.store.Update(t.ID, task.Patch{Result: &reason}); err != nil {
		logger.WarnCF("task_executor", "could not write the run's status reason onto the task",
			map[string]any{"task_id": t.ID, "error": err.Error()})
	}
}

// appendRunErrorTranscript records a failed turn in the run session and marks
// the session interrupted. content is written as given: a caller whose error
// may carry a provider response body passes a plain reason instead.
func (te *TaskExecutor) appendRunErrorTranscript(t *task.Task, taskSessionID string, sessStore *session.UnifiedStore, content string) {
	if taskSessionID == "" || sessStore == nil {
		return
	}
	if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-error-%d", t.ID, time.Now().UnixNano()),
		Role:      "assistant",
		Content:   content,
		Status:    "error",
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("task_executor", "Transcript write failed",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
	}
	status := session.StatusInterrupted
	if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &status}); setErr != nil {
		logger.WarnCF("task_executor", "Meta update failed",
			map[string]any{"task_id": t.ID, "error": setErr.Error()})
	}
}

// appendRunSystemTranscript records a steering note in the run session so the
// transcript explains why the next turn happened.
func (te *TaskExecutor) appendRunSystemTranscript(t *task.Task, taskSessionID string, sessStore *session.UnifiedStore, content string) {
	if taskSessionID == "" || sessStore == nil || content == "" {
		return
	}
	if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-steer-%d", t.ID, time.Now().UnixNano()),
		Role:      "system",
		Content:   content,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("task_executor", "goal: steering transcript write failed",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
	}
}

// noClaimSteeringPrompt teaches the one claim path to a worker that ended its
// turn without claiming.
func noClaimSteeringPrompt(externalCLI bool) string {
	if externalCLI {
		return "You ended your turn without reporting completion. When the work is done and verified, end with the " +
			"evidence line and the completion marker:\n  [goal:evidence] <one line stating what you verified>\n  TASK_STATUS: success\n" +
			"If you cannot proceed, end with TASK_STATUS: failure and the reason."
	}
	return "You ended your turn without claiming. When you believe the work is done and verified, call goal_claim with " +
		"status \"met\" and a one-line statement of what you verified. If you cannot proceed and it is not something the " +
		"operator can answer directly, call goal_claim with status \"blocked\" and the reason. Otherwise keep working."
}

// outputOrPlaceholder bounds raw output for a reason, naming the empty case.
func outputOrPlaceholder(resp string) string {
	if strings.TrimSpace(resp) == "" {
		return "(agent produced no output)"
	}
	return truncateTaskOutput(resp)
}

// reasonSuffix renders ": <text>", or ": <fallback>" when text is blank.
func reasonSuffix(text, fallback string) string {
	if s := strings.TrimSpace(text); s != "" {
		return ": " + s
	}
	return ": " + fallback
}

// judgeUnavailableNeedsOperator reports whether a Judge-unavailable reason is
// one only an operator can clear — waiting and retrying cannot help.
func judgeUnavailableNeedsOperator(reason string) bool {
	for _, prefix := range []string{
		JudgeMisconfiguredReasonPrefix,
		JudgeOutputTruncatedReasonPrefix,
	} {
		if strings.HasPrefix(reason, prefix) {
			return true
		}
	}
	return false
}

// judgeOperatorFix strips the machine prefix and returns the plain fix text.
func judgeOperatorFix(reason string) string {
	for _, prefix := range []string{
		JudgeMisconfiguredReasonPrefix,
		JudgeOutputTruncatedReasonPrefix,
	} {
		if after, ok := strings.CutPrefix(reason, prefix); ok {
			return strings.TrimSpace(after)
		}
	}
	return reason
}

// taskOperatorFixReason words a task run's operator-only refusal: that the task
// could not run, what to change and where, and that the task must then be run
// again. Names come from the agent's configuration and from the provider id
// the fallback chain recorded on the error — never from the error's text,
// which can carry a provider response body.
func (te *TaskExecutor) taskOperatorFixReason(agentID string, turnErr error, cause operatorFixCause) string {
	agentName, provider, model := agentID, "", ""
	if te.agentLoop != nil {
		if ag, ok := te.agentLoop.GetRegistry().GetAgent(agentID); ok && ag != nil {
			if name := strings.TrimSpace(ag.Name); name != "" {
				agentName = name
			}
			provider, model = ag.primaryModelPair()
			if needs, id := ag.needsProviderSnapshot(); needs && strings.TrimSpace(id) != "" {
				provider = id
			}
		}
	}
	var fe *providers.FailoverError
	if errors.As(turnErr, &fe) && strings.TrimSpace(fe.Provider) != "" {
		provider = fe.Provider
	}
	return taskOperatorFixText(cause, strings.TrimSpace(agentName), strings.TrimSpace(provider), strings.TrimSpace(model))
}

// taskOperatorFixText renders taskOperatorFixReason's sentence. A name that is
// unknown is left out of the sentence rather than shown blank.
func taskOperatorFixText(cause operatorFixCause, agentName, provider, model string) string {
	const lead, rerun = "The task could not run: ", ", then run the task again."
	agent := "the agent"
	if agentName != "" {
		agent = "the agent " + agentName
	}
	switch cause {
	case operatorFixCredentialsRejected:
		if provider != "" {
			return lead + "the provider key for " + provider + " was rejected. Fix it in Settings → Providers" + rerun
		}
		return lead + "the provider rejected the key. Fix it in Settings → Providers" + rerun
	case operatorFixSignInExpired:
		if provider != "" {
			return lead + "the sign-in for " + provider + " has expired. Sign in again in Settings → Providers" + rerun
		}
		return lead + "the provider sign-in has expired. Sign in again in Settings → Providers" + rerun
	case operatorFixProviderNotConfigured:
		if provider != "" {
			return lead + agent + " uses the provider " + provider + ", which is not configured. " +
				"Configure it in Settings → Providers or pick a configured provider in the agent's settings" + rerun
		}
		return lead + agent + " has no configured provider. " +
			"Configure one in Settings → Providers or pick a configured provider in the agent's settings" + rerun
	case operatorFixModelUnassigned:
		return lead + agent + " has no model assigned. Pick a model in the agent's settings" + rerun
	case operatorFixContextWindowUnknown:
		subject := "the agent's model"
		if model != "" {
			subject = "the model " + model
		}
		return lead + subject + " did not report a context length. " +
			"Set it in Settings → Models → Model overrides → Context length" + rerun
	case operatorFixAgentNotOnWorkspace:
		return lead + agent + " is not on any workspace team, so it has nowhere to work. Add it to a workspace team" + rerun
	case operatorFixWorkDirUnavailable:
		return lead + "the working folder for " + agent + " could not be opened. " +
			"Check that the disk has space and the folder is writable" + rerun
	}
	return lead + "a setting needs an operator's attention. Check the run transcript for which one" + rerun
}

// taskVerdictStillApplicable re-reads taskID's CURRENT status and reports
// whether an outcome computed moments ago may still be applied: the task must
// still be in_progress. A read failure fails SAFE (drop the outcome).
func (te *TaskExecutor) taskVerdictStillApplicable(taskID string) bool {
	current, err := te.store.Get(taskID)
	if err != nil {
		logger.WarnCF("task_executor",
			"could not re-read task before applying an outcome (fail-safe: dropping it)",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return false
	}
	return current.Status == task.StatusInProgress
}
