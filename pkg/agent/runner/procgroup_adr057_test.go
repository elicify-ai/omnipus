//go:build !windows

// Not built on Windows: process GROUPS are a POSIX concept. These tests set
// SysProcAttr.Setpgid and signal the group with syscall.Kill, neither of
// which exists in the Windows syscall package. The Windows equivalent is a
// Job Object (pkg/sandbox/hardened_exec_windows.go) and needs its own test.

// procgroup_adr057_test.go — ADR-057 U22 (W9c, FR-029, BDD-86, test #68a).
//
// Regression coverage for the process-group gap procgroup_unix.go closes: a
// 3P external-CLI child that itself forks a subprocess (a tool it shells
// out to, a dev server, a lint run) used to leave that subprocess running as
// a silent orphan once the driver's Cancel() fired, because
// exec.CommandContext's default cancellation kills only the direct child
// PID. These tests assert against REAL OS processes and REAL PIDs — per
// this repo's own precedent for exactly this shape of test
// (pkg/tools/session_adr057_unix_test.go's pidAlive / u16's real-PID
// cascade tests, pkg/entity/store_crossprocess_test.go) — rather than
// trusting driver-internal state alone, since an orphaned process leaves no
// error and is otherwise invisible.
//
// RED/GREEN, reported alongside this file: with u22SetupProcessGroup /
// u22InstallGroupCancel temporarily removed from one driver's Run() (the
// pre-fix shape), TestU22ClaudeDriver_CancelKillsProcessGroup failed with
// the grandchild PID still alive after Cancel() + the full poll deadline —
// reproducing the exact "0 killed, orphan survives" signature this unit
// exists to close. Restoring the two calls makes it pass.

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// u22GrandchildSleepSeconds is the lifetime of the orphan-prone grandchild
// spawned by the stub scripts below. Long enough to reliably still be alive
// across the precondition check and the (short) group-kill grace window;
// short enough that a deliberately-reproduced RED failure (the bug this
// unit fixes) self-terminates quickly rather than leaking a long-lived
// process on the box.
const u22GrandchildSleepSeconds = 20

// u22ProcessGroupStubScript returns a POSIX shell script that forks a
// detached `sleep` grandchild, announces its PID via pidFile, then itself
// keeps running — mirroring the exact shape named in FR-029's rationale
// (pkg/sandbox/hardened_exec_cancel_unix.go's "sh forks a grandchild that
// inherits the pipe fds" scenario). The direct child (this script, run as
// `sh`) and its `sleep` grandchild share ONE process group by default
// (fork() does not change pgid), so a GROUP kill reaches both while a
// direct-PID-only kill reaches neither the grandchild.
//
// Fast-paths `--version` (all three drivers' Run() calls
// detectAndPinVersion/detectCLIVersion FIRST, which execs the SAME binary as
// `<binary> --version` under its own bare 5s context timeout with no group
// kill at all — driver_stream.go's detectCLIVersion). Without this fast
// path the version probe would ALSO fork the grandchild and then block for
// the grandchild's full lifetime on the version-check's own pipe (the exact
// pipe-holding-grandchild hazard FR-029 is about), inflating every test by
// ~20s for a code path this unit isn't exercising.
func u22ProcessGroupStubScript(t *testing.T, pidFile string) string {
	t.Helper()
	body := fmt.Sprintf(`#!/bin/sh
case "$1" in
  --version) echo "1.0.0-stub"; exit 0 ;;
esac
sleep %d &
echo $! > %s
sleep %d
`, u22GrandchildSleepSeconds, pidFile, u22GrandchildSleepSeconds)
	return writeStubScript(t, body)
}

// u22WaitGrandchildPID polls pidFile until the stub script has announced its
// grandchild's PID, or fails the test after a bounded deadline.
func u22WaitGrandchildPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pidfile %q did not contain a valid pid within deadline", pidFile)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// u22PidAlive reports whether pid is a live process by sending it signal 0
// (existence/permission check only — no signal delivered). Same technique as
// this repo's own pkg/tools/session_adr057_unix_test.go:pidAlive.
func u22PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// u22AssertGroupKillReapsGrandchild is the shared assertion body for the
// three per-driver tests below: wait for the grandchild to announce itself,
// prove it is alive (positive lower bound, binding rule 4), fire cancel,
// then prove the grandchild is actually gone within a bounded deadline.
func u22AssertGroupKillReapsGrandchild(t *testing.T, pidFile string, cancel func()) {
	t.Helper()
	gcPID := u22WaitGrandchildPID(t, pidFile)

	if !u22PidAlive(gcPID) {
		t.Fatalf("precondition failed: grandchild pid %d must be alive before Cancel", gcPID)
	}

	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for u22PidAlive(gcPID) {
		if time.Now().After(deadline) {
			t.Fatalf("FR-029 regression: grandchild pid %d (in the child's process group) is still "+
				"alive after Cancel() + 5s — Cancel must kill the whole process GROUP, not just the "+
				"direct child PID, or a 3P child's own subprocess tree survives as a silent orphan", gcPID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestU22ClaudeDriver_CancelKillsProcessGroup covers FR-029/BDD-86/#68a for
// driver_claude.go: Cancel() must kill the child's whole process group, not
// just the direct `claude` process, so a subprocess tree the CLI spawned
// does not survive as an orphan.
func TestU22ClaudeDriver_CancelKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script; FR-029 group-kill is POSIX-only")
	}

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	stub := u22ProcessGroupStubScript(t, pidFile)

	orig := claudeBinName
	claudeBinName = stub
	t.Cleanup(func() { claudeBinName = orig })

	d := NewClaudeDriver(nil)
	ch, err := d.Run(context.Background(), RunOptions{Input: "task"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	t.Cleanup(d.Cancel) // safety net: no leaked process even if an assertion fails first

	u22AssertGroupKillReapsGrandchild(t, pidFile, d.Cancel)
	drain(t, ch)
}

// TestU22CodexDriver_CancelKillsProcessGroup mirrors the claude test for
// driver_codex.go.
//
//nolint:dupl // parallel test scaffolding intentionally mirrors the claude/opencode variants (same mechanism, different driver/bin var)
func TestU22CodexDriver_CancelKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script; FR-029 group-kill is POSIX-only")
	}

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	stub := u22ProcessGroupStubScript(t, pidFile)

	orig := codexBinName
	codexBinName = stub
	t.Cleanup(func() { codexBinName = orig })

	d := NewCodexDriver(nil)
	ch, err := d.Run(context.Background(), RunOptions{Input: "task"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	t.Cleanup(d.Cancel)

	u22AssertGroupKillReapsGrandchild(t, pidFile, d.Cancel)
	drain(t, ch)
}

// TestU22OpencodeDriver_CancelKillsProcessGroup mirrors the claude test for
// driver_opencode.go.
//
//nolint:dupl // parallel test scaffolding intentionally mirrors the claude/codex variants (same mechanism, different driver/bin var)
func TestU22OpencodeDriver_CancelKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script; FR-029 group-kill is POSIX-only")
	}

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	stub := u22ProcessGroupStubScript(t, pidFile)

	orig := opencodeBinName
	opencodeBinName = stub
	t.Cleanup(func() { opencodeBinName = orig })

	d := NewOpencodeDriver(nil)
	ch, err := d.Run(context.Background(), RunOptions{Input: "task"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	t.Cleanup(d.Cancel)

	u22AssertGroupKillReapsGrandchild(t, pidFile, d.Cancel)
	drain(t, ch)
}

// TestU22SetupProcessGroup_InstallsGroupCancel is a fast unit check that
// u22SetupProcessGroup + u22InstallGroupCancel actually wire up Setpgid and
// cmd.Cancel — guards against a refactor silently dropping either half.
// Mirrors pkg/sandbox/hardened_exec_pgroup_test.go's
// TestInstallProcessGroupCancel_SetsCancel for the same shape of guard.
func TestU22SetupProcessGroup_InstallsGroupCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Setpgid / group-kill cmd.Cancel override is POSIX-only (FR-029)")
	}

	cmd := exec.Command("true")
	u22SetupProcessGroup(cmd)
	u22InstallGroupCancel(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid not set; a group-kill cancel would signal the wrong group")
	}
	if cmd.Cancel == nil {
		t.Fatal("cmd.Cancel is nil after u22InstallGroupCancel; group-kill override missing")
	}
	// Safe to invoke before Start (cmd.Process == nil): must return nil, not panic.
	if err := cmd.Cancel(); err != nil {
		t.Errorf("cmd.Cancel() before Start returned %v; want nil", err)
	}
}
