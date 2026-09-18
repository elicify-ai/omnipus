// environment_setup_runner.go — the real-subprocess session machinery for the
// generic environment_setup tool (founder decision GENERIC INSTALL OPTION A,
// 2026-09-18). See environment_setup.go's header for the authority model and
// the ES-FR-01..04 references.
//
// REUSE CONTRACT: no second jobs engine. The install is a real child process
// tracked as a ProcessSession in the shared global SessionManager, so every
// existing termination path (explicit kill, timeout guard, RequestCancel
// cascade, shutdown reaper) reaches it with no new machinery, exactly like a
// bash background session.
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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// startPlan is everything the session machinery needs to start one install.
// area ownership transfers to the completion goroutine on a successful start:
// it terminates the target exactly once (Commit on exit 0 for shared scope,
// Abort otherwise; workspace-scope Abort is a storage-side no-op).
type startPlan struct {
	command        string
	purpose        string
	scope          string
	target         environmentSetupTarget
	area           EnvironmentSetupTarget
	cwd            string
	timeoutSeconds int32
	lim            sandbox.Limits
	env            []string
	ownerSessionID string
	agentID        string
	cb             AsyncCallback
}

// buildSetupLimits authors the setup child's sandbox posture with the same
// machinery bash uses: sandbox.ResolveLimits (timeout/memory/egress), then a
// per-turn kernel policy derived from an FSPolicy whose WorkDir is the
// authorized target work root. This makes that root's source files part of
// the own-tree read view; write is stripped again below except for the exact
// selected installation roots. God mode mirrors bash: no kernel policy — the
// same operator opt-out that governs that agent's bash children.
func (t *EnvironmentSetupTool) buildSetupLimits(ctx context.Context, area EnvironmentSetupTarget, targetRoot string, runtimeEnv environmentsetup.RuntimeEnv, timeoutSeconds int32) (sandbox.Limits, error) {
	prefix := area.Prefix()
	lim, err := sandbox.ResolveLimits(t.godMode, prefix, t.proxy, timeoutSeconds)
	if err != nil {
		return sandbox.Limits{}, err
	}
	lim.WorkspaceDir = prefix

	if t.godMode {
		return lim, nil
	}

	authored, err := fspolicy.EffectiveFSPolicyWithReadConfined(
		ctx, "", targetRoot, true, t.home, ToolAgentID(ctx), ToolWorkspaceID(ctx), false)
	if err != nil {
		return sandbox.Limits{}, fmt.Errorf("resolve setup filesystem policy: %w", err)
	}
	authored.AllowedRoots = writableRootsOutsidePrefix(area)

	policy, err := sandbox.KernelPolicyForTurn(authored)
	if err != nil {
		// Fail closed (mirrors turnKernelPolicy): falling back to the boot
		// profile would hand the child the WIDER posture.
		return sandbox.Limits{}, fmt.Errorf("derive setup kernel policy: %w", err)
	}
	// nil is legal: no boot-registered base means the boot profile governs,
	// the same documented fallback bash has.
	if policy != nil {
		// A setup child must not inherit ANY ordinary write grant from the
		// agent turn, including operator allowed_paths or mounted roots. Setup
		// re-adds only its selected prefix/cache/tmp below. /dev/null remains a
		// writable device so normal shell redirection continues to work.
		for i := range policy.FilesystemRules {
			if filepath.Clean(policy.FilesystemRules[i].Path) != "/dev/null" {
				policy.FilesystemRules[i].Access &^= sandbox.AccessWrite
			}
		}
		// The selected target may itself be a new shared generation below the
		// store. Re-add only its exact installation roots after the defensive
		// ancestor strip; no sibling generation regains write access.
		policy.FilesystemRules = append(policy.FilesystemRules,
			sandbox.PathRule{Path: filepath.Clean(area.Prefix()), Access: sandbox.AccessRead | sandbox.AccessWrite},
		)
		for _, root := range writableRootsOutsidePrefix(area) {
			policy.FilesystemRules = append(policy.FilesystemRules,
				sandbox.PathRule{Path: filepath.Clean(root), Access: sandbox.AccessRead | sandbox.AccessWrite})
		}
		policy.FilesystemRules = append(policy.FilesystemRules,
			sandbox.PathRule{Path: filepath.Clean(targetRoot), Access: sandbox.AccessRead | sandbox.AccessExecute},
		)
		for _, root := range runtimeEnv.ReadExec {
			policy.FilesystemRules = append(policy.FilesystemRules,
				sandbox.PathRule{Path: filepath.Clean(root), Access: sandbox.AccessRead | sandbox.AccessExecute})
		}
	}
	lim.KernelPolicy = policy
	return lim, nil
}

// writableRootsOutsidePrefix returns the target's writable roots that are not
// lexically inside the prefix. Those become FSPolicy.AllowedRoots (additional
// write grants); in-prefix ones are already covered by WorkDir.
func writableRootsOutsidePrefix(area EnvironmentSetupTarget) []string {
	prefix := area.Prefix()
	var out []string
	for _, root := range area.WritableRoots() {
		if root == "" || pathWithin(prefix, root) {
			continue
		}
		out = append(out, root)
	}
	return out
}

// pathWithin reports whether path is dir itself or inside dir (lexically).
func pathWithin(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// setupEnv builds the setup child's environment. Non-god-mode base is the
// same scrubbed gateway env bash background children get, plus the egress
// proxy injection when the limits carry one; god mode uses the scrubbed host
// environ. On top of either, the documented generic variables and write-
// bounding redirections point INSIDE the granted area:
//
//	OMNIPUS_ENV_PREFIX / _CACHE / _TMP — the documented generic variables
//	  (storage package owns the canonical constants; literals kept in
//	  lockstep until the direct import replaces them);
//	TMPDIR, npm_config_cache, PIP_CACHE_DIR — conventional cache/temp
//	  redirections. These are boundary configuration, NOT dependency
//	  knowledge: no package name, version or recipe is encoded, and a script
//	  that ignores them fails visibly if it writes outside the grant
//	  (ES-FR-03: do not disable the sandbox to make an install succeed).
func (t *EnvironmentSetupTool) setupEnv(lim sandbox.Limits, area EnvironmentSetupTarget, runtimeEnv environmentsetup.RuntimeEnv) []string {
	var env []string
	if t.godMode {
		env = scrubbedEnv(os.Environ())
	} else {
		env = sandbox.ScrubGatewayEnv()
	}
	if !t.godMode && lim.EgressProxyAddr != "" {
		proxyURL := "http://" + lim.EgressProxyAddr
		env = append(env,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"NO_PROXY=127.0.0.1,localhost,::1",
			"no_proxy=127.0.0.1,localhost,::1",
		)
	}
	prefix, cache, tmp := area.Prefix(), area.Cache(), area.Tmp()
	env = append(env,
		environmentSetupEnvVarPrefix+"="+prefix,
		environmentSetupEnvVarCache+"="+cache,
		environmentSetupEnvVarTmp+"="+tmp,
		"TMPDIR="+tmp,
		"npm_config_cache="+cache,
		"PIP_CACHE_DIR="+cache,
	)
	// Reuse contract: a later approved script must discover a helper an
	// earlier one installed — the prefix's conventional executable dirs join
	// PATH ahead of the scrubbed base. Same per-OS bin-dir names storage uses
	// ("bin" always; "Scripts" also on Windows) and the PATH separator rule,
	// kept in lockstep like the env-var literals above.
	binDirs := make([]string, 0, 2+len(runtimeEnv.BinDirs))
	if runtime.GOOS == "windows" {
		binDirs = append(binDirs, filepath.Join(prefix, "Scripts"))
	}
	binDirs = append(binDirs, filepath.Join(prefix, "bin"))
	// Existing runtime directories follow the selected destination. This lets
	// a new shared generation use helpers from prior published generations,
	// and lets a workspace install use its earlier environment, without any
	// package-specific knowledge.
	binDirs = append(binDirs, runtimeEnv.BinDirs...)
	existingPath := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			existingPath = strings.TrimPrefix(kv, "PATH=")
		}
	}
	env = append(env, "PATH="+strings.Join(binDirs, string(os.PathListSeparator))+
		string(os.PathListSeparator)+existingPath)
	return env
}

// startSession starts the real install subprocess as a background session,
// mirroring runBackground (shell_bg.go) step for step with the setup boundary
// in place of bash's workspace boundary and a publication step in the
// completion goroutine. plan.area ownership transfers to the completion
// goroutine.
func (t *EnvironmentSetupTool) startSession(ctx context.Context, plan startPlan) *ToolResult {
	command := plan.command
	started := time.Now()
	session := &ProcessSession{
		ID:             generateSessionID(),
		Command:        command,
		Background:     true,
		StartTime:      started.Unix(),
		Status:         StatusRunning,
		OwnerSessionID: plan.ownerSessionID,
	}

	argv := buildShellArgv(command)
	cmd := exec.Command(argv[0], argv[1:]...) // gosec rationale (out of gosec scope): the command is agent-supplied by design, approved via the environment_setup Ask policy, and confined by the setup kernel policy.
	if plan.cwd != "" {
		cmd.Dir = plan.cwd
	}

	// Setpgid baseline so the process-group kill reaches grandchildren —
	// cancellation terminates the child process group (ES-FR-03).
	prepareCommandForTermination(cmd)

	// The environment is fully composed in setupEnv for both modes (god mode
	// uses the scrubbed host environ and skips hardening, mirroring bash).
	cmd.Env = plan.env
	if !t.godMode {
		if err := sandbox.ApplyChildHardening(cmd, plan.lim); err != nil {
			return setupStartAbortResult(plan.area, fmt.Sprintf("sandbox hardening failed: %v", err))
		}
	}

	stdoutReader, err := cmd.StdoutPipe()
	if err != nil {
		return setupStartAbortResult(plan.area, fmt.Sprintf("failed to create stdout pipe: %v", err))
	}
	stderrReader, err := cmd.StderrPipe()
	if err != nil {
		return setupStartAbortResult(plan.area, fmt.Sprintf("failed to create stderr pipe: %v", err))
	}

	var startErr error
	if t.godMode {
		startErr = cmd.Start()
	} else {
		// CRIT-1: the per-turn kernel policy must reach the Linux child —
		// StartLocked applies only the boot-global domain on the spawning
		// thread, silently dropping the turn's confinement (darwin already
		// reached the child through ApplyChildHardening/ApplyToCmd above).
		startErr = sandbox.StartLockedWithPolicy(cmd, plan.lim.KernelPolicy)
	}
	if startErr != nil {
		return setupStartAbortResult(plan.area, fmt.Sprintf("failed to start command: %v", startErr))
	}
	if !t.godMode {
		if err := sandbox.ApplyChildPostStartHardening(cmd, plan.lim); err != nil {
			// The documented pre/post hardening contract is fail-closed. Use the
			// same process-group termination primitive as normal cancellation;
			// a direct kill is a last resort if grouping itself failed.
			if killErr := killProcessGroupFn(cmd.Process.Pid); killErr != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			return setupStartAbortResult(plan.area, fmt.Sprintf("sandbox post-start hardening failed: %v", err))
		}
	}

	session.PID = cmd.Process.Pid
	// FR-011: report the child to the scheduled-run process tracker (if the
	// caller installed one) so it can be force-terminated on run completion.
	TrackProcess(ctx, session.PID)
	t.sessionManager.Add(session)

	// Bounded output capture — the same 1MB buffer + truncation marker as
	// bash (maxOutputBufferSize/outputTruncateMarker in session.go).
	session.outputBuffer = &bytes.Buffer{}
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

	// naturalCompletionCh guards the timeout timer against the stale-timer
	// false positive (see runBackground's doc comment in shell_bg.go: a
	// session that finished minutes ago must not be relabeled "timeout" when
	// the timer finally fires).
	naturalCompletionCh := make(chan struct{})

	if plan.timeoutSeconds > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(plan.timeoutSeconds) * time.Second)
			defer timer.Stop()
			select {
			case <-naturalCompletionCh:
				return
			case <-timer.C:
			}
			if killErr := session.KillAndRelabel(StatusTimeout); killErr != nil && !errors.Is(killErr, ErrSessionDone) {
				slog.Warn("environment_setup: background timeout kill failed",
					"session_id", session.ID, "pid", session.PID, "error", killErr)
				if t.killAuditFn != nil {
					t.killAuditFn(session.PID, killErr, "environment_setup_background_timeout")
				}
			}
		}()
	}

	// Completion goroutine: the single place that knows the process has
	// exited and pipes are drained. Claims "done" only if no other path
	// (explicit kill, timeout, RequestCancel cascade) already claimed a
	// terminal status — same race-free relabel discipline as runBackground.
	go func() {
		pipeWG.Wait()
		waitErr := cmd.Wait()

		// Capture the natural exit WITHOUT claiming the terminal status: the
		// session stays "running" (truthfully) until publication completes —
		// "done" is written only in the persistence section below (MAJ-1).
		naturalExit := false
		session.mu.Lock()
		if session.Status == StatusRunning {
			naturalExit = true
			if cmd.ProcessState != nil {
				session.ExitCode = cmd.ProcessState.ExitCode()
			} else {
				if waitErr != nil {
					slog.Warn("environment_setup: cmd.Wait returned error with nil ProcessState",
						"session_id", session.ID, "error", waitErr.Error())
				}
				session.ExitCode = -1
			}
		}
		finalStatus := session.Status
		finalExitCode := session.ExitCode
		outputSoFar := session.outputBuffer.String()
		session.mu.Unlock()
		// The completion TEXT must describe a natural exit as "done" even
		// though the session field is still "running" until publication
		// settles (MAJ-1 ordering): effectiveStatus translates for the
		// completion result only — the session field itself is written in
		// the persistence section below.
		effectiveStatus := finalStatus
		if naturalExit && finalStatus == StatusRunning {
			effectiveStatus = StatusDone
		}

		close(naturalCompletionCh)

		// Terminate the target exactly once (storage lifecycle contract):
		// shared scope publishes on a clean natural exit, is unpublished
		// otherwise. publishTarget owns cleanup-attempt/reporting after a
		// failed one. The workspace-scope Abort is a documented storage-side
		// no-op, so the uniform Abort call on every non-published outcome is
		// safe.
		pub := t.publishTarget(plan, plan.area, naturalExit, finalExitCode)

		completion := backgroundCompletionResult(session.ID, effectiveStatus, finalExitCode, outputSoFar)
		// The command's OWN failure (nonzero natural exit) is an error
		// result: backgroundCompletionResult reports any natural exit as
		// "finished", but the environment_setup contract is exit 0 = command
		// success only — a nonzero exit must not fold into a clean result.
		if effectiveStatus == StatusDone && finalExitCode != 0 {
			completion.IsError = true
		}
		switch pub.outcome {
		case "commit_failed", "abort_failed":
			completion.IsError = true
		}
		notice := t.setupCompletionNotice(plan, pub)
		completion.ForLLM += notice
		if completion.ForUser != "" {
			completion.ForUser += notice
		}

		// MAJ-1: persist the publication outcome on the EXISTING session —
		// ordering and the publication-window terminal discipline live in
		// persistCompletionOutcome.
		t.persistCompletionOutcome(session, pub, notice, naturalExit, finalExitCode)

		// Publication audit trail: a shared install that exited 0 but failed
		// to publish must be VISIBLE as an error, not folded into exit 0.
		t.auditPublication(plan, pub)

		if cb := plan.cb; cb != nil {
			cb(context.Background(), completion)
		}
	}()

	resp := ExecResponse{SessionID: session.ID, Status: string(StatusRunning)}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		slog.Warn("environment_setup: failed to marshal start response", "error", marshalErr.Error())
		data = marshalErrorFallback(marshalErr)
	}
	targetLine := ""
	if plan.target.ID != "" {
		targetLine = fmt.Sprintf("\ntarget workspace: %s", plan.target.ID)
	}
	notice := fmt.Sprintf(
		"\n\n[environment_setup] prefix: %s\nscope: %s%s\nThe script sees this path as OMNIPUS_ENV_PREFIX (cache: OMNIPUS_ENV_CACHE, temp: OMNIPUS_ENV_TMP). "+
			"Session running; use action=poll/read/kill with session_id %s. Exit 0 will mean the command succeeded only — "+
			"verify the installed capability in your own runtime before resuming.",
		plan.area.Prefix(), plan.scope, targetLine, session.ID)
	return &ToolResult{
		ForLLM:  string(data) + notice,
		ForUser: fmt.Sprintf("Installation session %s started", session.ID),
		IsError: marshalErr != nil,
	}
}

// persistCompletionOutcome writes the terminal record once publication has
// settled, atomically under one mutex section: the note feeds poll and read,
// the cap-aware buffer append feeds read, and the terminal status is the
// LAST thing written, so a poll landing mid-publication truthfully reports
// "running".
//
// When the natural exit was captured before publishTarget ran and the shared
// publication committed, the install genuinely finished and is published — a
// kill, timeout, cancel cascade, or shutdown reaper landing in the
// publication window stopped nothing, and every such relabel writes
// ExitCode=-1. The truthful terminal record is therefore done with the
// captured exit code, and both fields are written back, refusing the
// mid-flight downgrade. Any other terminal label means the natural exit was
// never captured (a real cancellation or failure): it stands.
func (t *EnvironmentSetupTool) persistCompletionOutcome(session *ProcessSession, pub setupPublication, notice string, naturalExit bool, finalExitCode int) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.resultNote = t.setupPollNote(pub)
	if !session.outputTruncated {
		session.outputBuffer.WriteString(notice)
	}
	if naturalExit && pub.outcome == "committed" {
		session.Status = StatusDone
		session.ExitCode = finalExitCode
	} else if naturalExit && session.Status == StatusRunning {
		session.Status = StatusDone
	}
}

// setupStartAbortResult reports a failed staging cleanup instead of hiding it
// behind the original startup error. No session exists on these paths, so this
// is the only result the caller can use to inspect the partial effect.
func setupStartAbortResult(area EnvironmentSetupTarget, cause string) *ToolResult {
	if err := area.Abort(); err != nil {
		return ErrorResult(cause + "; staging cleanup FAILED: " + err.Error())
	}
	return ErrorResult(cause)
}

// setupPublication is the outcome record publishTarget returns: the outcome
// string feeds the completion notice, the poll note, and the audit entry.
type setupPublication struct {
	outcome    string // committed | commit_failed | aborted | abort_failed | none
	detail     string // cause for failure outcomes; the scan note on committed
	generation string
	dir        string
}

// publishTarget terminates the target exactly once: shared scope publishes
// (Commit) on a clean natural exit — and aborts on every other terminal
// outcome (nonzero exit, killed, timeout, canceled). Workspace scope always
// takes the Abort path (a documented storage-side no-op). Publish eligibility
// rides the naturalExit flag rather than the session status: the session is
// deliberately left "running" until after this function returns so a
// mid-publication poll cannot report a completion that has not happened yet
// (MAJ-1).
func (t *EnvironmentSetupTool) publishTarget(plan startPlan, area EnvironmentSetupTarget, naturalExit bool, exitCode int) setupPublication {
	if plan.scope == environmentSetupScopeShared && naturalExit && exitCode == 0 {
		gen, dir, err := area.Commit()
		if err != nil {
			// MAJ-2: the command exited 0 but the area could not be published
			// — the installation is NOT available; the cause must reach the
			// agent, and cleanup is ATTEMPTED and REPORTED (never silently
			// swallowed).
			commitDetail := err.Error()
			if abortErr := area.Abort(); abortErr != nil {
				commitDetail += "; staging cleanup FAILED: " + abortErr.Error()
			}
			return setupPublication{outcome: "commit_failed", detail: commitDetail}
		}
		return setupPublication{outcome: "committed", generation: gen, dir: dir}
	}
	// Every non-published terminal outcome aborts. A cleanup FAILURE is a
	// distinct outcome (MAJ-2): the notice must not claim "staging removed"
	// when it is not true.
	abortErr := area.Abort()
	if plan.scope == environmentSetupScopeShared && abortErr != nil {
		return setupPublication{outcome: "abort_failed", detail: abortErr.Error()}
	}
	if plan.scope == environmentSetupScopeShared {
		return setupPublication{outcome: "aborted"}
	}
	return setupPublication{outcome: "none"}
}

// setupCompletionNotice appends the honest outcome lines to the completion
// result (ES-FR-04): what was published (or not), where the area is, the
// survivor-scan result and its standing limitation, and the
// verify-in-runtime caveat.
func (t *EnvironmentSetupTool) setupCompletionNotice(plan startPlan, pub setupPublication) string {
	var lines string
	switch pub.outcome {
	case "committed":
		lines = fmt.Sprintf(
			"\n\n[environment_setup] shared installation published: generation %s at %s (read/execute-only for agents from now on).\n"+
				"limitation: Bash process cleanup covers ordinary inherited children; deliberately detached children can outlive the installer. Installation commands must wait for their work to finish.",
			pub.generation, pub.dir)
	case "commit_failed":
		lines = fmt.Sprintf(
			"\n\n[environment_setup] the command exited 0 but PUBLICATION FAILED — the shared installation is NOT published; treat it as not available. Cause: %s.",
			pub.detail)
	case "abort_failed":
		lines = fmt.Sprintf(
			"\n\n[environment_setup] the command failed and staging cleanup FAILED: %s — staging may remain on disk; previously published installations unchanged.",
			pub.detail)
	case "aborted":
		lines = "\n\n[environment_setup] shared installation NOT published (command failed, was killed, timed out, or was canceled); staging removed, previously published installations unchanged."
	default:
		// "none" — workspace scope has no publication semantics; the agent
		// owns partial effects in its own reserved subtree.
		lines = fmt.Sprintf(
			"\n\n[environment_setup] installation area: %s (workspace scope; partial effects are yours to inspect and fix).",
			plan.area.Prefix())
	}
	return lines + " Exit 0 reports command success only — verify the installed capability in your own runtime before resuming."
}

// setupPollNote is the compact terminal note persisted on the session
// (MAJ-1): poll reports it alongside the status JSON so a late poller gets
// the same publication story the completion callback carried.
func (t *EnvironmentSetupTool) setupPollNote(pub setupPublication) string {
	switch pub.outcome {
	case "committed":
		return "publication: committed (generation " + pub.generation + ")"
	case "commit_failed":
		return "publication: FAILED (" + pub.detail + ")"
	case "abort_failed":
		return "staging cleanup FAILED (" + pub.detail + ")"
	case "aborted":
		return "publication: not published (command failed, was killed, timed out, or was canceled); staging removed"
	default:
		return "" // workspace scope: no publication semantics
	}
}

// auditPublication writes the audit record for the shared-scope publication
// outcome. A commit failure is recorded as an audit ERROR so an operator sees
// a zero-exit install that never became available; a blocked publication
// and a failed cleanup are errors too.
func (t *EnvironmentSetupTool) auditPublication(plan startPlan, pub setupPublication) {
	if t.auditLogger == nil {
		return
	}
	details := map[string]any{
		"session_id": plan.ownerSessionID,
		"purpose":    plan.purpose,
		"scope":      plan.scope,
		"prefix":     plan.area.Prefix(),
	}
	decision := audit.DecisionAllow
	switch pub.outcome {
	case "committed":
		details["publication"] = "committed"
		details["generation"] = pub.generation
		details["published_dir"] = pub.dir
	case "commit_failed":
		decision = audit.DecisionError
		details["publication"] = "commit_failed"
		details["cause"] = pub.detail
	case "abort_failed":
		decision = audit.DecisionError
		details["publication"] = pub.outcome
		details["cause"] = pub.detail
	case "aborted":
		details["publication"] = "aborted"
	default:
		return // workspace scope: the run-path allow entry already covers it
	}
	if err := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: decision,
		AgentID:  plan.agentID,
		Tool:     t.Name(),
		Details:  details,
	}); err != nil {
		slog.Warn("environment_setup: audit write failed", "error", err)
	}
}

// --- session actions (poll / read / kill) ------------------------------------
//
// Thin mirrors of the bash dispatchers (shell_bg.go) over the SAME shared
// SessionManager and GetOwned ownership gate — duplicated only because those
// are ExecTool methods and shell_bg.go is another lane's file; keep them in
// lockstep with bash behavior (status shape, ownership refusal, benign kill
// races) when it changes.

func (t *EnvironmentSetupTool) getSessionArg(ctx context.Context, args map[string]any) (*ProcessSession, string, *ToolResult) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, "", ErrorResult("session_id is required")
	}
	session, err := t.sessionManager.GetOwned(sessionID, ToolTranscriptSessionID(ctx))
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			// Same indistinguishable "not found" a foreign session gets — no
			// existence oracle for other sessions' jobs (GetOwned contract).
			return nil, "", ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return nil, "", ErrorResult(err.Error())
	}
	return session, sessionID, nil
}

func (t *EnvironmentSetupTool) executePoll(ctx context.Context, args map[string]any) *ToolResult {
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
		slog.Warn("environment_setup: failed to marshal poll response", "error", marshalErr.Error())
		data = marshalErrorFallback(marshalErr)
	}
	result := &ToolResult{ForLLM: string(data), IsError: marshalErr != nil}
	// MAJ-1: a poll that arrives after completion carries the persisted
	// publication note — the same story the completion callback told.
	if note := session.ResultNote(); note != "" {
		result.ForLLM += "\n\n" + note
	}
	return result
}

func (t *EnvironmentSetupTool) executeRead(ctx context.Context, args map[string]any) *ToolResult {
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
		slog.Warn("environment_setup: failed to marshal read response", "error", marshalErr.Error())
		data = marshalErrorFallback(marshalErr)
	}
	result := &ToolResult{ForLLM: string(data), IsError: marshalErr != nil}
	// The captured output has a bounded size and may already be truncated
	// before completion persists its publication note. Keep that terminal
	// outcome outside the buffer so read remains truthful after noisy jobs.
	if note := session.ResultNote(); note != "" {
		result.ForLLM += "\n\n" + note
	}
	return result
}

// executeKill terminates a running install session, mirroring bash's MIN-002
// discipline: if the process already exited naturally, report the REAL final
// status instead of a false "killed" — IsDone() pre-check plus KillAndRelabel's
// benign ErrSessionDone race handling.
func (t *EnvironmentSetupTool) executeKill(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}
	if session.IsDone() {
		return t.sessionActionResult(sessionID, session)
	}
	if err := session.KillAndRelabel(StatusKilled); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return t.sessionActionResult(sessionID, session)
		}
		if t.killAuditFn != nil {
			t.killAuditFn(session.PID, err, "environment_setup_kill_action")
		}
		return ErrorResult(fmt.Sprintf("failed to kill session: %v", err))
	}
	return t.sessionActionResult(sessionID, session)
}

func (t *EnvironmentSetupTool) sessionActionResult(sessionID string, session *ProcessSession) *ToolResult {
	status := session.GetStatus()
	resp := ExecResponse{
		SessionID: sessionID,
		Status:    status,
		ExitCode:  session.GetExitCode(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		slog.Warn("environment_setup: failed to marshal session action response", "error", marshalErr.Error())
		data = marshalErrorFallback(marshalErr)
	}
	result := &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s %s", sessionID, status),
		IsError: marshalErr != nil,
	}
	// Poll/read/kill parity: a finished session's kill action carries the
	// same persisted publication story, so a publication failure is never
	// hidden behind a bare {"status":"done"}.
	if note := session.ResultNote(); note != "" {
		result.ForLLM += "\n\n" + note
	}
	return result
}
