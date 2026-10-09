// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The body of one steered turn, chosen by dispatch kind.
//
// ADR-091 funnels every steered session through ONE turn engine: the first
// run/revival (steer_launcher.go::runDispatchedSteeredTurn), the wake
// (loop_inbound.go::processSteeredSystemWake), and the post-turn
// continuation (steer_turn_drain.go::continueSteeredTurn) all rebuild the
// turn through reconstructSteeredTurn and then run it. This file is the ONE
// place that decides HOW that body runs — the ADR-091 analogue of the branch
// the pre-ADR-091 deleted spawnSubTurn carried at its "execute the sub-turn"
// step: a target whose own Subagents.Executor resolves to
// runner.DispatchKindExternalCLI drives the external CLI through the SAME
// gate (runner.ResolveDispatch) and the SAME runner (runExternalCLISubTurn,
// external_dispatch.go) the task path uses
// (task_executor_run.go::processTaskDirectExternalCLI). Everything else runs
// the native Omnipus loop exactly as before.
//
// There is no second external-CLI runner and no second dispatch gate here.
// ResolveDispatch is the gate processTaskDirect (task_executor.go) and
// dispatchesExternalCLI (task_executor_run.go) already call before choosing a
// path, and runExternalCLISubTurn is the one runner they call.
//
// A CLI agent is a delegation TARGET like any other; it may never be a MAIN
// agent. That ceiling is enforced at the door that knows about main agents
// (the agent catalog / launch-time stamping), not here: if a steered turn was
// admitted at all, its target's own executor decides how it runs.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// runSteeredTurnBody runs one admitted steered turn and returns the native
// turnResult shape, so every caller's existing drain/completion tail is
// unchanged whether the body actually ran native or through an external CLI.
//
// The dispatch kind is resolved from the turn's OWN agent (the reconstructed
// target, never the delegating parent's) through the shared gate. An
// unresolvable executor kind (remote-a2a is reserved; anything else unknown)
// fails the turn VISIBLY rather than silently running the native loop for a
// target that asked for something else — the clean-failure posture
// pre-ADR-091 spawnSubTurn had, and the same posture the launch path takes
// (steer_launcher.go::resolveLaunchDispatchKind). A resolver error must never
// degrade into a native run: that is exactly the class of drift this file
// exists to close.
func (al *AgentLoop) runSteeredTurnBody(ctx context.Context, rec *session.LifecycleRecord, ts *turnState) (turnResult, error) {
	kind, dispatchErr := runner.ResolveDispatch(executorConfigOf(ts.agent))
	if dispatchErr != nil {
		// F3: a resolver failure must still run the turn teardown. The native
		// branch below is torn down by runTurn itself and the external branch by
		// runExternalCLISteeredTurn's own defers — but this early return used to
		// install neither, so an admitted turn refused here stayed registered:
		// Finished() never closed, IsAlive() stayed true, and a claimed cancel's
		// finish callback never fired.
		al.finishRefusedSteeredTurnBody(ts)
		reason := "steer: dispatch: " + dispatchErr.Error()
		return turnResult{status: TurnEndStatusError, finalContent: reason, turnFailed: true}, dispatchErr
	}
	// F4 = Property B: the Is3P classification is FIXED for a child when it is
	// launched (steer_launcher.go stamps LifecycleRecord.Is3P from the SAME
	// ResolveDispatch outcome). If the live executor kind now disagrees with that
	// stamp, the agent's executor config was changed after the worker launched
	// (the supported native→external update through
	// pkg/gateway/rest_agents_update.go::restAPIUpdateAgentFlow.validateTarget, or
	// the reverse). Never silently flip Is3P and never switch runtime under the
	// worker's feet: refuse VISIBLY, through the same teardown path F3 uses, with
	// an actionable message telling the person to start a new delegation with the
	// updated configuration.
	liveIs3P := kind == runner.DispatchKindExternalCLI
	if mismatch := verifyRecordedRuntimeClassification(rec, ts.agent.ID, liveIs3P); mismatch != nil {
		al.finishRefusedSteeredTurnBody(ts)
		return turnResult{status: TurnEndStatusError, finalContent: mismatch.Error(), turnFailed: true}, mismatch
	}
	if kind != runner.DispatchKindExternalCLI {
		return al.runTurn(ctx, ts)
	}
	return al.runExternalCLISteeredTurn(ctx, rec, ts)
}

// finishRefusedSteeredTurnBody runs the SAME teardown runExternalCLISteeredTurn's
// own defers run, for a body that REFUSES before either execution branch installs
// teardown (a resolver error — F3 — or a fixed-classification mismatch — F4).
// clearActiveTurn runs FIRST, then Finish, the LIFO order loop.go documents
// ("clearActiveTurn runs FIRST, then ... Finish"). Without it an admitted turn
// refused before dispatch stays in al.activeTurnStates for ever: Finished() never
// closes, IsAlive() stays true, and a claimed cancel's onCancelFinish callback
// never fires.
func (al *AgentLoop) finishRefusedSteeredTurnBody(ts *turnState) {
	al.clearActiveTurn(ts)
	ts.Finish(ts.hardAbortRequested())
}

// errSteeredWorkerRuntimeChanged is the base of F4's visible refusal — a worker's
// runtime changed after it launched, so its fixed classification no longer
// matches the live executor. Base var (not a fmt literal) so callers and tests
// can match it with errors.Is.
var errSteeredWorkerRuntimeChanged = errors.New("steer: dispatch: worker runtime changed after launch")

// errUnpersistedRuntimeClassification is the record-missing case of the
// Property-B invariant (Q2=A: no run begins with an unpersisted classification)
// — the session has a dispatch but no durable record to verify it against.
var errUnpersistedRuntimeClassification = errors.New("steer: dispatch: session has no persisted runtime classification")

// delegateRuntimeRecovery is the recovery sentence for a DELEGATE dispatch whose
// recorded runtime no longer matches the live executor: the fix is a fresh
// delegation with the updated configuration.
const delegateRuntimeRecovery = "a worker's runtime is fixed when it is launched — start a new delegation with the updated configuration"

// taskRuntimeRecovery is the recovery sentence for a TASK-executor run whose
// recorded runtime no longer matches the live executor: a task is restarted by
// an explicit Rerun, not by a new delegation.
const taskRuntimeRecovery = "the worker's runtime changed after this run started; Rerun the task to start a new run"

// verifyRecordedRuntimeClassification is F4's Property-B invariant, shared by
// BOTH dispatch sites that must honor it: the steered body
// (runSteeredTurnBody) and the task executor (processTaskDirect). Every
// execution on a session must run the runtime its durable LifecycleRecord.Is3P
// records, or refuse VISIBLY before any driver/provider call.
//
// It returns nil when the record's Is3P equals the live executor kind, and an
// actionable refusal (naming the worker, both runtimes, and the recovery)
// otherwise — or when the record is missing entirely (unpersisted
// classification). recovery is the caller-appropriate next step, because a
// task restarts by Rerun while a delegation restarts by a new delegation.
func verifyRecordedRuntimeClassification(rec *session.LifecycleRecord, agentID string, liveIs3P bool) error {
	if rec == nil {
		return fmt.Errorf("%w: worker %q", errUnpersistedRuntimeClassification, agentID)
	}
	if rec.Is3P == liveIs3P {
		return nil
	}
	recovery := delegateRuntimeRecovery
	if rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask {
		recovery = taskRuntimeRecovery
	}
	return steeredWorkerRuntimeRefusal(agentID, rec.Is3P, liveIs3P, recovery)
}

// steeredWorkerRuntimeRefusal builds F4's ACTIONABLE refusal: it names the
// worker, both runtimes, and the caller-appropriate recovery — never a bare
// mismatch report.
func steeredWorkerRuntimeRefusal(agentID string, recordIs3P, liveIs3P bool, recovery string) error {
	return fmt.Errorf(
		"%w: worker %q was launched as a %s worker, but its agent now resolves to a %s worker; %s",
		errSteeredWorkerRuntimeChanged, agentID, runtimeLabel(recordIs3P), runtimeLabel(liveIs3P), recovery)
}

// runtimeLabel is the plain-language name of a worker runtime for F4's refusal.
func runtimeLabel(is3P bool) string {
	if is3P {
		return "external-CLI"
	}
	return "native"
}

// runExternalCLISteeredTurn drives one steered turn through the shared
// external-CLI runner instead of the native loop.
//
// Teardown mirrors runTurn's own deferred pair in the same LIFO order
// (loop.go: "clearActiveTurn runs FIRST, then finalizeStreamer, then Finish"):
// clearActiveTurn runs FIRST, Finish LAST. runTurn does this internally, so
// the native branch returns with the turn already cleared from
// al.activeTurnStates and finished. This branch must do the same or a
// completed CLI delegate stays registered as an active turn for ever, and a
// cancel racing its tail registers a finish callback that can never fire.
// Same shape as the task-mode external dispatch
// (task_executor_run.go::processTaskDirectExternalCLI).
func (al *AgentLoop) runExternalCLISteeredTurn(ctx context.Context, rec *session.LifecycleRecord, ts *turnState) (turnResult, error) {
	defer func() { ts.Finish(ts.hardAbortRequested()) }()
	defer al.clearActiveTurn(ts)

	// runExternalCLISubTurn reads agent.Model unlocked (transcript attribution
	// + RunOptions.Model). The reconstructed turn's agent is the shared
	// registry instance whenever the launch carried no tool exclusions, so
	// snapshot the mutex-protected quad first — exactly what
	// processTaskDirectExternalCLI does (FIX 1) and what the pre-ADR-091
	// spawnSubTurn's execSource snapshot did, for the same
	// SwitchModel/ApplyAgentModel race.
	ts.agent = ts.agent.snapshotForExternalDispatch()

	input := composeDelegateInput(al, steeredExternalCLIInput(ts), "", ts.agent.ID)
	result, runErr := runExternalCLISubTurn(ctx, al, ts, input, steeredExternalCLITimeout(rec))
	if result == nil {
		// runExternalCLISubTurn's two return shapes are (nil, err) and
		// (result, result.Err). A nil result with a nil error is a broken
		// invariant; fail loudly rather than reporting an empty success, the
		// same choice processTaskDirectExternalCLI made for its dead branch.
		if runErr == nil {
			runErr = errExternalDispatchNilResult
		}
		return turnResult{status: TurnEndStatusError, finalContent: runErr.Error(), turnFailed: true}, runErr
	}

	answer := strings.TrimSpace(result.ForUser)
	if answer == "" {
		answer = strings.TrimSpace(result.ForLLM)
	}
	status := TurnEndStatusCompleted
	if runErr != nil {
		status = TurnEndStatusError
	}
	return turnResult{status: status, finalContent: answer, turnFailed: runErr != nil}, runErr
}

// errExternalDispatchNilResult is the invariant violation named above — a
// package-level value (not a fmt.Errorf literal at the call site) so a test
// can assert this branch was taken.
var errExternalDispatchNilResult = errors.New("steer: external-cli dispatch returned no result and no error")

// steeredExternalCLIInput is the instruction the external CLI is driven with.
//
// For a first run or a wake that is ts.opts.UserMessage, the turn's own
// instruction, exactly what the native loop consumes. The post-turn
// continuation (steer_turn_drain.go::continueSteeredTurn) deliberately clears
// UserMessage and carries the queued instructions on
// opts.InitialSteeringMessages instead — for a CLI target those ARE the
// instruction, so they are joined here rather than reaching the CLI as an
// empty prompt.
func steeredExternalCLIInput(ts *turnState) string {
	if msg := strings.TrimSpace(ts.opts.UserMessage); msg != "" {
		return msg
	}
	parts := make([]string, 0, len(ts.opts.InitialSteeringMessages))
	for _, m := range ts.opts.InitialSteeringMessages {
		if content := strings.TrimSpace(m.Content); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// steeredExternalCLITimeout is the driver-level hint handed to
// runExternalCLISubTurn (RunOptions.TimeoutSeconds / the defaultSubTurnTimeout
// fallback). The run's real bound is the Go context the caller already
// derived from this same record (steeredTurnRunContext), so this is the
// remaining active-time budget — the honest value for the hint, and the same
// budget the context enforces. No deadline (an unsteered/ordinary root record,
// or an edge with no configured limit) falls back to the launcher's own
// default rather than inventing one.
func steeredExternalCLITimeout(rec *session.LifecycleRecord) time.Duration {
	if deadline, ok := rec.ActiveBudgetDeadline(time.Now()); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			return remaining
		}
	}
	return defaultSteeredSessionTimeout
}
