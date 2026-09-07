//go:build !windows

// Not built on Windows: these tests drive REAL processes and assert with
// syscall.Kill, which does not exist there. The behaviour under test is the
// POSIX signal path; Windows cancels via Job Objects, a different mechanism
// that needs its own test rather than a #ifdef of this one.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_stop_shell_leaf_adr052_qa_test.go is ADR-052 Wave 2's qa-go coverage
// for spec Test 9 / SC-004 ("Stop reaches shell leaves"): the existing
// plan_stop_test.go suite proves the fan-out reaches sessions via a FAKE
// sessionCanceller (records call args, never touches a real process), and
// cancel_background_bash_test.go proves RequestCancel's KillBackgroundSessions
// hook reaches a REAL tools.SessionManager but with a SYNTHETIC ProcessSession
// (a fabricated out-of-range PID, never a genuinely running OS process). This
// file closes the remaining gap: an ACTUAL background OS process, spawned via
// the real production bash tool under a plan member's session, killed via
// PlanEngine.StopTask's REAL (non-faked) canceller — proving the whole chain
// StopTask -> AgentLoop.RequestCancelForSession ->
// killBackgroundSessionsForCancelSurface -> tools.GetSharedSessionManager() ->
// KillAllForSession -> killProcessGroup(pid) -> syscall.Kill(-pid, SIGKILL)
// reaches a genuine OS process group, not just an in-memory status flag.
//
// Feasibility note (requested by the dispatching lead): a true end-to-end test
// IS feasible in this sandbox — os.StartProcess/exec.Command works fine here,
// and the real ExecTool's run_in_background path is exactly what
// bash_async_completion_test.go already exercises for the async-notifier
// wiring. What this file adds beyond that existing coverage is: (a) driving
// the kill through PlanEngine.StopTask (a plan member, not a raw chat cancel)
// with pe.canceller left as the REAL *AgentLoop (production wiring via
// NewPlanEngine — no fakeSessionCanceller substitution), and (b) verifying
// process DEATH via a direct OS-level syscall.Kill(pid, 0) liveness probe
// (with an ESRCH poll loop to absorb the natural SIGKILL-to-reap race), not
// merely the in-memory ProcessSession.GetStatus()=="canceled" flag every
// existing test stops at.
//
// Traces to: autonomous-agent-plan-execution-spec.md Test 9 (line 531),
// SC-004 (line 703), FR-013 (line 669), FR-025 (line 681).

package agent

import (
	"context"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// waitForProcessGroupDeath polls syscall.Kill(-pid, 0) until it reports the
// process group is gone (ESRCH) or the deadline elapses. SIGKILL delivery is
// near-instant at the kernel level, but the sender (killProcessGroup) does not
// itself wait for the target to be reaped — cmd.Wait() in runBackground's own
// completion goroutine does that asynchronously — so a single immediate check
// right after StopTask returns would be racy. Returns nil once confirmed dead,
// or a non-nil error describing the last observed liveness state.
func waitForProcessGroupDeath(t *testing.T, pid int, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		err := syscall.Kill(-pid, 0) // signal 0: liveness probe only, sends nothing
		if err == syscall.ESRCH {
			return nil // process group confirmed gone
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr == nil {
		return errUnexpectedNilKill
	}
	return lastErr
}

// errUnexpectedNilKill guards the pathological case where syscall.Kill(-pid, 0)
// returns nil (still alive, no error) right up to the deadline.
var errUnexpectedNilKill = &stillAliveError{}

type stillAliveError struct{}

func (*stillAliveError) Error() string {
	return "process group still responds to signal 0 (still alive)"
}

// TestStopReachesShellLeaf_RealBackgroundProcessGroupDies is spec Test 9 /
// SC-004: a plan member with a REAL, genuinely running background `sleep`
// process (spawned via the actual production ExecTool.ExecuteAsync path, the
// SAME session's background-shell machinery a real task run would use) is
// Stopped via PlanEngine.StopTask, and the process GROUP must die within the
// hard-abort window — verified at the OS level, not merely via the
// ProcessSession's in-memory status field.
func TestStopReachesShellLeaf_RealBackgroundProcessGroupDies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX process-group SIGKILL semantics (syscall.Kill(-pid, ...))")
	}
	// Deliberately NOT t.Parallel(). tools.GetSharedSessionManager() is a
	// single process-wide singleton shared by every *AgentLoop any pkg/agent
	// test constructs (see loop.go's AgentLoop.Close doc comment, "LOAD-
	// BEARING PRECONDITION": that reaper's unconditional, process-wide
	// SessionManager.KillAll() is only safe under a single-AgentLoop-per-
	// process assumption that holds in production but NOT inside this
	// package's own test binary). Under a full pkg/agent package run with
	// this test parallel, ANOTHER, unrelated test's t.Cleanup(al.Close) can
	// fire concurrently and its KillAll() can race THIS test's own
	// PlanEngine.StopTask -> ... -> KillAllForSession cascade for the real
	// background "sleep 60" ProcessSession this test owns — whichever call
	// wins the ProcessSession's mutex first decided the final status. This
	// was confirmed as the actual root cause of a CI-only flake (status
	// ended up "done" instead of "canceled" only under full-package load,
	// never when run alone): tools/session.go's KillAndRelabel now lets a
	// specific status (canceled/killed/timeout) correct an already-set
	// generic "done" written by such a racing caller within its OWN
	// KillAndRelabel call (see statusPriority's doc comment and
	// pkg/tools/session_status_priority_test.go), but that fix cannot help
	// if the OTHER test's KillAll() strikes even earlier — anywhere between
	// this test spawning the real background process and its own
	// StopTask/KillAllForSession call ever reaching it (a window that
	// includes real disk I/O for plan/task store setup below, not just a
	// microsecond mutex race). Running this test non-parallel removes it
	// from that shared singleton's blast radius entirely: per the Go testing
	// package's scheduling contract, no t.Parallel() test's body can be
	// actively executing while a sequential top-level test in the same
	// package is running, so no other test's AgentLoop.Close()/KillAll() can
	// fire during this test's execution window at all.

	// newBashAsyncTestLoop (bash_async_completion_test.go, same package)
	// wires a REAL *tools.ExecTool (GodMode=true, so no sandbox-hardening
	// plumbing unrelated to this test's assertion) onto a real *AgentLoop and
	// grants every seeded agent "bash: allow". The scenario provider is never
	// actually invoked here — this test drives the exec tool directly rather
	// than through a scripted LLM turn — so an empty scenario is enough.
	al, _, wrapper := newBashAsyncTestLoop(t, testutil.NewScenario())

	const sessionKey = "adr052-shell-leaf-session"

	// Spawn a REAL, long-running background process the same way a live turn
	// would: runTurn stamps tools.WithTranscriptSessionID(turnCtx,
	// ts.opts.TranscriptSessionID) onto the tool-dispatch ctx before calling
	// ExecuteAsync (pkg/agent/loop.go) — shell.go's executeRun reads it back
	// via ToolTranscriptSessionID(ctx) to stamp ProcessSession.OwnerSessionID.
	// Reproducing that ctx wiring directly here (rather than driving a full
	// scripted chat turn) exercises the exact same production code path with
	// less incidental machinery.
	bgCtx := tools.WithTranscriptSessionID(context.Background(), sessionKey)
	startResult := wrapper.ExecuteAsync(bgCtx, map[string]any{
		"command":           "sleep 60",
		"run_in_background": true,
		"timeout_seconds":   60,
	}, nil)
	if startResult == nil || startResult.IsError {
		var forLLM string
		if startResult != nil {
			forLLM = startResult.ForLLM
		}
		t.Fatalf("BLOCKED: could not start the real background sleep process, got IsError with ForLLM=%q", forLLM)
	}

	bgSessionID := wrapper.capturedSessionID()
	if bgSessionID == "" {
		t.Fatal("BLOCKED: the real run_in_background call did not produce a session_id")
	}

	procSess, err := tools.GetSharedSessionManager().Get(bgSessionID)
	if err != nil {
		t.Fatalf("BLOCKED: could not resolve the just-started ProcessSession: %v", err)
	}
	pid := procSess.PID
	if pid <= 0 {
		t.Fatalf("BLOCKED: ProcessSession has no valid PID (%d) — cannot verify OS-level death", pid)
	}

	// Sanity: the process must genuinely be alive right now (proves this is a
	// REAL spawned process, not a synthetic/pre-dead PID like the
	// cancel_background_bash_test.go fakes deliberately use).
	if aliveErr := syscall.Kill(pid, 0); aliveErr != nil {
		t.Fatalf("BLOCKED: expected the freshly-spawned sleep process (pid %d) to be alive before Stop, got %v",
			pid, aliveErr)
	}

	// Build a real plan + task-store pair with ONE in-progress member whose
	// SessionID is the SAME sessionKey the background process was stamped
	// with as its OwnerSessionID — this is what lets StopTask's cancel reach
	// it.
	dir := t.TempDir()
	planStore := plan.New(filepath.Join(dir, "plans"))
	taskStore := task.New(filepath.Join(dir, "tasks"))
	if createPlanErr := planStore.Create(&plan.Plan{
		ID: "p-shell-leaf", Title: "p-shell-leaf", WorkspaceID: "ws",
		OwnerAgentID: "native-agent", State: plan.StateRunning,
	}); createPlanErr != nil {
		t.Fatalf("create plan: %v", createPlanErr)
	}
	member := &task.Task{
		Title: "member-with-real-bg-shell", WorkspaceID: "ws", PlanID: "p-shell-leaf",
		Status: task.StatusInProgress, SessionID: sessionKey,
	}
	if createTaskErr := taskStore.Create(member); createTaskErr != nil {
		t.Fatalf("create task: %v", createTaskErr)
	}

	// NewPlanEngine wires pe.canceller = al (the REAL *AgentLoop) by default
	// — deliberately NOT overridden with a fakeSessionCanceller here, unlike
	// every test in plan_stop_test.go, so StopTask's cancel genuinely reaches
	// AgentLoop.RequestCancelForSession -> killBackgroundSessionsForCancelSurface
	// -> tools.GetSharedSessionManager().KillAllForSession.
	pe := NewPlanEngine(al, planStore, taskStore, nil)

	updated, err := pe.StopTask(context.Background(), member.ID, "tester", "web")
	if err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	if updated.Status != task.StatusFailed || !isCancelledMember(updated) {
		t.Fatalf("member status=%q result=%q, want failed+cancel-marker", updated.Status, updated.Result)
	}

	// (a) in-memory status: mirrors the existing cancel_background_bash_test.go
	// assertions — the ProcessSession must be relabeled terminal/canceled.
	gotSess, getErr := tools.GetSharedSessionManager().Get(bgSessionID)
	if getErr != nil {
		t.Fatalf("could not re-fetch the background ProcessSession after Stop: %v", getErr)
	}
	if !gotSess.IsDone() {
		t.Fatal("the background ProcessSession must be terminal after StopTask")
	}
	if got := gotSess.GetStatus(); got != "canceled" {
		t.Errorf("ProcessSession status = %q, want \"canceled\" (StopTask's fan-out relabel)", got)
	}

	// (b) THE NEW ASSERTION this file adds: the REAL OS process group must
	// actually be dead — not just flagged dead in memory. killProcessGroup
	// (pkg/tools/session_process_unix.go) sends SIGKILL to -pid (the whole
	// process group, Setpgid:true baseline); confirm no process in that group
	// still responds to a liveness probe, within SC-004's ~3s hard-abort
	// window (generous 5s ceiling here for CI/sandbox scheduling jitter).
	if err := waitForProcessGroupDeath(t, pid, 5*time.Second); err != nil {
		t.Fatalf("SC-004 VIOLATION: the real background process group (pid %d) did not die within the "+
			"hard-abort window after StopTask — last liveness probe result: %v", pid, err)
	}
}
