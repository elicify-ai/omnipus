// shell_bg.go: Background dispatch, status poll and read of a long-running shell command.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime/debug"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// --- background execution ----------------------------------------------------

// sandboxLimitsEnv builds the environment for a background session on the
// non-god-mode path. Starts from sandbox.ScrubGatewayEnv() and layers the
// Limits-derived injections (HTTP_PROXY, npm_config_cache) on top.
func sandboxLimitsEnv(lim sandbox.Limits) []string {
	scrubbed := sandbox.ScrubGatewayEnv()
	if lim.EgressProxyAddr != "" {
		proxyURL := "http://" + lim.EgressProxyAddr
		scrubbed = append(scrubbed,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"NO_PROXY=127.0.0.1,localhost,::1",
			"no_proxy=127.0.0.1,localhost,::1",
		)
	}
	if lim.WorkspaceDir != "" {
		scrubbed = append(scrubbed, "npm_config_cache="+lim.WorkspaceDir+"/.npm-cache")
	}
	return scrubbed
}

// runBackground starts command detached, tracks it as a ProcessSession
// (stamped with OwnerSessionID per FR-B10 so a session-level cancel can find
// it — see pkg/agent/cancel.go's CancelHooks.KillBackgroundSessions /
// SessionManager.KillAllForSession), and returns immediately with a
// session_id. ownerSessionID is whatever the caller's context carries as
// ToolTranscriptSessionID(ctx) (see executeRun above) — under ADR-057
// (FR-027) that is the CHILD's own distinct session id when this call
// happens inside a delegated sub-turn, not the root chat session's id it
// may previously have shared; a session-level cancel that must reach this
// process therefore cascades over the resolved descendant set via
// SessionManager.KillAllForSessions rather than a single exact match. The
// completion goroutine below fires cb exactly once — on natural completion,
// failure, timeout, or explicit kill (FR-B9) — via whichever ToolResult best
// describes the final state. baseDir is the same turn base directory
// executeRun computed; the completion goroutine needs it for the D-14
// post-command symlink sweep it runs before delivering the result.
func (t *ExecTool) runBackground(
	ctx context.Context,
	command, cwd, baseDir string,
	timeoutSeconds int32,
	lim sandbox.Limits,
	ownerSessionID string,
	cb AsyncCallback,
) *ToolResult {
	started := time.Now()
	sessionID := generateSessionID()
	session := &ProcessSession{
		ID:             sessionID,
		Command:        command,
		Background:     true,
		StartTime:      started.Unix(),
		Status:         StatusRunning,
		OwnerSessionID: ownerSessionID,
	}

	argv := buildShellArgv(command)
	cmd := exec.Command(argv[0], argv[1:]...) // gosec rationale (out of gosec scope; kept as documentation): command is agent-supplied by design; guarded above
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Setpgid baseline (Setpgid:true; no-op on Windows). sandbox.ApplyChildHardening
	// (below) safely EXTENDS this SysProcAttr (adds Pdeathsig) rather than
	// clobbering it — see hardened_exec_linux.go's applyPlatformHardening,
	// which only sets fields on an already-non-nil SysProcAttr.
	prepareCommandForTermination(cmd)

	if t.godMode {
		cmd.Env = scrubbedEnv(os.Environ())
	} else {
		cmd.Env = sandboxLimitsEnv(lim)
		if err := sandbox.ApplyChildHardening(cmd, lim); err != nil {
			return ErrorResult(fmt.Sprintf("sandbox hardening failed: %v", err))
		}
	}

	stdoutReader, err := cmd.StdoutPipe()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create stdout pipe: %v", err))
	}
	stderrReader, err := cmd.StderrPipe()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create stderr pipe: %v", err))
	}

	session.outputBuffer = &bytes.Buffer{}

	var startErr error
	if t.godMode {
		startErr = cmd.Start()
	} else {
		startErr = sandbox.StartLocked(cmd)
	}
	if startErr != nil {
		return ErrorResult(fmt.Sprintf("failed to start command: %v", startErr))
	}

	session.PID = cmd.Process.Pid
	// FR-011: report the spawned background child to the scheduled-run
	// process tracker (if the caller installed one) so it can be
	// force-terminated on run completion. No-op when no tracker is on ctx.
	TrackProcess(ctx, session.PID)
	t.sessionManager.Add(session)

	pipeReadFn := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				session.mu.Lock()
				if session.outputBuffer.Len() >= maxOutputBufferSize {
					if !session.outputTruncated {
						session.outputBuffer.WriteString(outputTruncateMarker)
						session.outputTruncated = true
					}
				} else {
					session.outputBuffer.Write(buf[:n])
				}
				session.mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}

	var pipeWG sync.WaitGroup
	pipeWG.Add(2)
	go func() { defer pipeWG.Done(); pipeReadFn(stdoutReader) }()
	go func() { defer pipeWG.Done(); pipeReadFn(stderrReader) }()

	// naturalCompletionCh is closed by the completion goroutine below the
	// instant it has recorded a terminal status for the session (whatever
	// that status turns out to be), so the timeout-guard goroutine can bail
	// out instead of firing its stale timer against an already-finished
	// session. Without this, S10 (UAT full-tool-catalog batch1, 2026-09-02,
	// §2.2): the timeout timer below is scheduled once, at session start, for
	// timeoutSeconds regardless of how quickly the command actually
	// finishes — a command that completes in 2 seconds under a 300s default
	// timeout leaves this goroutine asleep until t=300s and STILL fires then.
	// KillAndRelabel's statusPriority "upgrade" rule (session.go) — meant for
	// a genuine near-simultaneous race between two callers relabeling the
	// SAME dying process — cannot tell that race apart from this stale-timer
	// case, so it happily overwrites the correct, minutes-old StatusDone
	// (priority 1) with StatusTimeout (priority 2): a poll issued before the
	// timer fires correctly reports "done", and a read issued moments after
	// it fires reports "timeout" with no output (the field is empty in the
	// upgrade branch — only Status is rewritten, and by then the buffer may
	// already have been drained) — self-contradictory to a caller and
	// factually wrong, since the command was never killed for exceeding its
	// budget. Closing this channel as soon as the process's real fate is
	// known removes the false-positive path entirely while leaving the
	// genuine boundary race (natural exit and timer firing within the same
	// instant) exactly as before: KillAndRelabel's priority tie-breaking
	// still decides that case correctly.
	naturalCompletionCh := make(chan struct{})

	// FR-B3: enforce timeout_seconds identically for background as for
	// foreground. Fires session.KillAndRelabel(StatusTimeout) (the same kill
	// primitive SessionManager.KillAllForSession uses, atomically relabeled
	// to "timeout" in ONE lock acquisition — see KillAndRelabel's doc
	// comment) so pollers/AsyncNotifier can distinguish it from an explicit
	// kill or a natural exit.
	if timeoutSeconds > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
			defer timer.Stop()
			select {
			case <-naturalCompletionCh:
				// The session already reached a terminal status (done, killed,
				// or canceled) before the timer fired — nothing to enforce.
				return
			case <-timer.C:
			}
			if killErr := session.KillAndRelabel(StatusTimeout); killErr != nil {
				if !errors.Is(killErr, ErrSessionDone) {
					slog.Warn("bash: background timeout kill failed",
						"session_id", sessionID, "pid", session.PID, "error", killErr)
					if t.killAuditFn != nil {
						t.killAuditFn(session.PID, killErr, "bash_background_timeout")
					}
				}
			}
		}()
	}

	// Completion goroutine: the single place that knows the process has
	// actually exited and pipes are drained. It claims "done" only if no
	// other path (explicit action=kill, timeout_seconds guard, or a
	// RequestCancel kill cascade — see KillAndRelabel) already claimed a
	// terminal status — see KillAndRelabel's own atomic running-check for why
	// this is race free (MIN-002). This is also FR-B9's sole notification
	// point: it fires cb exactly once, regardless of which of the four
	// outcomes occurred (done, killed, timeout, canceled —
	// backgroundCompletionResult's switch below must handle all four
	// explicitly; a missed case previously let "canceled" fall through to a
	// misleading generic-failure summary, see that function's doc comment).
	go func() {
		pipeWG.Wait()
		waitErr := cmd.Wait()

		session.mu.Lock()
		if session.Status == StatusRunning {
			if cmd.ProcessState != nil {
				session.ExitCode = cmd.ProcessState.ExitCode()
			} else {
				if waitErr != nil {
					logger.WarnCF("bash", "background cmd.Wait returned error with nil ProcessState",
						map[string]any{"session_id": sessionID, "error": waitErr.Error()})
				}
				session.ExitCode = -1
			}
			session.Status = StatusDone
		}
		finalStatus := session.Status
		finalExitCode := session.ExitCode
		// Peek (non-destructive) at whatever output has accumulated so the
		// async notification has content to show — do NOT Reset() here: an
		// explicit action=read call after completion must still be able to
		// drain this same buffered output (Reset is session.Read()'s job).
		outputSoFar := session.outputBuffer.String()
		session.mu.Unlock()

		// Stop the timeout guard now that the session's real fate is settled
		// — see naturalCompletionCh's doc comment above. Safe to close
		// unconditionally even when no timeout goroutine was started
		// (timeoutSeconds <= 0): nothing ever selects on it in that case.
		close(naturalCompletionCh)

		// D-14 background coverage (Claude review 2026-09-14): run the
		// post-command escaping-symlink sweep HERE, at the completion
		// goroutine — the one place that knows the process has exited and
		// its pipes are drained, so the walk cannot race a command that is
		// still writing. Before this, only the foreground path swept, so a
		// background run could plant the very symlink escape the sweep
		// exists to name and never be reported. The sweep is report-only
		// (see sweepAfterRun) and shares its skip conditions (god mode,
		// unrestricted tool); it runs even when cb is nil so the operator's
		// audit entry is still written, and its notice is folded into the
		// completion result the callback delivers. sweepAfterRun reads only
		// context VALUES off ctx (agent/workspace identity for the policy
		// lookup and the audit entry), so a turn that has since ended does
		// not silence it.
		completion := backgroundCompletionResult(sessionID, finalStatus, finalExitCode, outputSoFar)
		completion = t.sweepBackgroundCompletion(ctx, command, cwd, baseDir, sessionID, started, completion)
		if cb != nil {
			cb(context.Background(), completion)
		}
	}()

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    string(StatusRunning),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash start response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s started", sessionID),
		IsError: marshalErr != nil,
	}
}

// sweepBackgroundCompletion runs the D-14 post-command sweep for a finished
// background session, and cannot take the gateway down with it.
//
// WHY THE RECOVER (silent-failure review 2026-09-14, F9). The foreground sweep
// runs inside a tool call, under the agent loop's own protection. This one
// runs in runBackground's bare completion goroutine, after the turn that
// started the command has usually ended — nothing above it catches a panic,
// so a panic in the sweep or in the policy lookup it makes would crash the
// whole gateway process, recorded only on stderr (gateway_panic.log covers
// startup only). A crashed check must not cost the operator every other
// session, and must not vanish either, so a panic here is turned into:
//
//   - the completion result STILL delivered through the callback, with a
//     plain notice that this run was NOT checked (the agent must not read a
//     missing sweep report as a clean one);
//   - one ERROR log carrying the panic value and the stack;
//   - an audit warning ("escaping_symlink_sweep_failed"), shaped like the
//     sweep's own "escaping_symlinks" warning, so an operator reviewing audit
//     sees the gap where a finding would have been.
func (t *ExecTool) sweepBackgroundCompletion(
	ctx context.Context,
	command, cwd, baseDir, sessionID string,
	started time.Time,
	completion *ToolResult,
) (out *ToolResult) {
	sweep := t.sweepAfterRun
	if t.backgroundSweepFn != nil {
		sweep = t.backgroundSweepFn
	}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		panicText := fmt.Sprint(r)
		slog.Error("bash: post-command symlink sweep panicked after a background command finished; delivering the result without the check",
			"agent_id", ToolAgentID(ctx),
			"session_id", sessionID,
			"panic", panicText,
			"stack", string(debug.Stack()))
		t.auditBackgroundSweepFailure(ctx, command, cwd, sessionID, panicText)
		out = completion
		if completion == nil {
			return
		}
		notice := backgroundSweepFailedNotice()
		completion.ForLLM = completion.ContentForLLM() + notice
		if completion.ForUser != "" {
			completion.ForUser += notice
		}
	}()
	return sweep(ctx, command, cwd, baseDir, started, completion)
}

// backgroundSweepFailedNotice is appended to a background completion whose
// post-command sweep crashed. It deliberately carries no panic text: that is
// internal detail for the operator log, not for the agent-facing result.
func backgroundSweepFailedNotice() string {
	return "\n\n[SAFETY GUARD: the post-command symlink check FAILED to run after this command finished, " +
		"so this run was NOT checked for symlinks pointing outside the workspace. " +
		"Do not treat the absence of a finding as a clean result. The failure has been recorded for the operator.]"
}

// auditBackgroundSweepFailure writes the audit warning for a crashed
// background sweep. The audit write is itself guarded: sweepAfterRun writes
// audit too, so a panicking audit logger is one plausible cause of the panic
// being reported, and re-raising it here would crash the gateway after all.
func (t *ExecTool) auditBackgroundSweepFailure(ctx context.Context, command, cwd, sessionID, panicText string) {
	if t.auditLogger == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("bash: writing the audit entry for a failed post-command symlink sweep panicked as well",
				"agent_id", ToolAgentID(ctx),
				"session_id", sessionID,
				"panic", fmt.Sprint(r))
		}
	}()
	// Same shape as sweepAfterRun's "escaping_symlinks" entry: the command
	// was allowed and ran; this is a warning attached to it.
	if err := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  ToolAgentID(ctx),
		Tool:     t.Name(),
		Command:  command,
		Details: map[string]any{
			"cwd":        cwd,
			"session_id": sessionID,
			"warning":    "escaping_symlink_sweep_failed",
			"error":      panicText,
			"reason": "the post-command check for symlinks pointing outside the workspace crashed after this background command finished; " +
				"this run was NOT checked — operator review",
		},
	}); err != nil {
		slog.Warn("bash: audit write failed", "agent_id", ToolAgentID(ctx), "error", err)
	}
}

// backgroundCompletionResult builds the ToolResult passed to cb when a
// background session reaches a terminal state (FR-B9). There are exactly
// four possible terminal outcomes for a background bash session — a natural
// exit (StatusDone), an explicit action=kill (StatusKilled), the
// timeout_seconds guard firing (StatusTimeout), and a RequestCancel kill
// cascade (StatusCanceled, see SessionManager.KillAllForSession) — and this
// switch MUST handle all four explicitly. Before this switch added the
// StatusCanceled case, a canceled background job fell through to the
// default branch and was misreported to the LLM/user as a generic failure
// ("finished (exit code N)", IsError:true) instead of an intentional,
// user-initiated cancellation.
func backgroundCompletionResult(sessionID string, status SessionStatus, exitCode int, output string) *ToolResult {
	if output == "" {
		output = "(no output)"
	}
	var summary string
	isError := true
	switch status {
	case StatusTimeout:
		summary = fmt.Sprintf("Background session %s timed out.\n\n%s", sessionID, output)
	case StatusKilled:
		summary = fmt.Sprintf("Background session %s was killed.\n\n%s", sessionID, output)
	case StatusCanceled:
		summary = fmt.Sprintf("Background session %s was canceled.\n\n%s", sessionID, output)
		isError = false
	case StatusDone:
		summary = fmt.Sprintf("Background session %s finished (exit code %d).\n\n%s", sessionID, exitCode, output)
		isError = false
	default:
		// Unexpected status reaching this switch (e.g. StatusRunning/
		// StatusExited, which should never be the FINAL status a completion
		// goroutine observes) — keep the same generic-failure fallback the
		// pre-existing default case used, so an unforeseen future status
		// still produces a safe (loud, not silently-successful) result.
		summary = fmt.Sprintf("Background session %s finished (exit code %d).\n\n%s", sessionID, exitCode, output)
	}
	return &ToolResult{
		ForLLM:  summary,
		ForUser: summary,
		IsError: isError,
	}
}

// --- session actions (poll / read / kill) ------------------------------------

// getSessionArg resolves the action=poll/read/kill session_id argument
// through SessionManager.GetOwned (M5 fix, live UAT 2026-07-31): the
// caller's own ToolTranscriptSessionID(ctx) must match the session's
// OwnerSessionID, or the lookup is denied with the same "session not found"
// message a genuinely-missing session_id would produce — see GetOwned's own
// doc comment for why this must not be a distinguishable "forbidden" error.
// Before this fix, ANY chat/transcript session could poll/read/kill ANY
// OTHER session's background bash job process-wide just by knowing its
// short session_id, with zero ownership check.
func (t *ExecTool) getSessionArg(ctx context.Context, args map[string]any) (*ProcessSession, string, *ToolResult) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, "", ErrorResult("session_id is required")
	}
	session, err := t.sessionManager.GetOwned(sessionID, ToolTranscriptSessionID(ctx))
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, "", ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return nil, "", ErrorResult(err.Error())
	}
	return session, sessionID, nil
}

func (t *ExecTool) executePoll(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    session.GetStatus(),
		ExitCode:  session.GetExitCode(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash poll response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executeRead(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}

	output := session.Read()

	resp := ExecResponse{
		SessionID: sessionID,
		Output:    output,
		Status:    session.GetStatus(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash read response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: marshalErr != nil,
	}
}

// executeKill terminates a running background session. MIN-002: if the
// process already exited naturally (a race between an earlier poll and this
// kill call), it reports the REAL final status instead of a false "killed" —
// session.IsDone() is checked BEFORE any kill attempt, and
// session.KillAndRelabel itself atomically no-ops (ErrSessionDone) if it lost
// that race.
func (t *ExecTool) executeKill(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}

	if session.IsDone() {
		return t.sessionActionResult(sessionID, session)
	}

	// KillAndRelabel kills AND relabels to StatusKilled atomically under one
	// lock acquisition — no separate SetStatus call, no gap for a concurrent
	// poller to observe the generic "done" before the more specific "killed"
	// label lands.
	if err := session.KillAndRelabel(StatusKilled); err != nil {
		if errors.Is(err, ErrSessionDone) {
			// Raced: the process exited naturally between our IsDone() check
			// and this Kill() call. Report the real final status, not an error.
			return t.sessionActionResult(sessionID, session)
		}
		if t.killAuditFn != nil {
			t.killAuditFn(session.PID, err, "bash_kill_action")
		}
		return ErrorResult(fmt.Sprintf("failed to kill session: %v", err))
	}

	return t.sessionActionResult(sessionID, session)
}

func (t *ExecTool) sessionActionResult(sessionID string, session *ProcessSession) *ToolResult {
	status := session.GetStatus()
	resp := ExecResponse{
		SessionID: sessionID,
		Status:    status,
		ExitCode:  session.GetExitCode(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash kill response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s %s", sessionID, status),
		IsError: marshalErr != nil,
	}
}
