// external_dispatch.go — Spec-4 wiring: run a delegated sub-agent via an external
// CLI runner instead of the native Omnipus agent loop.
//
// This is the production wiring site for ExecutorKind="external-cli". The native
// path (steer_launcher.go's SteerLauncher.Dispatch -> al.runTurn via
// runDispatchedSteeredTurn; pre-ADR-091, spawnSubTurn -> al.runTurn) remains
// the default; this file is reached ONLY when the resolved sub-agent's
// SubagentsConfig.Executor resolves to runner.DispatchKindExternalCLI (see
// runner.ResolveDispatch).
//
// Flow (FR-5.1 / FR-5.2 / FR-5.4; FR-5.3 as amended by ADR-032):
//
//  1. Resolve the run's working directory — the DELEGATE's own workspace
//     directory (ADR-032, 2026-07: runs execute IN the agent's real workspace,
//     not an isolated git-worktree/temp-dir copy — see
//     docs/internal/architecture/ADR-032-external-agent-workspace-execution.md).
//     The external CLI's OWN sandbox remains the kernel boundary (operator
//     decision — no new Omnipus confiner).
//  2. NewDriver — instantiate the claude-code / codex / opencode driver.
//  3. Run — drive the CLI with the delegated input + a per-run timeout and turn
//     cap derived from sub-turn config, with the delegate's own model auto-set.
//  4. Stream events through ConsentDispatcher: permission-requests are
//     auto-approved unconditionally (operator decision, 2026-07-05, issue
//     #488 — see policyApproverConsent's own doc for why); all events
//     (output/tool-call/diff/error/permission-request) are written to the
//     sub-agent session transcript so the SPA renders the run inline.
//
// No teardown step: the workspace directory is the agent's persistent working
// tree, not a scratch dir — it is left in place after the run.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// executorConfigOf returns the sub-agent executor config for an AgentInstance, or
// nil (→ native) when the agent declares no Subagents block. Nil is handled by
// runner.ResolveDispatch (EffectiveKind defaults to native).
func executorConfigOf(a *AgentInstance) *config.ExecutorConfig {
	if a == nil || a.Subagents == nil {
		return nil
	}
	return a.Subagents.Executor
}

// workspaceRunLocks serializes external-CLI runs that target the SAME
// workspace directory (FIX 4 / arch #2 warning). ADR-032 removed worktree
// isolation — external-CLI sub-turns now run directly in the delegate
// agent's own persistent workspace — so two concurrent sub-turns delegating
// to the same agent (or to two agents that happen to share a workspace
// path) would otherwise spawn two child CLI processes reading/writing the
// same directory concurrently: file races, a corrupted git index, clobbered
// output files. Keyed by the workspace's cleaned path; the lock is held for
// the FULL duration of the child run (acquired near the top of
// runExternalCLISubTurn, released via defer), which is the window in which
// the child process touches the workspace tree. Runs against distinct
// workspace paths are never blocked by each other — only same-path runs
// serialize.
//
// Each entry is a buffered channel of capacity 1 holding a single token —
// acquiring the lock is receiving the token, releasing is sending it back.
// This replaces a plain *sync.Mutex (BLOCK finding, 7-reviewer gate on
// FIX 1): Mutex.Lock() blocks unconditionally, with no way for a queued
// waiter to observe a cancel that fires while it is still waiting for the
// lock — a session-wide cancel arriving during that wait was a silent,
// PERMANENT no-op (requestHardAbort latches hardAbort=true on its first
// call and never re-fires once the nil cancel funcs are later replaced with
// real ones). A channel lets acquireWorkspaceRunLockCtx select on the
// caller's ctx.Done() alongside the receive, so a cancel firing while queued
// unblocks the wait immediately and the queued run is skipped entirely,
// instead of becoming uncancelable until the lock eventually frees.
//
// Entries are never removed from the map: a long-running gateway
// accumulates one token channel per distinct workspace path ever dispatched
// to. That set is bounded by the number of configured external-cli
// agents/workspaces (small, operator-controlled), not by run count, so the
// unbounded map is an accepted trade-off rather than a leak.
var workspaceRunLocks sync.Map // map[string]chan struct{}

// workspaceRunLockChan returns the capacity-1 token channel for workDir's
// cleaned path, creating and pre-loading it with a single token on first use
// via LoadOrStore.
func workspaceRunLockChan(workDir string) chan struct{} {
	key := filepath.Clean(workDir)
	fresh := make(chan struct{}, 1)
	fresh <- struct{}{}
	chAny, _ := workspaceRunLocks.LoadOrStore(key, fresh)
	ch, ok := chAny.(chan struct{})
	if !ok {
		// workspaceRunLocks is populated exclusively by this function with
		// chan struct{} tokens; a different type under this key means the
		// map invariant was corrupted by a programming error elsewhere.
		// Continuing with the wrong type would silently bypass the
		// mutual-exclusion lock and reintroduce the undetectable
		// concurrent-workspace-write race this map exists to prevent, so
		// abort loudly rather than hand back an unusable lock.
		panic(fmt.Sprintf("workspaceRunLocks: invariant violated for key %q: got %T, want chan struct{}", key, chAny))
	}
	return ch
}

// acquireWorkspaceRunLockCtx acquires the workDir run lock in a cancel-aware
// way: it selects on receiving the path's token OR ctx.Done(). Returns
// (release, true) when the token was acquired — the caller MUST `defer`
// release() (which sends the token back) so the lock releases even on an
// early return or panic. Returns (nil, false) when ctx ended while still
// waiting for the token — the caller must NOT start the run in that case;
// there is nothing to release.
func acquireWorkspaceRunLockCtx(ctx context.Context, workDir string) (release func(), acquired bool) {
	ch := workspaceRunLockChan(workDir)
	select {
	case <-ch:
		return func() { ch <- struct{}{} }, true
	case <-ctx.Done():
		return nil, false
	}
}

// runExternalCLISubTurnState carries the shared state of runExternalCLISubTurn across its stages.
type runExternalCLISubTurnState struct {
	al          *AgentLoop
	childTS     *turnState
	timeout     time.Duration
	agent       *AgentInstance
	runID       string
	timeoutSecs int
	maxTurns    int
	agentModel  string
	childEnv    []string
	execCfg     *config.ExecutorConfig
	cliArgs     []string
}

// runExternalCLISubTurn executes a delegated sub-agent task through an external
// CLI runner. It returns a tools.ToolResult mirroring the native sub-turn return
// shape (ForLLM/ForUser carry the run's aggregated output; Err is set on failure).
//
// childTS is the sub-turn's turnState (used for transcript writes + agent ID).
// task is the delegated input prompt. timeout bounds the whole run.
//
// childTS.agent is the resolved DELEGATE's own AgentInstance
// (steer_reconstruct.go::reconstructSteeredTurn sources it from
// al.GetRegistry().GetAgent(rec.AgentID), rec.AgentID being the TARGET named
// by the original LaunchRequest.TargetAgentID; pre-ADR-091, the deleted
// spawnSubTurn sourced Workspace/Model/MaxIterations/Subagents the same way
// when dispatch resolved to external-cli), so every field read below already
// reflects the delegate's own identity, not the delegating parent's.
// reportWorkspaceRefusal emits the typed error frame and stamps the
// transcript for a workspace resolution that refused, then returns the
// wrapped error for the caller to return.
//
// Extracted from runExternalCLISubTurn to keep it inside the 240-line
// function budget. It is the same defect class the native runTurn path
// fixed: the sentinel was known and the driver never started, but nothing
// TYPED reached the user -- and because a parent's delegate is hidden on
// failure, the thread stayed silent unless the parent happened to narrate
// it. Emitting the catalogue frame AND appending the classified error is
// what makes the refusal visible in the child's own view and as a status
// line in the parent's side panel.
func (ed *runExternalCLISubTurnState) reportWorkspaceRefusal(wsErr error) error {
	llm := TranslateTurnError(wsErr)
	chatID := ed.childTS.chatID
	if chatID == "" {
		chatID = ed.childTS.opts.ChatID
	}
	ed.al.emitEvent(
		EventKindError,
		ed.childTS.eventMeta("runTurn", "turn.error"),
		ErrorPayload{
			Stage:     "workspace",
			ChatID:    chatID,
			SessionID: string(ed.childTS.routingSessionID),
			Code:      string(llm.Code),
			Message:   llm.Message,
		},
	)
	ed.childTS.appendClassifiedError(EventKindError.String(), "workspace", llm)
	return fmt.Errorf("external-cli dispatch: %w", wsErr)
}

func runExternalCLISubTurn(
	ctx context.Context,
	al *AgentLoop,
	childTS *turnState,
	task string,
	timeout time.Duration,
) (*tools.ToolResult, error) {
	ed := &runExternalCLISubTurnState{al: al, childTS: childTS, timeout: timeout}

	ed.agent = ed.childTS.agent
	if ed.agent == nil || ed.agent.Subagents == nil || ed.agent.Subagents.Executor == nil {
		return nil, fmt.Errorf("external-cli dispatch: missing executor config")
	}
	cli := ed.agent.Subagents.Executor.CLI
	if cli == "" {
		return nil, fmt.Errorf("external-cli dispatch: executor.cli is empty (set claude-code, codex, or opencode)")
	}

	ed.runID = ed.childTS.turnID
	if ed.runID == "" {
		ed.runID = fmt.Sprintf("ext-%d", time.Now().UnixNano())
	}

	// 1. Resolve the run's working directory: the workspace's dedicated
	//    project-work subdirectory (workspaces/<id>/work/), via the SAME
	//    shared gate the native path uses (pkg/agent/loop.go's runTurn calls
	//    the identical resolveTurnWorkDirOrRefuse, workspace_reroot.go).
	//
	//    ADR-046 P1 (FR-007/008): execution is always workspace-scoped, for
	//    EVERY dispatch kind, no exceptions — an agent that is not a member of
	//    ANY workspace's CoreTeam cannot execute at all. This is deliberately
	//    NOT an "OVERRIDE" of some other default: unlike the pre-ADR-046
	//    behavior (ADR-032's "run directly in the agent's own workspace
	//    directory"), there is no fallback to agent.Home left at all — a
	//    non-member external-CLI dispatch is refused exactly like a
	//    non-member native turn, via the same ErrAgentNotWorkspaceMember. A
	//    prior version of this function fell through to agent.Home on
	//    !found, and warn-and-fell-through to agent.Home when a MEMBER's
	//    SafeWorkDir errored — both were the asymmetry a 7-reviewer gate
	//    flagged as a BLOCK/HIGH (native runTurn already hard-refused both
	//    cases). Neither fallback exists anymore.
	//
	//    workspace.FindForAgentPreferring (inside resolveTurnWorkDirOrRefuse)
	//    keys PRIMARILY off agent IDENTITY (CoreTeam membership), which is a
	//    different, independent signal from the channel-bound turn
	//    workspace_id that tools.WithWorkspaceID routes memory to — see
	//    pkg/agent/loop.go's runTurn for the mirrored resolution and the
	//    divergence discussion (FR-030: memory routing is untouched by this
	//    gate). It additionally breaks a multi-membership tie in favor of
	//    childTS.opts.WorkspaceID (the current turn's own channel-bound
	//    workspace) when the agent is actually a member of that specific
	//    workspace — pre-ADR-091, this was DOCUMENTED as almost always empty
	//    here, since subagent_3p is delegation-only and the deleted
	//    spawnSubTurn never threaded a workspace_id into a child's
	//    processOptions, so this fell straight through to
	//    FindForAgentPreferring's identity-only resolution. ADR-091 fix lane
	//    RX-SUBTURN finding (comment-only; code unchanged): today
	//    steer_reconstruct.go::reconstructSteeredTurn DOES set
	//    opts.WorkspaceID from rec.WorkspaceID, which steer_launcher.go's
	//    launchSteered inherits from the steering session's own workspace —
	//    so childTS.opts.WorkspaceID appears to be commonly NON-empty for a
	//    delegate child now, unlike the "almost always empty" premise below.
	//    Whether that changes which branch actually fires in practice needs
	//    a team check — flagged, not resolved here; kept
	//    for symmetry with the native path and in case that assumption
	//    changes.
	//
	//    The dedicated work/ subdirectory (not workspaces/<id>/ itself) keeps
	//    AGENT.md (Project Instructions) and the shared memory room (.omnipus/)
	//    structurally unreachable — os.Root-confined tools cannot open a path
	//    outside their root, not merely guarded against.
	workDir, workWsID, wsErr := resolveTurnWorkDirAndWorkspaceOrRefuse(ctx, ed.agent.ID, ed.agent.Home, ed.childTS.opts.WorkspaceID)
	if wsErr != nil {
		return nil, ed.reportWorkspaceRefusal(wsErr)
	}

	// FIX 1 (cancel propagation, BLOCK finding on the 7-reviewer gate): create
	// the run's context and register its cancel func on childTS — the SAME
	// way al.runTurn does for the native path (loop.go's ts.setTurnCancel /
	// ts.setProviderCancel — the ONLY other call sites, grep-verified) — and
	// do this BEFORE acquiring the workspace run lock below. The native path
	// already follows this exact order (ts.setTurnCancel is called before
	// al.registerActiveTurn, loop.go), and this dispatch path must too:
	// registering the lock FIRST (the original order here) left a window,
	// while a run was queued behind another same-workspace run, during which
	// childTS.providerCancel/turnCancel were still nil — a cancel firing in
	// that window was a silent, PERMANENT no-op (requestHardAbort latches
	// hardAbort=true on its very first call and never re-fires once the nil
	// cancel funcs are later replaced with real ones once the lock frees).
	// Moving this registration above the lock acquire means a cancel that
	// fires while queued now cancels runCtx directly, which the cancel-aware
	// lock acquisition immediately below observes.
	//
	// Without this registration at all, childTS.providerCancel/turnCancel
	// would stay nil, so the session-wide cancel cascade
	// (Interrupt/InterruptSessionHard, steering.go — ADR-057 FR-041's
	// collapsed two-function entry points, which fire those two turnState
	// fields directly, never through context inheritance) is a silent no-op
	// for an external-CLI sub-turn: childCtx is deliberately detached from
	// the parent's ctx tree — pre-ADR-091, spawnSubTurn did this with an
	// inline context.Background() call. ADR-091 fix lane RX-SUBTURN note
	// (comment-only; code unchanged): today runCtx is built below via
	// context.WithCancel(ctx), where ctx is this function's own parameter —
	// tracing its callers (task_executor_run.go's dispatchCtx, itself
	// derived from the incoming ctx; and, for a delegate-tool dispatch,
	// ultimately steer_launcher.go::runDispatchedSteeredTurn's
	// steeredTurnRunContext(context.Background(), rec)) suggests the same
	// Background()-rooted detachment still holds indirectly, but this was
	// not re-verified end-to-end for this lane. Worst case, pre-ADR-091: a
	// SYNCHRONOUS delegate (`delegate(async=false)`) deadlocked the parent
	// inside this call for up to the full run timeout while the UI showed
	// graceful→hard→detached as if cancel worked — ADR-091 D4 deleted the
	// async=false option outright, so this specific scenario no longer
	// applies, though the underlying "nothing else can ever cancel runCtx
	// without this registration" concern is presumably still real for
	// whatever timeout/cancel path replaced it.
	//
	// One cancel func for both slots is the correct behavior here (not a
	// simplification): runner.ExternalAgentRunner exposes no distinct graceful
	// stop — its doc says "canceling [ctx] is equivalent to calling Cancel"
	// (immediate termination), and all three drivers (claude/codex/opencode)
	// bind the OS child via exec.CommandContext(runCtx, ...), so canceling
	// runCtx already kills the subprocess outright. Firing the graceful stage
	// (Interrupt → providerCancel) is therefore already sufficient to end the
	// run; the hard stage (InterruptSessionHard → providerCancel +
	// turnCancel) re-fires the same (idempotent) cancel func defensively.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ed.childTS.setTurnCancel(cancel)
	ed.childTS.setProviderCancel(cancel)

	// 2. Build the consent handler. External-CLI permission requests are
	//    auto-approved unconditionally (issue #488) — see policyApproverConsent's
	//    type doc for why this is a deliberately different posture from native
	//    ask-policy tools.
	consent := &policyApproverConsent{
		agentID: ed.childTS.agentID,
		runID:   ed.runID,
	}

	ed.prepareRunOptions()

	// 3. Select the driver and how to start it (FR-043, N5/N7). A CONTINUATION
	//    that is delivering a queued follow-up instruction Resumes the session's
	//    existing native CLI conversation; a later entry of a steered session
	//    that has no retained driver — or any entry of a session that already
	//    ran — refuses rather than starting a fresh conversation; only a genuine
	//    first launch Runs fresh. The selection lives in external_run_session.go
	//    — one place, keyed per child session, so the resume reuses the SAME
	//    driver (its captured native conversation id + preserved RunOptions:
	//    runtime/workspace/model/caps) rather than starting a fresh conversation.
	//    The factory stays a package var so in-package tests can inject a
	//    fake/stub driver. cancel is the run's own cancel func, retained on the
	//    holder so a steer delivery interrupts THIS run (N2). resumeOnly is true
	//    for a steered (non-task) session, whose every post-launch entry must
	//    resume-or-refuse.
	sessionKey := externalRunSessionKey(ed.childTS)
	resumeOnly, resumeOnlyErr := al.externalRunResumeOnly(sessionKey)
	if resumeOnlyErr != nil {
		// NEW-3: an unreadable session record refuses before any driver exists;
		// it must never fall through to a fresh, unmarked run.
		return nil, fmt.Errorf("external-cli dispatch: %w", resumeOnlyErr)
	}
	sess, resume, beginErr := al.beginExternalRun(sessionKey, ed.childTS, cancel, resumeOnly)
	if beginErr != nil {
		return nil, fmt.Errorf("external-cli dispatch: %w", beginErr)
	}
	defer al.finishExternalRun(sess, sessionKey)

	// 1 (continued). A CONTINUATION must run in — and lock — the SAME workspace
	//    the native CLI conversation started in (N1): re-resolve the preserved
	//    workspace and refuse when the agent is no longer eligible for it, so
	//    this invocation never authorizes one workspace while the retained driver
	//    executes in another. A first run records the workspace it resolved for
	//    the continuation's later check.
	resolvedWorkDir := workDir
	if resume {
		preservedWorkDir, preservedWsID := sess.workspaceSnapshot()
		rw, _, rwErr := resolveTurnWorkDirAndWorkspaceOrRefuse(ctx, ed.agent.ID, ed.agent.Home, preservedWsID)
		if rwErr != nil {
			return nil, ed.reportWorkspaceRefusal(rwErr)
		}
		if preservedWorkDir == "" || filepath.Clean(rw) != filepath.Clean(preservedWorkDir) {
			return nil, ed.reportWorkspaceRefusal(fmt.Errorf(
				"%w: agent %q: start a new delegation", errExternalWorkspaceChanged, ed.agent.ID))
		}
		resolvedWorkDir = preservedWorkDir
	} else {
		sess.recordWorkspace(workDir, workWsID)
	}

	// FIX 4 (concurrency, arch #2 warning): serialize external-CLI runs that
	// share this workspace directory — see workspaceRunLocks' doc comment. Held
	// for the whole run (driver instantiation through the drain loop below)
	// since that is the window during which the child process can touch the
	// workspace tree; a different workspace path is never blocked by this.
	// Acquired AFTER driver selection so a CONTINUATION locks the workspace the
	// conversation actually runs in (resolvedWorkDir == the preserved work dir).
	//
	// Cancel-aware acquire (BLOCK finding, layer 2 of the fix): waits for the
	// token OR runCtx ending, whichever comes first. Because runCtx's cancel
	// func was registered on childTS above (layer 1), a cancel that fires while
	// this run is queued behind another same-workspace run now unblocks this
	// select right away and the run below is skipped entirely, rather than
	// silently becoming uncancelable until the lock eventually frees.
	releaseWorkspaceLock, canceled, cancelErr := acquireRunSlot(runCtx, cli, resolvedWorkDir)
	if canceled != nil {
		return canceled, cancelErr
	}
	defer releaseWorkspaceLock()

	// Obtain the driver only now that the pre-start checks have passed: a fresh
	// Run creates one (recorded for a later continuation to Resume); a Resume
	// reuses the retained one. A canceled-before-start run therefore never
	// instantiates a driver (the belt-and-suspenders check above).
	if !resume && resumeOnly {
		// N7: durably record that this steered session's first CLI run is
		// starting, BEFORE any driver exists, so a revive after a restart
		// resumes-or-refuses instead of starting a fresh conversation.
		if markErr := al.markExternalRunStarted(sessionKey); markErr != nil {
			return nil, fmt.Errorf("external-cli dispatch: %w", markErr)
		}
	}
	driver, driverErr := sess.driverForRun(cli, consent, resume)
	if driverErr != nil {
		return nil, fmt.Errorf("external-cli dispatch: %w", driverErr)
	}

	return ed.startAndDrain(runCtx, driver, consent, cli, resolvedWorkDir, task, resume)
}

// startAndDrain starts (Run) or continues (Resume) the external CLI on driver,
// routes its events through the consent dispatcher, drains them into the
// transcript and waits for the process-exit barrier. Extracted from
// runExternalCLISubTurn unchanged in behaviour.
func (ed *runExternalCLISubTurnState) startAndDrain(
	runCtx context.Context,
	driver runner.ExternalAgentRunner,
	consent runner.ConsentHandler,
	cli, resolvedWorkDir, task string,
	resume bool,
) (*tools.ToolResult, error) {
	runOpts := runner.RunOptions{
		RunID:          ed.runID,
		WorkDir:        resolvedWorkDir,
		Input:          task,
		Env:            ed.childEnv,
		TimeoutSeconds: ed.timeoutSecs,
		MaxTurns:       ed.maxTurns,
		CLIPath:        ed.execCfg.CLIPath,
		CLIArgs:        ed.cliArgs,
		EnvOverrides:   ed.execCfg.EnvOverrides,
		// Fix C: auto-set the delegate's own configured model on the external
		// CLI invocation. Each driver's buildArgs guards so an unmapped/empty
		// model string is simply omitted rather than passed as garbage.
		Model: ed.agentModel,
	}

	var (
		evCh <-chan runner.RunEvent
		err  error
	)
	if resume {
		evCh, err = driver.Resume(runCtx, ed.runID, task)
		if err != nil {
			// FR-043: a delivery that cannot reach the native conversation —
			// no captured conversation id, a rejected/crashed resume — is a
			// VISIBLE failure, never a silent fresh conversation. The driver's
			// own Resume refusal is the truthful cause and is surfaced here
			// unchanged (BDD-05.6).
			return nil, fmt.Errorf("external-cli dispatch: resume native conversation (%s): %w", cli, err)
		}
	} else {
		evCh, err = driver.Run(runCtx, runOpts)
		if err != nil {
			return nil, fmt.Errorf("external-cli dispatch: driver start (%s): %w", cli, err)
		}
	}

	slog.Info("external-cli dispatch: run started",
		"run_id", ed.runID, "cli", cli, "work_dir", resolvedWorkDir,
		"timeout_s", ed.timeoutSecs, "max_turns", ed.maxTurns, "agent_id", ed.childTS.agentID)

	// 5. Route events through the consent dispatcher and drain into the transcript.
	//    ConsentDispatcher answers permission-requests (Decide) and forwards every
	//    event to `out` so we can record it. It runs until evCh closes or ctx ends;
	//    we close `out` on its exit so the drain loop below terminates cleanly
	//    (ConsentDispatcher does not own `out`'s lifetime).
	out := make(chan runner.RunEvent, 64)
	go func() {
		defer close(out)
		runner.ConsentDispatcher(runCtx, evCh, driver, ed.runID, ed.childTS.transcriptSessionID, consent, out)
	}()

	result := drainExternalRun(runCtx, ed.al, ed.childTS, ed.runID, cli, out)

	// N2 process-exit barrier: wait for the driver's OWN event stream to CLOSE.
	// Each driver's parser goroutine closes that channel only after the child
	// process has actually exited and its run state has been reset (e.g.
	// driver_claude.go::Run's deferred d.eventCh = nil / close(ch)). By this
	// point ConsentDispatcher has stopped reading evCh (it returns on ctx.Done
	// or evCh close, and drainExternalRun returning means `out` is closed), so
	// this drain takes evCh to closure — bounded by process exit plus the
	// existing group-cancel grace. It guarantees a continuation that follows can
	// call Resume without hitting "Run called while a run is already active",
	// and the deferred finishExternalRun / releaseWorkspaceLock below release
	// the driver and the workspace lock only once the process is truly gone.
	for range evCh {
	}
	return result, result.Err
}

// acquireRunSlot takes the workspace run lock cancel-aware and re-checks the
// run context right after (layers 2 and 3 of the BLOCK-finding fix): a run that
// got its cancel signal while queued, or in the instant between the acquire's
// own check and the token arriving, never starts. On cancellation it returns
// the canceled ToolResult and its error and no lock is held.
func acquireRunSlot(runCtx context.Context, cli, workDir string) (release func(), canceled *tools.ToolResult, err error) {
	releaseWorkspaceLock, acquired := acquireWorkspaceRunLockCtx(runCtx, workDir)
	if !acquired {
		cancelErr := fmt.Errorf(
			"external-cli dispatch: canceled while waiting for the workspace lock: %w",
			runCtx.Err(),
		)
		return nil, &tools.ToolResult{
			Err: cancelErr,
			ForLLM: fmt.Sprintf(
				"External CLI run (%s) canceled while waiting for the workspace lock: %v",
				cli,
				cancelErr,
			),
		}, cancelErr
	}
	if runCtx.Err() != nil {
		releaseWorkspaceLock()
		cancelErr := fmt.Errorf("external-cli dispatch: canceled before starting: %w", runCtx.Err())
		return nil, &tools.ToolResult{
			Err:    cancelErr,
			ForLLM: fmt.Sprintf("External CLI run (%s) canceled before starting: %v", cli, cancelErr),
		}, cancelErr
	}
	return releaseWorkspaceLock, nil, nil
}

// prepareRunOptions derives the run limits, model, scrubbed environment, and configured CLI arguments.
func (ed *runExternalCLISubTurnState) prepareRunOptions() {
	// 4. Bound the run: a per-run timeout + turn cap (FR-5.4).
	ed.timeoutSecs = int(ed.timeout.Seconds())
	if ed.timeoutSecs <= 0 {
		ed.timeoutSecs = int(defaultSubTurnTimeout.Seconds())
	}
	// Turn cap = the agent's effective tool-iteration limit (#904 D4/D14),
	// already resolved onto MaxIterations by NewAgentInstance. An instance
	// built without that constructor (MaxIterations <= 0) resolves through
	// the same resolver from the live config, WITH the agent's own stored
	// value when its record is in the live roster (so an own lower value
	// still applies) — never a separate literal.
	ed.maxTurns = ed.agent.MaxIterations
	if ed.maxTurns <= 0 {
		ed.maxTurns = resolveExternalMaxTurns(ed.al.GetConfig(), ed.agent.ID)
	}

	// FIX 5: hoist the repeated strings.TrimSpace(agent.Model) computation
	// (previously done independently for the transcript model stamp below
	// and again for RunOptions.Model further down) into a single local.
	ed.agentModel = strings.TrimSpace(ed.agent.Model)

	// Phase 1B FR-013: attribute the external-CLI sub-turn's transcript
	// output to the agent's configured model. The external CLI runs its
	// own LLM, but we record what model the agent would have used in the
	// non-CLI path — consistent with how chat turns are attributed.
	ed.childTS.setLastProducedModel(ed.agentModel)

	// SECURITY (Spec-4 FR-5.3 / SEC-23): the spawned external CLI must NOT inherit
	// the full gateway environment — that would leak OMNIPUS_MASTER_KEY (and every
	// other gateway secret) into a third-party binary. ScrubGatewayEnvForRunner
	// returns os.Environ() filtered through the generic child allowlist UNIONED with
	// the narrow runner-credential allowlist (the model-provider API keys the CLI
	// legitimately needs to authenticate). Passing it as RunOptions.Env makes the
	// driver use it as the COMPLETE child env (buildChildEnv) — no os.Environ()
	// fallback, so the master key never reaches the child.
	ed.childEnv = sandbox.ScrubGatewayEnvForRunner()

	// FR-5.3 egress allowlist: inject HTTP_PROXY/HTTPS_PROXY pointing at the
	// runner egress proxy so the CLI's HTTP/HTTPS traffic is forced through a
	// loopback proxy with SSRF internal-CIDR blocking (prevents the CLI from
	// reaching internal services / cloud metadata). Per ADR-019 FR-5.3 there is
	// NO new confiner — the CLI self-sandboxes; Omnipus controls egress via
	// proxy injection. Graceful degradation: an empty address (proxy could not
	// start) means no injection — the run proceeds under the CLI's own sandbox +
	// the workspace FS boundary.
	ed.childEnv = injectRunnerEgressProxy(ed.childEnv, ed.al.runnerEgressProxyAddr())

	// MAJ-5: consume the agent's ExecutorConfig (cli_path / cli_args /
	// env_overrides) when spawning the external CLI. cli_path overrides the
	// driver's default binary (else $PATH); cli_args is tokenised into argv
	// (execve, no shell — warn-not-reject on shell-metacharacters); env_overrides
	// merge into the scrubbed child env (OMNIPUS_* keys are dropped by the driver
	// env builder, so the master key / agent-identity vars stay protected).
	ed.execCfg = ed.agent.Subagents.Executor
	ed.cliArgs = runner.ParseCLIArgs(ed.execCfg.CLIArgs, ed.runID)
}

// drainExternalRun consumes the runner's event stream, mirrors each event into the
// sub-agent session transcript, and aggregates the run's textual output into a
// tools.ToolResult. It returns when the stream closes (run end/error) or ctx ends.
//
// External-CLI permission requests are auto-approved unconditionally (issue
// #488, see policyApproverConsent), so there is no longer a per-run deny path
// to consult here — a run only ends in failure via a fatal EventKindError or
// the run context ending.
func drainExternalRun(
	ctx context.Context,
	al *AgentLoop,
	childTS *turnState,
	runID, cli string,
	out <-chan runner.RunEvent,
) *tools.ToolResult {
	var sb strings.Builder
	var runErr error
	ended := false
	// unpairedStartID is the generated id of the latest tool call the runner
	// emitted without a CallID (#492), consumed by the next id-less result.
	var unpairedStartID string

	for {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			goto done
		case ev, ok := <-out:
			if !ok {
				// SELECT-RACE FIX (found while verifying the BLOCK fix above,
				// via TestStress_ExternalCLI_ConcurrentSpawnAndCancel):
				// `out` closing and ctx ending are not independent events for
				// an external-cli run — a cancel fires runCtx.Done(), which is
				// exactly what makes the driver's own event channel close
				// (blockingExternalDriver/the real CLI drivers all exit their
				// producer goroutine on <-ctx.Done()), which is what makes
				// ConsentDispatcher return and `out` close via its `defer
				// close(out)`. When BOTH the ctx.Done() case above and this
				// case become ready at effectively the same instant, Go's
				// `select` picks between them uniformly at random — so a
				// GENUINELY canceled run could still take this branch instead
				// of the `<-ctx.Done()` one above, and without this check
				// would fall through to "completed with no textual output"
				// with Err==nil, silently misreporting a canceled run as a
				// normal completion in the RETURNED ToolResult (the process
				// itself was still correctly killed either way — only the
				// caller-visible result was wrong). Checking ctx.Err() here
				// closes that gap regardless of which case the select
				// happened to take.
				if ctx.Err() != nil {
					runErr = ctx.Err()
				}
				goto done
			}
			switch ev.Kind {
			case runner.EventKindStart:
				// FR-5.6 / N3: the run's first event pins the external CLI version.
				// Log it so the run log records which stream schema the run used;
				// an unknown version (graceful-degradation) is surfaced at WARN.
				if ev.Start != nil {
					if ev.Start.VersionKnown {
						slog.Info("external-cli dispatch: run started",
							"run_id", runID, "cli", ev.Start.CLI, "version", ev.Start.Version)
					} else {
						slog.Warn(
							"external-cli dispatch: run started with unknown/unpinned CLI version — graceful degradation",
							"run_id",
							runID,
							"cli",
							ev.Start.CLI,
							"version",
							ev.Start.Version,
						)
					}
				} else {
					slog.Info("external-cli dispatch: run started", "run_id", runID, "cli", cli)
				}
			case runner.EventKindOutput:
				if ev.Output != nil && ev.Output.Text != "" {
					sb.WriteString(ev.Output.Text)
					childTS.appendIntermediateAssistantTranscript(ev.Output.Text, transcriptModelFor(childTS.agent))
				}
			case runner.EventKindToolCall:
				if ev.ToolCall != nil {
					id := recordExternalToolCall(childTS, ev.ToolCall)
					if ev.ToolCall.CallID == "" {
						// The runner gave no call id: remember the generated one so a
						// later id-less result can still pair with this start live.
						unpairedStartID = id
					}
					emitExternalToolCallStart(al, childTS, id, ev.ToolCall)
				}
			case runner.EventKindDiff:
				if ev.Diff != nil {
					txt := filterExternalString(al, "", fmt.Sprintf("diff %s:\n%s", ev.Diff.Path, ev.Diff.Diff))
					childTS.appendIntermediateAssistantTranscript(txt, transcriptModelFor(childTS.agent))
				}
			case runner.EventKindPermissionRequest:
				// Already routed to consent by ConsentDispatcher; record for the
				// transcript so the SPA can show the pending/decided approval.
				if ev.PermissionRequest != nil {
					recordExternalPermission(al, childTS, ev.PermissionRequest)
				}
			case runner.EventKindToolResult:
				// Tool result completion from the external runner: mirror it into the
				// transcript so the run shows the tool's outcome.
				if ev.ToolResult != nil {
					// Filter ONCE (#1222): the same credential-filtered output
					// feeds the transcript write and the live event, so neither
					// sink can carry what the other redacts.
					filtered := *ev.ToolResult
					filtered.Output = filterExternalToolOutput(al, filtered.ToolName, filtered.Output)
					recordExternalToolResult(childTS, &filtered)
					emitExternalToolCallEnd(al, childTS, &filtered, &unpairedStartID)
				}
			case runner.EventKindEnd:
				ended = true
			case runner.EventKindError:
				if ev.Err != nil {
					// Wave 1 (ADR-051 §RD5 MAJ-005 / fail-closed sanitizer):
					// route runner-channel errors through SanitizeRunnerError
					// so raw stderr / CLI failure text does NOT reach the
					// assistant transcript. The transcript gets a generic
					// marker; the raw is logged + retained on the EventKindError
					// payload for the WS forwarder (where Verbose Chat
					// decides whether to show it).
					rawMsg := ev.Err.Message
					sanitized := SanitizeRunnerError(rawMsg)
					childTS.appendIntermediateAssistantTranscript(
						"[external-cli error] "+sanitized.AssistantText,
						transcriptModelFor(childTS.agent),
					)
					// Surface the generic EventKindError for the WS forwarder
					// — the assistant transcript gets sanitized text, but the
					// structured error frame still carries the code/retryable
					// bits the SPA needs.
					emitExternalCLIErrorEvent(al, childTS, ev.Err, sanitized)
					if ev.Err.Fatal {
						// ADR-051 B2 fix: runErr MUST carry the sanitized
						// generic message — it flows into ToolResult.ForLLM
						// (L535) and reaches the parent agent's tool-result
						// context. sanitized.LogText is raw stderr / provider
						// text and must NEVER cross that boundary. The raw
						// text is retained ONLY in the structured slog.Warn
						// below for operator triage via gateway.log.
						// curatedTurnError: the CLI's own output has already been
						// replaced by the plain message for its code, so a task
						// run may show this text as written (turnErrorUserText).
						runErr = &curatedTurnError{text: "external-cli run failed: " + sanitized.AssistantText}
						slog.Warn("external-cli dispatch: fatal runner error",
							"run_id", runID, "cli", cli,
							"assistant_text", sanitized.AssistantText,
							"raw_log_text", sanitized.LogText,
							"code", string(sanitized.Code),
							"retryable", sanitized.Retryable)
					}
				}
			}
		}
	}

done:
	output := strings.TrimSpace(sb.String())
	// Persist the aggregated final content as the assistant transcript entry so a
	// replay reconstructs the run's result (mirrors the native finalContent path).
	if output != "" {
		childTS.appendAssistantTranscript(output, transcriptModelFor(childTS.agent))
	}

	slog.Info("external-cli dispatch: run finished",
		"run_id", runID, "cli", cli, "ended", ended,
		"err", runErr, "output_len", len(output))

	if runErr != nil {
		return &tools.ToolResult{
			Err:    runErr,
			ForLLM: fmt.Sprintf("External CLI run (%s) failed: %v", cli, runErr),
		}
	}

	if output == "" {
		output = fmt.Sprintf("External CLI run (%s) completed with no textual output.", cli)
	}
	return &tools.ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

// recordExternalToolCall mirrors an external runner tool-call event into the
// sub-agent session transcript as a tool_call entry. It returns the call id
// the entry was recorded under (the runner's own, or a generated one) so the
// live tool_call_start event carries the SAME id.
func recordExternalToolCall(childTS *turnState, tc *runner.ToolCallEvent) string {
	id := tc.CallID
	if id == "" {
		id = fmt.Sprintf("ext-tool-%d", time.Now().UnixNano())
	}
	args := parseExternalToolInput(tc, id)
	// The external CLI emits the tool-call event when the call STARTS; Omnipus
	// consent is post-hoc and we never observe the call's own success/failure from
	// the stream. Recording "success" would assert an outcome we did not verify.
	// Use "completed" — the call was transcribed/observed, with no success claim
	// (review finding [MINOR]).
	childTS.appendToolCallTranscript(session.ToolCall{
		ID:         session.ToolCallID(id),
		Tool:       tc.ToolName,
		Status:     "completed",
		Parameters: args,
	})
	return id
}

// parseExternalToolInput decodes a runner tool-call's raw JSON input into an
// argument map; a non-object input yields nil (logged at debug).
func parseExternalToolInput(tc *runner.ToolCallEvent, id string) map[string]any {
	var args map[string]any
	if len(tc.ToolInput) > 0 {
		if uErr := json.Unmarshal(tc.ToolInput, &args); uErr != nil {
			slog.Debug("external-cli dispatch: tool-call input is not a JSON object",
				"tool", tc.ToolName, "call_id", id, "err", uErr)
		}
	}
	return args
}

// emitExternalToolCallStart broadcasts the live tool_call_start for an
// external-CLI child's tool call (#492), tied to the parent spawn call exactly
// as the native path does (ParentSpawnCallID), so the SPA's existing
// SubagentSpan.steps machinery renders it. It only adds a live broadcast
// alongside the transcript write in recordExternalToolCall; storage is
// unchanged.
func emitExternalToolCallStart(al *AgentLoop, childTS *turnState, callID string, tc *runner.ToolCallEvent) {
	if al == nil || childTS == nil {
		return
	}
	al.emitEvent(
		EventKindToolExecStart,
		childTS.eventMeta("drainExternalRun", "turn.tool.start"),
		ToolExecStartPayload{
			ToolCallID:        session.ToolCallID(callID),
			ChatID:            childTS.chatID,
			SessionID:         u9ToolExecSessionIDs(childTS),
			Tool:              tc.ToolName,
			Arguments:         cloneEventArguments(parseExternalToolInput(tc, callID)),
			ParentSpawnCallID: session.ToolCallID(childTS.parentSpawnCallID),
			AgentID:           childTS.resolveActiveAgentID(),
		},
	)
}

// emitExternalToolCallEnd broadcasts the live tool_call_result for an
// external-CLI child's tool result (#492); the counterpart of
// emitExternalToolCallStart, paired by the runner's CallID. When the runner
// sent no CallID the result cannot be paired by id: it reuses the start id of
// the latest id-less call (*unpairedStartID, consumed here) where one is
// recorded, and logs a warning either way.
func emitExternalToolCallEnd(al *AgentLoop, childTS *turnState, tr *runner.ToolResultEvent, unpairedStartID *string) {
	if al == nil || childTS == nil {
		return
	}
	id := tr.CallID
	if id == "" {
		if unpairedStartID != nil && *unpairedStartID != "" {
			id = *unpairedStartID
			*unpairedStartID = ""
			slog.Warn("external-cli dispatch: tool result has no call id; pairing with the latest id-less tool call",
				"tool", tr.ToolName, "paired_call_id", id)
		} else {
			id = fmt.Sprintf("ext-tool-result-%d", time.Now().UnixNano())
			slog.Warn("external-cli dispatch: tool result has no call id and no unpaired start; live result cannot pair",
				"tool", tr.ToolName, "call_id", id)
		}
	}
	al.emitEvent(
		EventKindToolExecEnd,
		childTS.eventMeta("drainExternalRun", "turn.tool.end"),
		ToolExecEndPayload{
			ToolCallID:        session.ToolCallID(id),
			ChatID:            childTS.chatID,
			SessionID:         u9ToolExecSessionIDs(childTS),
			Tool:              tr.ToolName,
			ForLLMLen:         len(tr.Output),
			IsError:           tr.IsError,
			Result:            string(tr.Output), // already credential-filtered by the caller (filterExternalToolOutput)
			ParentSpawnCallID: session.ToolCallID(childTS.parentSpawnCallID),
			AgentID:           childTS.resolveActiveAgentID(),
		},
	)
}

// filterExternalString gives external-CLI text the treatment the native path
// applies before it stores or broadcasts tool content: the prompt-guard
// sanitizer for untrusted tool names first (sanitizeUntrustedToolResult), then
// credential redaction — in that order, reusing the same guard and the config's
// own replacer. It honours tools.filter_sensitive_data (IsFilterSensitiveDataEnabled)
// but deliberately skips FilterSensitiveData's short-content fast path: applied
// per JSON string leaf that rule would let a short leaf carrying a short secret
// through where the native whole-content filter would catch it. toolName "" is
// "not a tool result" (diff, permission text) and skips the prompt guard.
func filterExternalString(al *AgentLoop, toolName, s string) string {
	if al == nil || s == "" {
		return s
	}
	if toolName != "" && al.promptGuard != nil && isUntrustedToolResult(toolName) {
		s = al.promptGuard.Sanitize(s, false)
	}
	if cfg := al.GetConfig(); cfg != nil && cfg.Tools.IsFilterSensitiveDataEnabled() {
		s = cfg.SensitiveDataReplacer().Replace(s)
	}
	return s
}

// filterExternalToolOutput returns an external tool result's output with every
// credential filtered out, for BOTH the transcript write and the live event
// (#1222). JSON output is decoded FIRST and each string leaf filtered, because
// a secret containing a quote or newline appears only in escaped form in the
// raw bytes and a raw-bytes replace would miss it; non-JSON output is filtered
// as plain text. Output that needs no change is returned byte-for-byte.
func filterExternalToolOutput(al *AgentLoop, toolName string, out []byte) []byte {
	if al == nil || len(out) == 0 {
		return out
	}
	if !json.Valid(out) {
		return []byte(filterExternalString(al, toolName, string(out)))
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// json.Valid said yes, so this is unreachable; fail closed to the
		// text filter rather than pass the bytes through unfiltered.
		return []byte(filterExternalString(al, toolName, string(out)))
	}
	changed := false
	var walk func(x any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			f := filterExternalString(al, toolName, t)
			if f != t {
				changed = true
			}
			return f
		case map[string]any:
			for k, e := range t {
				t[k] = walk(e)
			}
			return t
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
			return t
		default:
			return x
		}
	}
	v = walk(v)
	if !changed {
		return out
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("[FILTERED]") // cannot re-encode: never fall back to the unfiltered bytes
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// recordExternalPermission records a permission-request as a transcript line so the
// run's gated actions are visible in replay. The decision itself is routed by the
// ConsentDispatcher; this is observability only.
func recordExternalPermission(al *AgentLoop, childTS *turnState, pr *runner.PermissionRequestEvent) {
	childTS.appendIntermediateAssistantTranscript(
		filterExternalString(al, "", fmt.Sprintf("[external-cli permission] tool=%q: %s", pr.ToolName, pr.Description)),
		transcriptModelFor(childTS.agent))
}

// recordExternalToolResult mirrors a completed external tool result into the
// sub-agent session transcript. The paired tool-call event was already recorded
// (with Status="completed" + the call's Parameters) when the call started via
// recordExternalToolCall; this call updates that existing entry in-place with
// the result + final status rather than appending a second transcript line with
// the same tool-call ID (which previously produced duplicate tool_call entries
// on replay — review finding S1).
//
// If no matching pending entry is found (defensive: the start event was lost or
// this is a result without a prior call), it falls back to appending a new entry.
//
// The in-place update reads the transcript JSONL, rewrites the matching line,
// and writes the file back atomically under an advisory flock. This mirrors the
// read-modify-rewrite pattern used by UnifiedStore.MarkLastEntryTruncated.
// Because the store's AppendTranscript is guarded by an in-process mutex (not
// flock), a narrow in-process race with a concurrent append from a sibling
// sub-turn sharing this transcript session remains: in the worst case a
// sibling's appended line lands in the read→rewrite gap and is overwritten.
// That is accepted for this [MINOR] transcript-cleanliness fix; a structural
// fix would add an exported UpdateToolCallResult method on UnifiedStore.
func recordExternalToolResult(childTS *turnState, tr *runner.ToolResultEvent) {
	id := tr.CallID
	if id == "" {
		id = fmt.Sprintf("ext-tool-result-%d", time.Now().UnixNano())
	}
	status := "success"
	if tr.IsError {
		status = "error"
	}
	var result map[string]any
	if len(tr.Output) > 0 {
		if err := json.Unmarshal(tr.Output, &result); err != nil {
			// Not a JSON object — store as a raw string under "output".
			result = map[string]any{"output": string(tr.Output)}
		}
	}

	// S1: try to update the existing pending tool-call entry in-place. Only
	// attempt this when a transcript store + session are configured and the
	// turn has not been abandoned (appendToolCallTranscript applies the same
	// guards, so we mirror them here to avoid a pointless rewrite).
	if !childTS.abandoned.Load() &&
		childTS.transcriptStore != nil && childTS.transcriptSessionID != "" {
		if recordExternalToolResultUpdateInPlace(childTS, session.ToolCallID(id), status, result) {
			return
		}
	}

	// Defensive fallback: no matching pending entry found, or the in-place
	// rewrite failed. Append a new transcript line so the result is not lost.
	childTS.appendToolCallTranscript(session.ToolCall{
		ID:         session.ToolCallID(id),
		Tool:       tr.ToolName,
		Status:     status,
		Parameters: nil,
		Result:     result,
	})
}

// recordExternalToolResultUpdateInPlace finds the most recent transcript entry
// of type tool_call whose ToolCall.ID matches callID and Status is "completed"
// (the entry written by recordExternalToolCall when the call started), and
// updates that ToolCall in-place with the final status + result. It returns
// true when an entry was found and the on-disk rewrite succeeded, false
// otherwise (caller falls back to appending).
//
// The JSONL read-modify-rewrite itself lives in mutateToolCallInTranscript
// (approval_transcript.go) — shared with the approval gate's pending→denied
// settle, which needs the identical operation against a different expected
// status. Only the "which status do I overwrite, and what do I write" policy
// differs, and that is expressed by the two arguments below.
func recordExternalToolResultUpdateInPlace(
	childTS *turnState,
	callID session.ToolCallID,
	status string,
	result map[string]any,
) bool {
	return mutateToolCall(childTS, callID, "completed", func(tc *session.ToolCall) {
		tc.Status = status
		tc.Result = result
	})
}

// resolveExternalMaxTurns resolves agentID's effective tool-iteration limit
// from cfg: the global in force (cfg may be nil — the shipped default) and the
// agent's own value when cfg.Agents.List carries its record.
func resolveExternalMaxTurns(cfg *config.Config, agentID string) int {
	if cfg == nil {
		return config.ResolveMaxToolIterations(nil, nil).Effective
	}
	var own *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			own = &cfg.Agents.List[i]
			break
		}
	}
	return config.ResolveMaxToolIterations(&cfg.Agents.Defaults, own).Effective
}

// transcriptModelFor returns the model string to stamp on transcript entries
// produced by an external-CLI sub-turn. It mirrors the trim applied in
// setLastProducedModel so the variadic and the single-slot stamp agree
// (W4-9 — variadic surface is now USED, not just declared).
func transcriptModelFor(agent *AgentInstance) string {
	if agent == nil {
		return ""
	}
	return strings.TrimSpace(agent.Model)
}

// newExternalDriver is the driver factory used by the external command-line runner. It is a
// package var (not a direct call to runner.NewDriver) solely so in-package tests
// can inject a fake/stub ExternalAgentRunner and exercise the full dispatch flow
// (worktree → run → stream → consent → teardown) without a real external CLI on
// PATH. Production always uses runner.NewDriver.
var newExternalDriver = func(cli string, consent runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
	return runner.NewDriver(cli, consent)
}

// policyApproverConsent implements runner.ConsentHandler for external-CLI
// (subagent_3p) permission requests.
//
// AUTO-APPROVE, unconditional (operator decision, 2026-07-05, issue #488):
// this deliberately does NOT route through the gateway-wired PolicyApprover /
// human-in-the-loop WS approval modal that native ask-policy tools use
// (pkg/agent/loop.go's CheckGrantOrRequestApproval) — that path is untouched
// and still gates native agents exactly as before. External-CLI requests are
// different in kind, not just posture: consent.go's own package doc
// establishes that by the time a PermissionRequestEvent reaches Omnipus, the
// CLI has already started (or finished) the tool call — an ALLOW is a no-op
// and a DENY can only kill the whole run after the fact, never veto the call.
// Blocking on a human "Approve" click therefore added pure friction with zero
// preventive value, which is exactly the complaint that led to this change
// ("the agent must just run without any popup modal"). The run can still be
// stopped via the ordinary turn-cancellation control; there is no longer a
// per-tool-call deny path for external-CLI runs specifically.
type policyApproverConsent struct {
	agentID string
	runID   string
}

// RequestConsent implements runner.ConsentHandler: auto-approves every
// external-CLI permission request unconditionally (see the type doc above),
// logging for observability only — no approver is consulted and no WS
// approval frame is ever broadcast for this path.
func (c *policyApproverConsent) RequestConsent(_ context.Context, req runner.ConsentRequest) (bool, string) {
	slog.Info("runner/consent: auto-approved (external-cli, post-hoc observability only, issue #488)",
		"tool", req.ToolName, "agent_id", c.agentID, "run_id", c.runID)
	return true, ""
}

// compile-time interface assertion
var _ runner.ConsentHandler = (*policyApproverConsent)(nil)

// SanitizedRunnerError is the output of SanitizeRunnerError — the
// fail-closed separator between an external CLI's raw stderr text and
// anything that crosses to the assistant or to the operator-visible log.
// ADR-051 §RD5 MAJ-005: REST-executor (subagent_3p) CLI errors that cross
// to the SPA must be routed through the shared classifier; the runner
// channel's raw ev.Err.Message is the most direct path for that data to
// leak, and this sanitizer is the barrier.
type SanitizedRunnerError struct {
	// AssistantText is the user/assistant-facing copy. Generic over CLI
	// identity and stderr text — the assistant transcript gets this and
	// ONLY this. NEVER the raw stderr.
	AssistantText string

	// LogText is the operator-facing copy (raw + classifier code). Lands
	// in gateway.log only, never on the wire and never in the transcript.
	LogText string

	// Code is the LLMErrorCode the classifier mapped this error to. Empty
	// string when classification is unknown. The WS forwarder prefers this
	// already-computed code on the live error frame; rate_limited is
	// forwarded (a provider 429 is the user's only signal).
	Code LLMErrorCode

	// Retryable mirrors the LLMError's retryable bit — the WS forwarder
	// surfaces retry hints when true.
	Retryable bool
}

// SanitizeRunnerError is the fail-closed sanitizer between a runner's
// raw error text and anything that reaches the assistant or the wire.
// Maps the runner error through the shared translateLLMError classifier
// (using a synthetic ProviderError built from the message) and returns
// a SanitizedRunnerError with AssistantText generic over CLI identity.
//
// The function NEVER returns the raw stderr verbatim — AssistantText is
// always one of the userMessages entries, regardless of classification
// outcome. This is the load-bearing invariant: a failed CLI run cannot
// leak its raw stderr to the assistant transcript even when the
// classifier returns CodeUnknown.
func SanitizeRunnerError(rawMessage string) SanitizedRunnerError {
	if rawMessage == "" {
		rawMessage = "external CLI failed"
	}
	// Build a synthetic ProviderError carrying only the message; the
	// classifier falls through to substring matching (no status) — fine
	// for the common external-CLI errors (auth, content policy, generic
	// process failure). Status-less classification is intentional: the
	// runner's ev.Err has no HTTP context.
	pe := &ProviderError{
		Status: 0,
		Body:   rawMessage,
		Err:    nil,
	}
	llm := TranslateLLMError(pe, rawMessage)
	// The shared translator emits one of the userMessages entries; that
	// is always safe for the assistant to see. Force the empty/CodeUnknown
	// case onto the generic copy too — the load-bearing invariant.
	assistant := llm.Message
	if assistant == "" {
		assistant = "The external CLI failed. See gateway.log for details."
	}
	return SanitizedRunnerError{
		AssistantText: assistant,
		LogText:       rawMessage,
		Code:          llm.Code,
		Retryable:     llm.Retryable,
	}
}

// emitExternalCLIErrorEvent surfaces a generic EventKindError on the bus
// for the runner's raw error, carrying the structured classifier code
// (NOT the raw stderr in the assistant-facing Message — that lives in
// LogText for operator triage and is gated by Verbose Chat at the WS
// forwarder). The agent-loop appendErrorTranscript write choke point
// also runs through translateLLMError, so the transcript gets the same
// generic copy (NOT the raw CLI stderr).
func emitExternalCLIErrorEvent(
	al *AgentLoop,
	ts *turnState,
	runnerErr *runner.ErrorEvent,
	sanitized SanitizedRunnerError,
) {
	if al == nil || ts == nil {
		return
	}
	if runnerErr == nil {
		return
	}
	al.emitEvent(
		EventKindError,
		ts.eventMeta("runTurn", "turn.error"),
		ErrorPayload{
			Stage: "external_cli",
			// FIX 4: ChatID was never set here, unlike every other
			// ErrorPayload construction site (ts.opts.ChatID) — the WS
			// forwarder's matchesChatID gate (pkg/gateway/websocket.go)
			// never matches an empty ChatID, so this event was silently
			// dropped for every live subscriber. External-CLI errors
			// (claude-code/codex/opencode sub-turns) were invisible live,
			// appearing only after a page reload replayed the transcript.
			ChatID:    ts.opts.ChatID,
			SessionID: string(ts.routingSessionID),
			// FIX 3: SanitizeRunnerError already computed the classifier
			// code (sanitized.Code) alongside sanitized.AssistantText —
			// thread it through so the WS forwarder (FIX 2) can use the
			// already-computed code/message pair instead of re-translating.
			Code:    string(sanitized.Code),
			Message: sanitized.AssistantText,
		},
	)
	// Mirror to the JSONL transcript via the write choke point. The raw
	// runnerErr.Message stays in sanitized.LogText / gateway.log; the
	// transcript gets the generic copy.
	ts.appendClassifiedError(EventKindError.String(), "external_cli", LLMError{
		Code:      sanitized.Code,
		Message:   sanitized.AssistantText,
		Retryable: isRetryable(sanitized.Code),
	})
}
