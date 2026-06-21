package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestClaudeDriver_DecideDeny_Cancels verifies the CRITICAL fix: a DENY decision
// must cancel the run (kill the process). Before the fix, ClaudeDriver.Decide
// consulted a never-populated `decided` map and was a complete no-op, so a deny
// never canceled anything. We install a sentinel cancel func and assert Decide
// with Allow=false invokes it.
func TestClaudeDriver_DecideDeny_Cancels(t *testing.T) {
	d := NewClaudeDriver(nil)

	var canceled bool
	var mu sync.Mutex
	d.mu.Lock()
	d.runID = "run-deny"
	d.eventCh = make(chan RunEvent, 1) // mark a run as active
	d.cancel = func() {
		mu.Lock()
		canceled = true
		mu.Unlock()
	}
	d.mu.Unlock()

	d.Decide(PermissionDecision{RequestID: "req-1", Allow: false, Reason: "policy deny"})

	mu.Lock()
	got := canceled
	mu.Unlock()
	if !got {
		t.Fatal("Decide(Allow=false) did not cancel the run; deny must cancel (CRITICAL fix)")
	}
}

// TestClaudeDriver_DecideAllow_DoesNotCancel verifies that an ALLOW decision is a
// no-op (the run continues) — claude -p has no mid-run allow channel.
func TestClaudeDriver_DecideAllow_DoesNotCancel(t *testing.T) {
	d := NewClaudeDriver(nil)

	var canceled bool
	var mu sync.Mutex
	d.mu.Lock()
	d.cancel = func() {
		mu.Lock()
		canceled = true
		mu.Unlock()
	}
	d.mu.Unlock()

	d.Decide(PermissionDecision{RequestID: "req-1", Allow: true})

	mu.Lock()
	got := canceled
	mu.Unlock()
	if got {
		t.Fatal("Decide(Allow=true) canceled the run; an allow must be a no-op")
	}
}

// TestCodexDriver_DecideDeny_Cancels and TestOpencodeDriver_DecideDeny_Cancels
// confirm the deny→Cancel contract holds across the other two drivers too.
func TestCodexDriver_DecideDeny_Cancels(t *testing.T) {
	d := NewCodexDriver(nil)
	var canceled bool
	var mu sync.Mutex
	d.mu.Lock()
	d.runID = "run-deny"
	d.cancel = func() { mu.Lock(); canceled = true; mu.Unlock() }
	d.mu.Unlock()

	d.Decide(PermissionDecision{RequestID: "req-1", Allow: false})
	mu.Lock()
	defer mu.Unlock()
	if !canceled {
		t.Fatal("codex Decide(deny) did not cancel")
	}
}

func TestOpencodeDriver_DecideDeny_Cancels(t *testing.T) {
	d := NewOpencodeDriver(nil)
	var canceled bool
	var mu sync.Mutex
	d.mu.Lock()
	d.runID = "run-deny"
	d.cancel = func() { mu.Lock(); canceled = true; mu.Unlock() }
	d.mu.Unlock()

	d.Decide(PermissionDecision{RequestID: "req-1", Allow: false})
	mu.Lock()
	defer mu.Unlock()
	if !canceled {
		t.Fatal("opencode Decide(deny) did not cancel")
	}
}

// TestClaudeDriver_EventChReset_AllowsResume verifies the MAJOR fix: after a run
// completes, the parse goroutine's defer must clear d.eventCh so a SECOND Run (or
// Resume) is accepted. Before the fix, eventCh stayed non-nil forever and every
// subsequent Run returned "already active", breaking resumability.
//
// It drives the real run lifecycle against a stub process (no real claude CLI).
func TestClaudeDriver_EventChReset_AllowsResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}

	// A stub that ignores all args, prints one valid claude result line, exits 0.
	stub := writeStubScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"success","result":"ok"}'
exit 0
`)

	orig := claudeBinName
	claudeBinName = stub
	t.Cleanup(func() { claudeBinName = orig })

	d := NewClaudeDriver(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First run: drain to completion (channel closes).
	ch1, err := d.Run(ctx, RunOptions{Input: "first"})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	drain(t, ch1)

	// After completion the driver must have reset its run state.
	d.mu.Lock()
	resetEventCh := d.eventCh == nil
	resetCmd := d.cmd == nil
	resetCancel := d.cancel == nil
	d.mu.Unlock()
	if !resetEventCh {
		t.Error("eventCh not reset to nil after run completion (resumability broken)")
	}
	if !resetCmd {
		t.Error("cmd not reset to nil after run completion")
	}
	if !resetCancel {
		t.Error("cancel not reset to nil after run completion")
	}

	// Second run (Resume) must be accepted, not rejected with "already active".
	ch2, err := d.Resume(ctx, "run-resume")
	if err != nil {
		t.Fatalf("Resume() after completed run returned %v; want nil (eventCh-reset fix)", err)
	}
	drain(t, ch2)
}

// TestCodexDriver_EventChReset_AllowsResume mirrors the claude lifecycle test for
// the codex driver.
//
//nolint:dupl // parallel test scaffolding intentionally mirrors TestOpencodeDriver_EventChReset_AllowsResume (same lifecycle, different driver/stub)
func TestCodexDriver_EventChReset_AllowsResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	stub := writeStubScript(t, `#!/bin/sh
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
exit 0
`)
	orig := codexBinName
	codexBinName = stub
	t.Cleanup(func() { codexBinName = orig })

	d := NewCodexDriver(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch1, err := d.Run(ctx, RunOptions{Input: "first"})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	drain(t, ch1)

	d.mu.Lock()
	reset := d.eventCh == nil && d.cmd == nil && d.cancel == nil
	d.mu.Unlock()
	if !reset {
		t.Error("codex run state not reset after completion")
	}

	ch2, err := d.Resume(ctx, "run-resume")
	if err != nil {
		t.Fatalf("codex Resume() after completed run = %v, want nil", err)
	}
	drain(t, ch2)
}

// TestOpencodeDriver_EventChReset_AllowsResume mirrors the lifecycle test for the
// opencode driver.
//
//nolint:dupl // parallel test scaffolding intentionally mirrors TestCodexDriver_EventChReset_AllowsResume (same lifecycle, different driver/stub)
func TestOpencodeDriver_EventChReset_AllowsResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	stub := writeStubScript(t, `#!/bin/sh
printf '%s\n' '{"type":"session.complete","session_id":"s1"}'
exit 0
`)
	orig := opencodeBinName
	opencodeBinName = stub
	t.Cleanup(func() { opencodeBinName = orig })

	d := NewOpencodeDriver(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch1, err := d.Run(ctx, RunOptions{Input: "first"})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	drain(t, ch1)

	d.mu.Lock()
	reset := d.eventCh == nil && d.cmd == nil && d.cancel == nil
	d.mu.Unlock()
	if !reset {
		t.Error("opencode run state not reset after completion")
	}

	ch2, err := d.Resume(ctx, "run-resume")
	if err != nil {
		t.Fatalf("opencode Resume() after completed run = %v, want nil", err)
	}
	drain(t, ch2)
}

// writeStubScript writes an executable POSIX script to a temp dir and returns its
// absolute path.
func writeStubScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stub-cli")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("writeStubScript: %v", err)
	}
	return path
}

// drain reads a run's event channel until it closes (run complete) or a deadline
// elapses.
func drain(t *testing.T, ch <-chan RunEvent) {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("run channel did not close within deadline")
		}
	}
}
