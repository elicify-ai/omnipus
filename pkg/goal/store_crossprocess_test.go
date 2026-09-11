// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

// Package goal — the cross-process half of GOAL-FR-008's store guarantee,
// proven for THIS package's own Store wrapper rather than re-deriving
// pkg/entity's own proof (pkg/entity/store_crossprocess_test.go already
// proves the underlying entity.Store[T] guarantee with real forked
// processes; that proof is not re-run here — see doc.go: "pkg/entity/store.go
// is reused AS-IS and is never forked").
//
// What THIS file closes is a narrower, wrapper-specific gap: Store.Create
// does MORE than entity.Store.Create before it ever reaches the sidecar
// flock — it calls g.Validate() and, for a task-owned goal, GetByOwner's own
// List() scan (R-04's one-goal-per-task check) — entirely OUTSIDE the
// goalLockAcquireFn/goalLockReleaseFn-bracketed critical section. A mistake
// that moved the actual entity.Store.Create call earlier, or that performed
// the pre-check AFTER the flock instead of before, could silently reopen the
// same "more than one winner" race pkg/entity's own test proves closed for
// the underlying primitive. This test races real OS processes creating a
// session-owned goal (deliberately NOT task-owned, to isolate the wrapper's
// OWN pre-check from R-04's separate, already-covered-by-unit-tests
// uniqueness rule — see predicate_test.go's
// TestCreate_RefusesSecondGoalForSameTask) under the SAME goal id, and
// asserts the pkg/entity-level guarantee (exactly one winner, no corrupted
// record) still holds through this wrapper.
package goal

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

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const (
	xprocChildEnv      = "OMNIPUS_GOAL_XPROC_CHILD"
	xprocDirEnv        = "OMNIPUS_GOAL_XPROC_DIR"
	xprocIDEnv         = "OMNIPUS_GOAL_XPROC_ID"
	xprocIdxEnv        = "OMNIPUS_GOAL_XPROC_IDX"
	xprocBarrierDirEnv = "OMNIPUS_GOAL_XPROC_BARRIER_DIR"

	xprocChildTestTimeoutFlag = "-test.timeout=60s"
	xprocChildContextTimeout  = 90 * time.Second
)

func xprocNewChildCmd(ctx context.Context, testName string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^"+testName+"$",
		"-test.count=1",
		xprocChildTestTimeoutFlag,
	)
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

func xprocReapChildren(cmds []*exec.Cmd) {
	for _, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
}

func xprocStartAll(t *testing.T, cmds []*exec.Cmd) {
	t.Helper()
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child process %d: %v", i, err)
		}
	}
}

func xprocChildAwaitBarrier(barrierDir string, idx int) error {
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

func xprocParentReleaseBarrier(t *testing.T, barrierDir string, n int) {
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

// TestCrossProcess_GoalStoreCreateRaceOnSameID_ExactlyOneWinner races real
// OS processes calling goal.Store.Create with the SAME, pre-assigned
// session-owned goal id. Store.Create never silently overwrites (it delegates
// to entity.Store.Create, which write-then-verifies), so the correct
// outcome regardless of scheduling is: exactly one process observes a nil
// error, every other process observes an error wrapping entity.ErrAlreadyExists,
// and the persisted record is exactly the winner's own payload.
func TestCrossProcess_GoalStoreCreateRaceOnSameID_ExactlyOneWinner(t *testing.T) {
	if os.Getenv(xprocChildEnv) == "1" {
		runGoalCreateRaceChild()
		return // unreachable — runGoalCreateRaceChild calls os.Exit
	}

	const (
		numRaceProcesses = 8
		sharedGoalID     = "xproc-goal-create-race"
	)

	home := t.TempDir()
	barrierDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), xprocChildContextTimeout)
	defer cancel()

	cmds := make([]*exec.Cmd, numRaceProcesses)
	bufs := make([]*bytes.Buffer, numRaceProcesses)
	for i := 0; i < numRaceProcesses; i++ {
		cmd := xprocNewChildCmd(ctx, "TestCrossProcess_GoalStoreCreateRaceOnSameID_ExactlyOneWinner", []string{
			xprocChildEnv + "=1",
			xprocIdxEnv + "=" + strconv.Itoa(i),
			xprocDirEnv + "=" + home,
			xprocIDEnv + "=" + sharedGoalID,
			xprocBarrierDirEnv + "=" + barrierDir,
		})
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		cmds[i] = cmd
		bufs[i] = &buf
	}
	t.Cleanup(func() { xprocReapChildren(cmds) })

	xprocStartAll(t, cmds)
	xprocParentReleaseBarrier(t, barrierDir, numRaceProcesses)

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
				numLosers++ // expected already-exists outcome
				continue
			}
			unexpected = append(unexpected, fmt.Sprintf("child %d: %v, output:\n%s", i, err, out))
		}
	}

	if len(unexpected) > 0 {
		t.Fatalf("%d of %d child processes behaved unexpectedly (neither won nor lost cleanly):\n%s",
			len(unexpected), len(cmds), strings.Join(unexpected, "\n---\n"))
	}
	if numWinners != 1 {
		t.Fatalf("CROSS-PROCESS GOAL CREATE RACE: %d of %d processes reported a successful Create for the SAME goal id %q, "+
			"want exactly 1 (%d correctly lost). goal.Store.Create's own pre-checks must not have widened the race window "+
			"the underlying pkg/entity sidecar flock closes.",
			numWinners, len(cmds), sharedGoalID, numLosers)
	}
	if numLosers != numRaceProcesses-1 {
		t.Fatalf("accounting mismatch: 1 winner + %d losers != %d processes", numLosers, numRaceProcesses)
	}

	s := NewStore(home)
	final, err := s.Get(sharedGoalID)
	if err != nil {
		t.Fatalf("Get after race: %v", err)
	}
	if final.GoalID != sharedGoalID {
		t.Fatalf("final record GoalID = %q, want %q", final.GoalID, sharedGoalID)
	}
	wantPrompt := fmt.Sprintf("child-%d", winnerIdx)
	if final.Prompt != wantPrompt {
		t.Fatalf("final record Prompt = %q, does not match the reported winner's own payload (want %q) — "+
			"the persisted record does not match any single process's write", final.Prompt, wantPrompt)
	}
	t.Logf("cross-process goal create race resolved to exactly one winner (child %d), record intact", winnerIdx)
}

// runGoalCreateRaceChild is the child-mode implementation. Every participant
// attempts exactly one goal.Store.Create call for the shared goal id;
// outcome is reported purely via exit code + stdout marker, since the
// child's *testing.T is never observed by the parent.
func runGoalCreateRaceChild() {
	home := os.Getenv(xprocDirEnv)
	id := os.Getenv(xprocIDEnv)
	barrierDir := os.Getenv(xprocBarrierDirEnv)
	idx, idxErr := strconv.Atoi(os.Getenv(xprocIdxEnv))
	if home == "" || id == "" || barrierDir == "" || idxErr != nil {
		fmt.Fprintln(os.Stderr, "goal create-race child: missing or invalid env configuration")
		os.Exit(3)
	}

	if err := xprocChildAwaitBarrier(barrierDir, idx); err != nil {
		fmt.Fprintf(os.Stderr, "goal create-race child %d: barrier: %v\n", idx, err)
		os.Exit(3)
	}

	os.Exit(childCreateAndReport(home, id, idx))
}

// childCreateAndReport builds a valid session-owned goal, force-assigns the
// shared id (bypassing the normal auto-assign path — every racer must
// target the SAME id for this to be a same-id race at all), and reports the
// outcome via exit code + stdout marker: 0 with a "WIN" line on the winning
// Create, 1 for the expected already-exists loss, 2 for any other error.
func childCreateAndReport(home, id string, idx int) int {
	s := NewStore(home)
	criterion := task.AcceptanceCriterion{
		Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the tests pass",
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "xproc"}, Status: task.CritPending,
	}
	dod := task.AcceptanceCriterion{
		Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "no secrets leaked",
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "xproc"}, Status: task.CritPending,
	}
	g, err := New(
		generated.GoalOwnerKindSession, "xproc-owner-session", generated.ChatCompiled,
		fmt.Sprintf("child-%d", idx), "",
		[]task.AcceptanceCriterion{criterion},
		[]task.AcceptanceCriterion{dod},
		20, time.Now().UTC(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goal create-race child %d: New: %v\n", idx, err)
		return 3
	}
	g.GoalID = id // force every racer onto the SAME id

	switch createErr := s.Create(g); {
	case createErr == nil:
		fmt.Printf("WIN %d\n", idx)
		return 0
	case errors.Is(createErr, entity.ErrAlreadyExists):
		return 1
	default:
		fmt.Fprintf(os.Stderr, "goal create-race child %d: unexpected Create error: %v\n", idx, createErr)
		return 2
	}
}
