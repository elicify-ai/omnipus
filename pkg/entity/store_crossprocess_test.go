// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

// Package entity — the missing CROSS-PROCESS half of ADR-054 D3's
// concurrency contract.
//
// TestSameEntityContention_ConcurrentUpdatesSerializeNoLostUpdate
// (store_test.go) proves no-lost-update for N GOROUTINES inside a single
// process. D3 additionally claims a cross-process guarantee via the sidecar
// lockfile (fileutil.WithFlock on entities/<kind>/<id>.lock), but nothing in
// this repo ever forked a second OS process to exercise it — the in-process
// striped mutex (lock.go) provides zero protection across process
// boundaries, since each process gets its own address space and therefore
// its own, entirely separate stripedLock instance.
//
// This file closes that gap for all THREE call sites that share the sidecar
// flock, not just Update:
//
//   - TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate — N processes
//     racing Store.Update's read-modify-write cycle on one shared entity
//     counter. No lost updates iff the sidecar flock genuinely serializes
//     Update across processes.
//   - TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner — N processes
//     racing Store.Create on the exact same entity ID. Exactly one may win
//     (nil error); every other process must observe ErrAlreadyExists, and the
//     persisted record must be exactly the winner's payload, never a
//     corrupted mix of two processes' writes.
//   - TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity — one
//     process deletes an entity while several others concurrently run
//     Store.Update against the same ID. A correctly serialized store must
//     never let an in-flight Update "resurrect" the file after a concurrent
//     Delete has removed it.
//
// A prior revision of this file only exercised Update, and flock_isolation_test.go's
// (and ADR-054 §5.1's) claim that the gap was closed for "the Update/Create/Delete
// call sites" was an overclaim: a review independently reproduced that, with
// fileutil.WithFlock stubbed to a pass-through at BOTH the Create and Delete call
// sites in store.go, the entire pre-existing pkg/entity suite still passed. The two
// tests added here close that specific overclaim; see each test's own doc comment
// for how it was proven non-vacuous.
//
// All three tests re-exec the test binary itself as several independent OS
// processes (mirrors the established pkg/sandbox/*_subprocess_test.go
// convention — an env-var marker selects "child mode", the child communicates
// outcome via os.Exit rather than t.Fatal/t.Skip since the testing.T in the
// child process is not the one the parent observes).
package entity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	crossProcChildEnv   = "OMNIPUS_ENTITY_XPROC_CHILD"
	crossProcDirEnv     = "OMNIPUS_ENTITY_XPROC_DIR"
	crossProcIDEnv      = "OMNIPUS_ENTITY_XPROC_ID"
	crossProcItersEnv   = "OMNIPUS_ENTITY_XPROC_ITERS"
	crossProcSleepMsEnv = "OMNIPUS_ENTITY_XPROC_SLEEP_MS"

	// Barrier env/config shared by the Create-race and Delete/Update-race
	// tests below (the original Update-race test doesn't need one — its race
	// window is already widened via an in-mutate sleep).
	crossProcBarrierDirEnv = "OMNIPUS_ENTITY_XPROC_BARRIER_DIR"

	crossProcCreateChildEnv = "OMNIPUS_ENTITY_XPROC_CREATE_CHILD"
	crossProcCreateIdxEnv   = "OMNIPUS_ENTITY_XPROC_CREATE_IDX"
	// crossProcCreatePayloadBytes pads each racer's payload so Store.write's
	// marshal+fsync+rename takes long enough to give straggling siblings a
	// realistic chance to also clear the pre-write existence check while the
	// window is open — widening the race window the same way the Update-race
	// test's in-mutate sleep does, but Create has no caller-supplied callback
	// to hook, so the widening has to come from the payload itself.
	crossProcCreatePayloadBytes = 2_000_000

	crossProcDUChildEnv = "OMNIPUS_ENTITY_XPROC_DU_CHILD"
	crossProcDURoleEnv  = "OMNIPUS_ENTITY_XPROC_DU_ROLE" // "update" or "delete"
	crossProcDUIdxEnv   = "OMNIPUS_ENTITY_XPROC_DU_IDX"

	// childTestTimeoutFlag bounds each child's OWN go test run (GAP 3): a
	// directly re-exec'd test binary has no default timeout (only the `go
	// test` wrapper injects the familiar 10m), so a child stuck on a leaked
	// flock would otherwise hang forever, orphaned once the parent gives up.
	childTestTimeoutFlag = "-test.timeout=60s"
	// childContextTimeout is the parent-side belt to childTestTimeoutFlag's
	// suspenders: exec.CommandContext force-kills the child if go test's own
	// internal timeout somehow fails to fire (e.g. because the process is
	// wedged in an uninterruptible flock(2) with no goroutine scheduled to
	// even notice).
	childContextTimeout = 90 * time.Second
)

// newChildCmd builds an exec.Cmd that re-execs this test binary running only
// the named test (which must itself branch into child-mode based on an env
// var in env), bounded by childContextTimeout/childTestTimeoutFlag per GAP 3.
func newChildCmd(ctx context.Context, testName string, env []string) *exec.Cmd {
	//nolint:gosec // intentional test-binary self-exec, mirrors the
	// pkg/sandbox/*_subprocess_test.go convention.
	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^"+testName+"$",
		"-test.count=1",
		childTestTimeoutFlag,
	)
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// reapChildren kills and WAITS every started process. Wait is the part a
// bare Kill loop is missing (GAP 3): on POSIX, a killed-but-unwaited child
// becomes a zombie until the parent process exits, so a t.Fatalf mid-Start-loop
// (already-started siblings killed but never reaped) would otherwise leak
// zombie processes for the remaining lifetime of the test binary.
func reapChildren(cmds []*exec.Cmd) {
	for _, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
}

// startAll starts every cmd, failing loudly (and relying on the caller's
// t.Cleanup(reapChildren) to reap anything already started) on the first
// error rather than silently leaving some children unstarted.
func startAll(t *testing.T, cmds []*exec.Cmd) {
	t.Helper()
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child process %d: %v", i, err)
		}
	}
}

// childAwaitBarrier signals readiness by creating a per-index marker file in
// barrierDir, then polls until every participant's marker is visible AND the
// parent has released the barrier (created barrierDir/go) — tightening the
// start-time skew between racing child processes far below what plain
// near-simultaneous exec.Cmd.Start() calls would achieve on their own, since
// fork+exec latency alone can be multiple milliseconds and would otherwise
// dominate the very race window these tests exist to exercise.
func childAwaitBarrier(barrierDir string, idx int) error {
	readyPath := filepath.Join(barrierDir, fmt.Sprintf("ready-%d", idx))
	if err := os.WriteFile(readyPath, []byte("1"), 0o600); err != nil {
		return fmt.Errorf("write ready marker: %w", err)
	}
	goPath := filepath.Join(barrierDir, "go")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(goPath); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for barrier release")
		}
		time.Sleep(time.Millisecond)
	}
}

// parentReleaseBarrier blocks until all n participants have signalled ready
// (see childAwaitBarrier), then releases every one of them at (as close to)
// the same instant as the filesystem allows by creating the barrier's "go"
// marker.
func parentReleaseBarrier(t *testing.T, barrierDir string, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; i < n; i++ {
		readyPath := filepath.Join(barrierDir, fmt.Sprintf("ready-%d", i))
		for {
			if _, err := os.Stat(readyPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for child %d to signal ready", i)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(barrierDir, "go"), []byte("1"), 0o600); err != nil {
		t.Fatalf("create barrier go file: %v", err)
	}
}

// TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate is the
// cross-process counterpart to
// TestSameEntityContention_ConcurrentUpdatesSerializeNoLostUpdate: same
// read-modify-write-on-a-shared-counter shape, but racing REAL OS processes
// instead of goroutines, so the in-process striped mutex cannot be the thing
// providing exclusion — only the sidecar flock (ADR-054 D3) can.
//
// Traces to: docs/internal/architecture/ADR-054-entity-config-separation.md
// §5 (D3, "Cross-process: requires a sidecar lockfile ... flocked across
// BOTH read and write").
func TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate(t *testing.T) {
	if os.Getenv(crossProcChildEnv) == "1" {
		runCrossProcessChild()
		return // unreachable — runCrossProcessChild calls os.Exit
	}

	const (
		numProcesses    = 6
		itersPerProcess = 15
		sleepMillis     = 15
	)

	dir := t.TempDir()
	s := New[testEntity](dir, testAccessors())

	e := &testEntity{ID: "xproc-counter", Name: "0"}
	if err := s.Create(e); err != nil {
		t.Fatalf("Create seed entity: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), childContextTimeout)
	defer cancel()

	cmds := make([]*exec.Cmd, numProcesses)
	bufs := make([]*bytes.Buffer, numProcesses)
	for i := 0; i < numProcesses; i++ {
		cmd := newChildCmd(ctx, "TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate", []string{
			crossProcChildEnv + "=1",
			crossProcDirEnv + "=" + dir,
			crossProcIDEnv + "=" + e.ID,
			crossProcItersEnv + "=" + strconv.Itoa(itersPerProcess),
			crossProcSleepMsEnv + "=" + strconv.Itoa(sleepMillis),
		})
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		cmds[i] = cmd
		bufs[i] = &buf
	}

	// Kill AND reap any survivor on ANY exit path (GAP 3). Without the Wait,
	// a t.Fatalf below calls runtime.Goexit while up to len(cmds)-1 children
	// are still writing entity files and sidecar locks into t.TempDir() —
	// TempDir's cleanup then races them, and a killed-but-unwaited child is
	// left as a zombie for the rest of the test binary's life on top of that.
	t.Cleanup(func() { reapChildren(cmds) })

	// Start every child before waiting on any of them so they genuinely race
	// against each other rather than running one at a time.
	startAll(t, cmds)

	// Collect ALL results before failing, so one bad child does not hide the
	// others' output and every process is reaped rather than orphaned.
	var childErrs []string
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			childErrs = append(childErrs,
				fmt.Sprintf("child process %d failed: %v\noutput:\n%s", i, err, bufs[i].String()))
		}
	}
	if len(childErrs) > 0 {
		t.Fatalf("%d of %d child processes failed:\n%s",
			len(childErrs), len(cmds), strings.Join(childErrs, "\n---\n"))
	}

	final, err := s.Get(e.ID)
	if err != nil {
		t.Fatalf("Get final entity: %v", err)
	}
	got, parseErr := strconv.Atoi(strings.TrimSpace(final.Name))
	if parseErr != nil {
		t.Fatalf("parse final counter %q: %v", final.Name, parseErr)
	}
	want := numProcesses * itersPerProcess
	if got != want {
		t.Fatalf("CROSS-PROCESS LOST UPDATE: final counter = %d, want %d "+
			"(%d concurrent OS processes x %d Update() calls each). Some processes' "+
			"read-modify-write cycles overlapped and overwrote each other — the sidecar "+
			"flock (fileutil.WithFlock on Store.lockPath) failed to serialize Store.Update "+
			"across process boundaries, exactly the guarantee ADR-054 D3 claims.",
			got, want, numProcesses, itersPerProcess)
	}
	t.Logf("cross-process serialization confirmed: %d processes x %d updates = %d, no lost updates",
		numProcesses, itersPerProcess, want)
}

// runCrossProcessChild is the child-mode implementation. It must only call
// os.Exit — never t.Fatal — because the parent communicates outcome via exit
// code (mirrors pkg/sandbox/backend_linux_subprocess_test.go's
// runLandlockChild convention): the child's own *testing.T is not observed
// by the parent, only its process exit status and captured output are.
func runCrossProcessChild() {
	dir := os.Getenv(crossProcDirEnv)
	id := os.Getenv(crossProcIDEnv)
	iters, itersErr := strconv.Atoi(os.Getenv(crossProcItersEnv))
	sleepMs, sleepErr := strconv.Atoi(os.Getenv(crossProcSleepMsEnv))
	if dir == "" || id == "" || itersErr != nil || sleepErr != nil || iters <= 0 {
		fmt.Fprintln(os.Stderr, "xproc child: missing or invalid env configuration")
		os.Exit(3)
	}

	s := New[testEntity](dir, testAccessors())
	sleepDur := time.Duration(sleepMs) * time.Millisecond

	for i := 0; i < iters; i++ {
		_, err := s.Update(id, func(t *testEntity) error {
			cur := 0
			trimmed := strings.TrimSpace(t.Name)
			if trimmed != "" && trimmed != "0" {
				var parseErr error
				cur, parseErr = strconv.Atoi(trimmed)
				if parseErr != nil {
					return fmt.Errorf("parse counter %q: %w", t.Name, parseErr)
				}
			}
			// Widen the window between read and write far past normal
			// process-scheduling jitter (same technique
			// pkg/fileutil/flock_test.go's TestAdvisoryFileLockWrites uses
			// in-process, "to increase contention probability"). Without the
			// sidecar flock actually serializing this section across
			// processes, two children racing here are virtually guaranteed
			// to both read the same "cur" before either writes back cur+1,
			// losing one of their increments.
			time.Sleep(sleepDur)
			cur++
			t.Name = strconv.Itoa(cur)
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "xproc child: Update #%d failed: %v\n", i, err)
			os.Exit(1)
		}
	}
	os.Exit(0)
}

// TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner closes GAP 1's Create
// half: it races numCreateRaceProcesses independent OS processes, all calling
// Store.Create with the SAME entity ID, barrier-synchronized to start their
// Create call as close to simultaneously as the filesystem allows. Store.Create
// never silently overwrites (see its doc comment), so the correct outcome
// regardless of scheduling is: exactly one process observes a nil error, every
// other process observes ErrAlreadyExists, and the persisted record is exactly
// the winner's own payload — never two winners, and never a corrupted/
// half-written record from two processes' writes interleaving.
//
// PROVEN NOT VACUOUS: with fileutil.WithFlock's call removed from Store.Create
// (leaving only the in-process striped mutex, which provides zero protection
// across the process boundaries this test exercises), this test reliably
// observes MORE THAN ONE winner — every racer's os.Stat existence check runs
// unserialized, so several processes can observe "not yet created" before any
// of them finishes writing. Restoring fileutil.WithFlock makes exactly one
// winner the only possible outcome again, deterministically (the OS-level
// exclusive lock, not timing, is what the "with flock" pass relies on — see
// the qa-lead session's recorded before/after transcript for the run output).
//
// Traces to: docs/internal/architecture/ADR-054-entity-config-separation.md
// §5.1 ("Closed by two new tests" — extended here to cover Create, which the
// original two tests did not).
func TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner(t *testing.T) {
	if os.Getenv(crossProcCreateChildEnv) == "1" {
		runCreateRaceChild()
		return // unreachable — runCreateRaceChild calls os.Exit
	}

	const (
		numCreateRaceProcesses = 10
		sharedID               = "xproc-create-race"
	)

	dir := t.TempDir()
	barrierDir := t.TempDir()
	s := New[testEntity](dir, testAccessors())

	ctx, cancel := context.WithTimeout(context.Background(), childContextTimeout)
	defer cancel()

	cmds := make([]*exec.Cmd, numCreateRaceProcesses)
	bufs := make([]*bytes.Buffer, numCreateRaceProcesses)
	for i := 0; i < numCreateRaceProcesses; i++ {
		cmd := newChildCmd(ctx, "TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner", []string{
			crossProcCreateChildEnv + "=1",
			crossProcCreateIdxEnv + "=" + strconv.Itoa(i),
			crossProcDirEnv + "=" + dir,
			crossProcIDEnv + "=" + sharedID,
			crossProcBarrierDirEnv + "=" + barrierDir,
		})
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		cmds[i] = cmd
		bufs[i] = &buf
	}

	t.Cleanup(func() { reapChildren(cmds) })

	startAll(t, cmds)
	parentReleaseBarrier(t, barrierDir, numCreateRaceProcesses)

	var (
		winnerIdx  = -1
		numWinners int
		numLosers  int
		unexpected []string
	)
	for i, cmd := range cmds {
		err := cmd.Wait()
		out := bufs[i].String()
		switch {
		case err == nil && strings.Contains(out, "WIN"):
			numWinners++
			winnerIdx = i
		case err == nil:
			unexpected = append(unexpected, fmt.Sprintf("child %d: exited 0 without a WIN marker, output:\n%s", i, out))
		default:
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				numLosers++ // expected ErrAlreadyExists outcome
				continue
			}
			unexpected = append(unexpected, fmt.Sprintf("child %d: %v, output:\n%s", i, err, out))
		}
	}

	if len(unexpected) > 0 {
		t.Fatalf("%d of %d child processes behaved unexpectedly (neither won nor got ErrAlreadyExists):\n%s",
			len(unexpected), len(cmds), strings.Join(unexpected, "\n---\n"))
	}
	if numWinners != 1 {
		t.Fatalf("CROSS-PROCESS CREATE RACE: %d of %d processes reported a successful Create for the SAME id %q, "+
			"want exactly 1 (%d correctly got ErrAlreadyExists). The sidecar flock (fileutil.WithFlock in Store.Create) "+
			"failed to serialize the existence-check-then-write across process boundaries, exactly the guarantee "+
			"ADR-054 D3/D6 claims for Create.",
			numWinners, len(cmds), sharedID, numLosers)
	}
	if numLosers != numCreateRaceProcesses-1 {
		t.Fatalf("accounting mismatch: 1 winner + %d losers != %d processes", numLosers, numCreateRaceProcesses)
	}

	// The persisted record must be exactly the winner's payload — never a
	// corrupted/half-written mix of two processes' writes.
	final, err := s.Get(sharedID)
	if err != nil {
		t.Fatalf("Get after race: %v", err)
	}
	if final.ID != sharedID {
		t.Fatalf("final record ID = %q, want %q", final.ID, sharedID)
	}
	if final.CreatedAt.IsZero() {
		t.Fatalf("final record CreatedAt is zero — corrupted/half-written record")
	}
	wantPrefix := fmt.Sprintf("child-%d:", winnerIdx)
	wantLen := len(wantPrefix) + crossProcCreatePayloadBytes
	if !strings.HasPrefix(final.Name, wantPrefix) {
		t.Fatalf("final record Name does not start with the reported winner's own prefix %q (got first 40 bytes: %.40q) "+
			"— the persisted record does not match any single process's write", wantPrefix, final.Name)
	}
	if len(final.Name) != wantLen {
		t.Fatalf("final record Name length = %d, want %d — payload truncated or corrupted", len(final.Name), wantLen)
	}
	t.Logf("cross-process create race resolved to exactly one winner (child %d), record intact (%d bytes)",
		winnerIdx, len(final.Name))
}

// runCreateRaceChild is the child-mode implementation for the Create race.
// Every participant attempts exactly one Store.Create call for the shared ID;
// outcome is reported purely via exit code + stdout marker (see
// runCrossProcessChild's doc comment for why: the child's *testing.T is never
// observed by the parent).
func runCreateRaceChild() {
	dir := os.Getenv(crossProcDirEnv)
	id := os.Getenv(crossProcIDEnv)
	barrierDir := os.Getenv(crossProcBarrierDirEnv)
	idx, idxErr := strconv.Atoi(os.Getenv(crossProcCreateIdxEnv))
	if dir == "" || id == "" || barrierDir == "" || idxErr != nil {
		fmt.Fprintln(os.Stderr, "create-race child: missing or invalid env configuration")
		os.Exit(3)
	}

	if err := childAwaitBarrier(barrierDir, idx); err != nil {
		fmt.Fprintf(os.Stderr, "create-race child %d: barrier: %v\n", idx, err)
		os.Exit(3)
	}

	s := New[testEntity](dir, testAccessors())
	// Pad the payload (see crossProcCreatePayloadBytes) so the write side of
	// the critical section takes long enough to give siblings a realistic
	// chance to also clear the pre-write existence check while it's open.
	payload := fmt.Sprintf("child-%d:%s", idx, strings.Repeat("x", crossProcCreatePayloadBytes))
	e := &testEntity{ID: id, Name: payload}

	switch err := s.Create(e); {
	case err == nil:
		fmt.Printf("WIN %d\n", idx)
		os.Exit(0)
	case errors.Is(err, ErrAlreadyExists):
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "create-race child %d: unexpected Create error: %v\n", idx, err)
		os.Exit(2)
	}
}

// TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity closes
// GAP 1's Delete half: numDURaceUpdateProcesses independent OS processes each
// run a small Update loop (with the SAME in-mutate sleep widening technique
// runCrossProcessChild uses) against a shared entity, while one more process
// concurrently calls Store.Delete on it — all barrier-synchronized to start
// at once. Update and Delete share the exact same sidecar lock path for a
// given ID, so a correctly serialized store permits only two coherent
// outcomes: a Delete that lands before/between Updates causes every
// subsequent Update to observe ErrNotFound (never a resurrection), and a
// Delete that lands after all Updates simply removes the final state. EITHER
// way, once every process has finished, the entity MUST be gone — nothing in
// a correctly serialized store can recreate a file after Delete has removed
// it.
//
// PROVEN NOT VACUOUS: with fileutil.WithFlock's call removed from Store.Delete
// ONLY (Store.Update's own flock call is left intact, exactly mirroring how
// the review reproduced GAP 1 by stubbing Create/Delete but not Update), an
// Update goroutine that has already read the pre-delete state can have Delete
// remove the file out from under it mid-sleep, and then Update's write
// unconditionally recreates the file when it flushes its mutation —
// resurrecting an entity that Delete already reported as successfully
// removed. This test reliably observes the entity still existing afterward
// in that configuration. Restoring fileutil.WithFlock on Delete makes "gone
// after the race" the only possible outcome again. See the qa-lead session's
// recorded before/after transcript for the run output.
//
// Traces to: docs/internal/architecture/ADR-054-entity-config-separation.md
// §5.1 ("Closed by two new tests" — extended here to cover Delete-vs-Update,
// which the original two tests did not).
func TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity(t *testing.T) {
	if os.Getenv(crossProcDUChildEnv) == "1" {
		runDeleteUpdateRaceChild()
		return // unreachable — runDeleteUpdateRaceChild calls os.Exit
	}

	const (
		numDURaceUpdateProcesses = 5
		duUpdateIters            = 4
		duUpdateSleepMillis      = 20
		duID                     = "xproc-delete-update-race"
	)

	dir := t.TempDir()
	barrierDir := t.TempDir()
	s := New[testEntity](dir, testAccessors())
	if err := s.Create(&testEntity{ID: duID, Name: "0"}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), childContextTimeout)
	defer cancel()

	total := numDURaceUpdateProcesses + 1 // +1 delete process
	cmds := make([]*exec.Cmd, 0, total)
	bufs := make([]*bytes.Buffer, 0, total)

	newDUCmd := func(idx int, role string) *exec.Cmd {
		return newChildCmd(ctx, "TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity", []string{
			crossProcDUChildEnv + "=1",
			crossProcDURoleEnv + "=" + role,
			crossProcDUIdxEnv + "=" + strconv.Itoa(idx),
			crossProcDirEnv + "=" + dir,
			crossProcIDEnv + "=" + duID,
			crossProcBarrierDirEnv + "=" + barrierDir,
			crossProcItersEnv + "=" + strconv.Itoa(duUpdateIters),
			crossProcSleepMsEnv + "=" + strconv.Itoa(duUpdateSleepMillis),
		})
	}

	idx := 0
	for i := 0; i < numDURaceUpdateProcesses; i++ {
		cmd := newDUCmd(idx, "update")
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		cmds = append(cmds, cmd)
		bufs = append(bufs, &buf)
		idx++
	}
	deleteCmd := newDUCmd(idx, "delete")
	var deleteBuf bytes.Buffer
	deleteCmd.Stdout, deleteCmd.Stderr = &deleteBuf, &deleteBuf
	cmds = append(cmds, deleteCmd)
	bufs = append(bufs, &deleteBuf)

	t.Cleanup(func() { reapChildren(cmds) })

	startAll(t, cmds)
	parentReleaseBarrier(t, barrierDir, len(cmds))

	var failures []string
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			failures = append(failures, fmt.Sprintf("child %d failed: %v\noutput:\n%s", i, err, bufs[i].String()))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d child processes failed:\n%s", len(failures), len(cmds), strings.Join(failures, "\n---\n"))
	}
	if !strings.Contains(deleteBuf.String(), "DELETED") {
		t.Fatalf("delete child did not report success; output:\n%s", deleteBuf.String())
	}

	if s.Exists(duID) {
		t.Fatalf("CROSS-PROCESS DELETE/UPDATE RACE: entity %q still exists after the Delete process reported success — "+
			"a concurrent Update resurrected it after deletion. The sidecar flock (fileutil.WithFlock, shared between "+
			"Store.Update and Store.Delete on the same lockPath) failed to serialize Delete against in-flight Updates, "+
			"exactly the guarantee ADR-054 D3 claims.", duID)
	}
	t.Logf("cross-process delete/update race resolved with no resurrection: entity correctly absent after Delete")
}

// runDeleteUpdateRaceChild is the child-mode implementation for the
// Delete-vs-Update race, dispatching on crossProcDURoleEnv.
func runDeleteUpdateRaceChild() {
	dir := os.Getenv(crossProcDirEnv)
	id := os.Getenv(crossProcIDEnv)
	role := os.Getenv(crossProcDURoleEnv)
	barrierDir := os.Getenv(crossProcBarrierDirEnv)
	idx, idxErr := strconv.Atoi(os.Getenv(crossProcDUIdxEnv))
	if dir == "" || id == "" || barrierDir == "" || idxErr != nil || (role != "update" && role != "delete") {
		fmt.Fprintln(os.Stderr, "delete/update-race child: missing or invalid env configuration")
		os.Exit(3)
	}

	if err := childAwaitBarrier(barrierDir, idx); err != nil {
		fmt.Fprintf(os.Stderr, "delete/update-race child %d (%s): barrier: %v\n", idx, role, err)
		os.Exit(3)
	}

	s := New[testEntity](dir, testAccessors())

	if role == "delete" {
		if err := s.Delete(id); err != nil {
			fmt.Fprintf(os.Stderr, "delete child: Delete failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("DELETED")
		os.Exit(0)
	}

	// role == "update"
	iters, itersErr := strconv.Atoi(os.Getenv(crossProcItersEnv))
	sleepMs, sleepErr := strconv.Atoi(os.Getenv(crossProcSleepMsEnv))
	if itersErr != nil || sleepErr != nil {
		fmt.Fprintln(os.Stderr, "update child: missing or invalid iters/sleep configuration")
		os.Exit(3)
	}
	sleepDur := time.Duration(sleepMs) * time.Millisecond

	for i := 0; i < iters; i++ {
		_, err := s.Update(id, func(t *testEntity) error {
			cur, parseErr := strconv.Atoi(strings.TrimSpace(t.Name))
			if parseErr != nil {
				return fmt.Errorf("parse counter %q: %w", t.Name, parseErr)
			}
			// Widen the read-modify-write window (same technique
			// runCrossProcessChild uses) so the racing Delete has a
			// realistic chance to land mid-cycle.
			time.Sleep(sleepDur)
			cur++
			t.Name = strconv.Itoa(cur)
			return nil
		})
		if err == nil {
			continue
		}
		if errors.Is(err, ErrNotFound) {
			// Expected once the racing Delete has won: the entity is gone,
			// nothing left to update. Not a failure — this IS the correctly
			// serialized outcome, not a bug.
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "update child %d: Update #%d failed: %v\n", idx, i, err)
		os.Exit(1)
	}
	os.Exit(0)
}
