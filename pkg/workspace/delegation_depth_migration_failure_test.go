// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Failure-path tests for the one-time delegation-depth migration (round 5,
// CHECK findings R1, R2 and R4 on head 53feea69b).
//
// The oracle is the founder rule (decision 2026-11-08, Q8 B), not the
// implementation: a stored 3 becomes 10 once; a 3 an operator saves AFTER the
// migration rewrote that record must never be rewritten; a record whose
// rewrite did not land must not be marked done and is retried; exactly one
// migration summary per install. "Complete" is observed behaviourally — a
// later call reports Ran=false and touches nothing — never by parsing the
// marker's internal format. Faults are injected only at the filesystem write
// seam (writeFileAtomicFn), with real permission denial and real separate
// processes where the finding is about the real filesystem or real processes.

package workspace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// fileutilWriteFileAtomicForTest is the real production writer, used to clear
// an injected fault mid-test (the "fault cleared, next boot" step).
var fileutilWriteFileAtomicForTest = fileutil.WriteFileAtomic

// storedDepth returns the explicit depth of the single edge in id's record.
func storedDepth(t *testing.T, home, id string) int {
	t.Helper()
	rec, _ := readDelegationStoreRaw(t, home, id)
	require.Len(t, rec.Delegation, 1, "%s: want exactly one edge", id)
	require.NotNil(t, rec.Delegation[0].Depth, "%s: edge must keep an explicit depth", id)
	return *rec.Delegation[0].Depth
}

// operatorSavesThree is an operator's deliberate PUT of depth 3, through the
// canonical writer under the caller lock its contract requires.
func operatorSavesThree(t *testing.T, home, id string) {
	t.Helper()
	unlock := LockID(id)
	err := SaveDelegation(home, id, []DelegationEdge{{FromAgent: "jim", ToAgent: "ava", Depth: ip(3)}})
	unlock()
	require.NoError(t, err)
}

func seedThree(t *testing.T, home string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		writeDelegationStore(t, home, id, []DelegationEdge{{FromAgent: "jim", ToAgent: "ava", Depth: ip(3)}})
	}
}

// isRecordPath reports whether path is a delegation record (not the marker,
// not a lock sidecar) under home's store directory.
func isRecordPath(home, path string) bool {
	return filepath.Dir(path) == DelegationStoreDir(home) && strings.HasSuffix(path, ".json")
}

// injectWriteFault replaces the filesystem write seam for the test's duration.
func injectWriteFault(t *testing.T, fn func(orig func(string, []byte, os.FileMode) error, path string, data []byte, perm os.FileMode) error) {
	t.Helper()
	orig := writeFileAtomicFn
	t.Cleanup(func() { writeFileAtomicFn = orig })
	writeFileAtomicFn = func(path string, data []byte, perm os.FileMode) error {
		return fn(orig, path, data, perm)
	}
}

func countSummaryLines(logText string) int {
	return strings.Count(logText, migrationLogLine+":")
}

// R1: the records were rewritten, but recording completion failed. Boot
// continues (best-effort), the operator sees 10 and deliberately saves 3 on
// both workspaces; the next boot must keep both 3s, then the migration is
// complete with exactly one summary line overall.
func TestDelegationDepthMigration_CompletionWriteFails_LaterThreeSurvives(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a", "ws-b")

	recordWrites := 0
	injectWriteFault(t, func(orig func(string, []byte, os.FileMode) error, path string, data []byte, perm os.FileMode) error {
		if isRecordPath(home, path) {
			recordWrites++
			return orig(path, data, perm)
		}
		if filepath.Base(path) == delegationDepthMigrationMarker && recordWrites == 2 {
			return &os.PathError{Op: "rename", Path: path, Err: syscall.EACCES}
		}
		return orig(path, data, perm)
	})

	var firstErr error
	logs := captureMigrationLog(t, func() {
		_, firstErr = MigrateDelegationDepthDefaultOnce(home)
	})
	require.Error(t, firstErr, "a failed completion write must be surfaced to the boot hook, not swallowed")
	assert.ErrorIs(t, firstErr, syscall.EACCES)
	assert.Equal(t, 2, recordWrites, "both records must have been rewritten before completion was recorded")
	assert.Equal(t, 10, storedDepth(t, home, "ws-a"))
	assert.Equal(t, 10, storedDepth(t, home, "ws-b"))

	// Boot continued; the operator now deliberately tightens both back to 3.
	writeFileAtomicFn = fileutilWriteFileAtomicForTest
	operatorSavesThree(t, home, "ws-a")
	operatorSavesThree(t, home, "ws-b")

	// Next boot, fault cleared.
	logs += captureMigrationLog(t, func() {
		_, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
	})
	assert.Equal(t, 3, storedDepth(t, home, "ws-a"), "a 3 saved after the migration rewrote ws-a must never be rewritten")
	assert.Equal(t, 3, storedDepth(t, home, "ws-b"), "a 3 saved after the migration rewrote ws-b must never be rewritten")

	// The migration is now complete: a further boot is a silent no-op.
	var third DelegationDepthMigrationResult
	logs += captureMigrationLog(t, func() {
		var err error
		third, err = MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
	})
	assert.False(t, third.Ran, "after recovery the migration must be complete")
	assert.Equal(t, 3, storedDepth(t, home, "ws-a"))
	assert.Equal(t, 3, storedDepth(t, home, "ws-b"))
	assert.Equal(t, 1, countSummaryLines(logs), "exactly one migration summary per install, got:\n%s", logs)
}

// R1 boundary: when even the first durable write of the migration fails, no
// record may be rewritten — otherwise the operator would see a 10 that the
// install has no durable record of having produced.
func TestDelegationDepthMigration_FirstDurableWriteFails_NothingRewritten(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a")
	_, before := readDelegationStoreRaw(t, home, "ws-a")

	injectWriteFault(t, func(orig func(string, []byte, os.FileMode) error, path string, data []byte, perm os.FileMode) error {
		if filepath.Base(path) == delegationDepthMigrationMarker {
			return &os.PathError{Op: "rename", Path: path, Err: syscall.EACCES}
		}
		return orig(path, data, perm)
	})

	_, err := MigrateDelegationDepthDefaultOnce(home)
	require.Error(t, err)
	assert.ErrorIs(t, err, syscall.EACCES)
	_, after := readDelegationStoreRaw(t, home, "ws-a")
	assert.Equal(t, string(before), string(after),
		"no record may be rewritten while the migration cannot durably record its progress")

	// Fault cleared: nothing was ever rewritten, so the stored 3 is still a
	// pre-migration 3 (the founder-accepted class) and migrates once.
	writeFileAtomicFn = fileutilWriteFileAtomicForTest
	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.True(t, res.Ran)
	assert.Equal(t, 10, storedDepth(t, home, "ws-a"))
	res2, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res2.Ran)
}

// Nothing to migrate, but completion cannot be recorded: the failure must be
// surfaced and the run must not claim completion (no summary line), so the
// install still ends with exactly one summary once the write succeeds.
func TestDelegationDepthMigration_NothingToMigrate_CompletionWriteFails(t *testing.T) {
	home := t.TempDir()
	writeDelegationStore(t, home, "ws-five", []DelegationEdge{{FromAgent: "jim", ToAgent: "ava", Depth: ip(5)}})

	injectWriteFault(t, func(orig func(string, []byte, os.FileMode) error, path string, data []byte, perm os.FileMode) error {
		if filepath.Base(path) == delegationDepthMigrationMarker {
			return &os.PathError{Op: "rename", Path: path, Err: syscall.ENOSPC}
		}
		return orig(path, data, perm)
	})

	var err error
	logs := captureMigrationLog(t, func() {
		_, err = MigrateDelegationDepthDefaultOnce(home)
	})
	require.Error(t, err, "a completion that could not be recorded must be surfaced")
	assert.ErrorIs(t, err, syscall.ENOSPC)
	assert.Zero(t, countSummaryLines(logs), "an unrecorded completion must not log the once-per-install summary")

	writeFileAtomicFn = fileutilWriteFileAtomicForTest
	logs += captureMigrationLog(t, func() {
		res, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
		assert.True(t, res.Ran)
		res2, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
		assert.False(t, res2.Ran)
	})
	assert.Equal(t, 1, countSummaryLines(logs), "exactly one summary per install, got:\n%s", logs)
	assert.Equal(t, 5, storedDepth(t, home, "ws-five"))
}

// R2 (injected): one valid record's rewrite does not land. The run must report
// the failure and must not complete; the next boot retries ONLY that record,
// so a 3 the operator saves in between on an already-migrated record survives.
func TestDelegationDepthMigration_RecordWriteFails_RetriedWithoutReMigrating(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a", "ws-b")
	_, aBefore := readDelegationStoreRaw(t, home, "ws-a")

	injectWriteFault(t, func(orig func(string, []byte, os.FileMode) error, path string, data []byte, perm os.FileMode) error {
		if filepath.Base(path) == "ws-a.json" {
			return &os.PathError{Op: "rename", Path: path, Err: syscall.EROFS}
		}
		return orig(path, data, perm)
	})

	_, err := MigrateDelegationDepthDefaultOnce(home)
	require.Error(t, err, "a valid record whose rewrite did not land must be reported, not counted as done")
	assert.ErrorIs(t, err, syscall.EROFS)
	assert.Contains(t, err.Error(), "ws-a", "the error must name the record that was not migrated")
	_, aAfter := readDelegationStoreRaw(t, home, "ws-a")
	assert.Equal(t, string(aBefore), string(aAfter), "the failed record's bytes must survive")
	assert.Equal(t, 10, storedDepth(t, home, "ws-b"))

	// Operator deliberately tightens the already-migrated ws-b back to 3.
	writeFileAtomicFn = fileutilWriteFileAtomicForTest
	operatorSavesThree(t, home, "ws-b")

	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.True(t, res.Ran, "the unfinished migration must run again on the next boot")
	assert.Equal(t, 10, storedDepth(t, home, "ws-a"), "the record that failed must be retried")
	assert.Equal(t, 3, storedDepth(t, home, "ws-b"), "an already-migrated record must not be migrated twice")

	res2, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res2.Ran)
	assert.Equal(t, 3, storedDepth(t, home, "ws-b"))
}

// R2 (real filesystem): the record's lock sidecar cannot be opened for
// writing (chmod 0400) while the JSON stays readable and valid.
func TestDelegationDepthMigration_RecordLockDenied_NotMarkedDone(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a")
	path, err := DelegationStorePath(home, "ws-a")
	require.NoError(t, err)
	lock := path + ".lock"
	require.NoError(t, os.WriteFile(lock, nil, 0o400))
	require.NoError(t, os.Chmod(lock, 0o400))
	t.Cleanup(func() { _ = os.Chmod(lock, 0o600) })
	// Instrument control: this host must really deny the open (not root).
	f, ctlErr := os.OpenFile(lock, os.O_RDWR, 0)
	if f != nil {
		_ = f.Close()
	}
	require.ErrorIs(t, ctlErr, os.ErrPermission, "instrument control: the 0400 lock sidecar must deny write-open on this host")
	_, before := readDelegationStoreRaw(t, home, "ws-a")

	_, err = MigrateDelegationDepthDefaultOnce(home)
	require.Error(t, err, "a record that could not be written must not let the migration complete silently")
	assert.ErrorIs(t, err, os.ErrPermission)
	_, after := readDelegationStoreRaw(t, home, "ws-a")
	assert.Equal(t, string(before), string(after))

	require.NoError(t, os.Chmod(lock, 0o600))
	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.True(t, res.Ran, "after the permission recovers the migration must still be pending")
	assert.Equal(t, 10, storedDepth(t, home, "ws-a"))
	res2, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res2.Ran)
}

// R1 (real process exit): the process dies right after rewriting the first
// record. The operator then saves 3 on that record; the next boot must keep
// it and still migrate the other record.
func TestDelegationDepthMigration_ProcessDiesAfterFirstRewrite_LaterThreeSurvives(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a", "ws-b")

	out, err := depthMigrationChild(t, home, "exit-after-first-record").CombinedOutput()
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr) && exitErr.ExitCode() == 73,
		"child must die by its own exit 73 right after the first record write; err=%v output=%s", err, out)

	a, b := storedDepth(t, home, "ws-a"), storedDepth(t, home, "ws-b")
	require.ElementsMatch(t, []int{3, 10}, []int{a, b}, "exactly one record must have been rewritten before the exit")
	rewritten, untouched := "ws-a", "ws-b"
	if b == 10 {
		rewritten, untouched = "ws-b", "ws-a"
	}
	operatorSavesThree(t, home, rewritten)

	_, err = MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.Equal(t, 3, storedDepth(t, home, rewritten), "a 3 saved after the record was migrated must never be rewritten")
	assert.Equal(t, 10, storedDepth(t, home, untouched), "the record the dead process never reached must still be migrated")
	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res.Ran)
}

// R4: two real processes share one install. The child is held inside its
// migration (mid record write). The parent's migration must not run alongside
// it; across both processes exactly one reports Ran and exactly one summary
// line is logged.
func TestDelegationDepthMigration_TwoProcessesExactlyOneRuns(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a")

	cmd := depthMigrationChild(t, home, "pause-in-record-write")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "DEPTH_MIGRATION_CHILD_READY" {
			ready = true
			break
		}
	}
	require.True(t, ready, "child never reached its in-migration pause; stderr=%s", stderr.String())

	type outcome struct {
		res DelegationDepthMigrationResult
		err error
	}
	parentDone := make(chan outcome, 1)
	parentLog := filepath.Join(t.TempDir(), "parent.log")
	require.NoError(t, logger.EnableFileLogging(parentLog))
	t.Cleanup(logger.DisableFileLogging)
	go func() {
		r, e := MigrateDelegationDepthDefaultOnce(home)
		parentDone <- outcome{r, e}
	}()

	select {
	case o := <-parentDone:
		t.Errorf("parent migration finished (%+v, %v) while the other process was still inside its migration", o.res, o.err)
		parentDone <- o
	case <-time.After(750 * time.Millisecond):
	}

	_, err = io.WriteString(stdin, "continue\n")
	require.NoError(t, err)
	for scanner.Scan() {
		t.Logf("child: %s", scanner.Text())
	}
	require.NoError(t, cmd.Wait(), "child failed; stderr=%s", stderr.String())

	var parent outcome
	select {
	case parent = <-parentDone:
	case <-time.After(30 * time.Second):
		t.Fatal("parent migration never returned after the child finished")
	}
	logger.DisableFileLogging()
	require.NoError(t, parent.err)

	var child childMigrationReport
	data, err := os.ReadFile(filepath.Join(home, "child-result.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &child))

	runs := 0
	for _, ran := range []bool{parent.res.Ran, child.Ran} {
		if ran {
			runs++
		}
	}
	assert.Equal(t, 1, runs, "exactly one process may run the migration; parent=%+v child=%+v", parent.res, child)
	parentText, err := os.ReadFile(parentLog)
	require.NoError(t, err)
	childText, err := os.ReadFile(filepath.Join(home, "child.log"))
	require.NoError(t, err)
	assert.Equal(t, 1, countSummaryLines(string(parentText))+countSummaryLines(string(childText)),
		"exactly one summary line across both processes; parent:\n%s\nchild:\n%s", parentText, childText)
	assert.Equal(t, 10, storedDepth(t, home, "ws-a"))

	// After both boots, a deliberate 3 survives every later boot.
	operatorSavesThree(t, home, "ws-a")
	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res.Ran)
	assert.Equal(t, 3, storedDepth(t, home, "ws-a"))
}

// A marker that cannot be understood must fail closed: report, rewrite nothing.
func TestDelegationDepthMigration_UnreadableMarkerFailsClosed(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a")
	_, before := readDelegationStoreRaw(t, home, "ws-a")
	require.NoError(t, os.WriteFile(migrationMarkerPath(home), []byte("{not a ledger"), 0o600))

	_, err := MigrateDelegationDepthDefaultOnce(home)
	require.Error(t, err, "an unparseable marker must be surfaced, not treated as permission to rewrite")
	_, after := readDelegationStoreRaw(t, home, "ws-a")
	assert.Equal(t, string(before), string(after))
}

// The round-4 build wrote a plain "done" stamp; it must keep meaning done.
func TestDelegationDepthMigration_RoundFourMarkerMeansDone(t *testing.T) {
	home := t.TempDir()
	seedThree(t, home, "ws-a")
	require.NoError(t, os.WriteFile(migrationMarkerPath(home),
		[]byte(`{"legacy_depth": 3, "raised_to": 10, "migrated_at": "2026-11-08T16:00:00Z"}`+"\n"), 0o600))

	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res.Ran)
	assert.Equal(t, 3, storedDepth(t, home, "ws-a"), "a 3 saved after a round-4 migration must survive")
}

func depthMigrationChild(t *testing.T, home, mode string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^TestDelegationDepthMigrationChildProcess$", "-test.v", "-test.timeout=0")
	cmd.Env = append(os.Environ(), "DEPTH_MIGRATION_CHILD_HOME="+home, "DEPTH_MIGRATION_CHILD_MODE="+mode)
	return cmd
}

// TestDelegationDepthMigrationChildProcess is the separate-process entry used
// by the tests above; without its environment it does nothing.
func TestDelegationDepthMigrationChildProcess(t *testing.T) {
	mode, home := os.Getenv("DEPTH_MIGRATION_CHILD_MODE"), os.Getenv("DEPTH_MIGRATION_CHILD_HOME")
	if mode == "" {
		return
	}
	paused := false
	orig := writeFileAtomicFn
	writeFileAtomicFn = func(path string, data []byte, perm os.FileMode) error {
		if !isRecordPath(home, path) {
			return orig(path, data, perm)
		}
		if mode == "pause-in-record-write" && !paused {
			paused = true
			fmt.Fprintln(os.Stdout, "DEPTH_MIGRATION_CHILD_READY")
			var token string
			if _, err := fmt.Fscanln(os.Stdin, &token); err != nil || token != "continue" {
				return fmt.Errorf("child handshake: token=%q: %w", token, err)
			}
		}
		if err := orig(path, data, perm); err != nil {
			return err
		}
		if mode == "exit-after-first-record" {
			os.Exit(73)
		}
		return nil
	}
	require.NoError(t, logger.EnableFileLogging(filepath.Join(home, "child.log")))
	res, err := MigrateDelegationDepthDefaultOnce(home)
	logger.DisableFileLogging()
	require.NoError(t, err)
	data, err := json.Marshal(childMigrationReport{Ran: res.Ran, EdgesMigrated: res.EdgesMigrated})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "child-result.json"), data, 0o600))
}

// childMigrationReport carries the child process's result to the parent.
type childMigrationReport struct {
	Ran           bool `json:"ran"`
	EdgesMigrated int  `json:"edges_migrated"`
}
