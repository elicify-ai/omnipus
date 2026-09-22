// task_executor_run_test.go: tests for run one task attempt to completion

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- moved from task_executor.go tests 2026-09-15 ---

// TestBuildPrompt_InstructionEchoNeverResolvesToSuccess is the review B2
// regression-proof lock on buildPrompt's echo-safety property (ADR-043
// §2.4 / review B1): if a model paraphrases or echoes buildPrompt's own
// instruction text verbatim as its "final message" — without ever emitting a
// genuine completion signal of its own — parsing that echoed text must NEVER
// resolve to verdictSuccess. At worst it fails closed (verdictNotFound) or
// resolves to verdictFailure (the deliberately "safe direction" per B1's
// ordering of the two TASK_STATUS lines), but never verdictSuccess. Proven
// for both dispatch kinds since buildPrompt's dispatch branch means native
// (marker + task_update instruction) and external-CLI (marker only) produce
// different final strings.
func TestBuildPrompt_InstructionEchoNeverResolvesToSuccess(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
		tk := newCompletionContractTask(t, al, "native-agent", "echo-safety native")
		prompt := al.taskExecutor.buildPrompt(tk)
		sig := parseTaskCompletionSignal(prompt)
		if sig.Verdict == verdictSuccess {
			t.Fatalf("parsing buildPrompt's own raw instruction text resolved to verdictSuccess "+
				"(echo-unsafe) — prompt:\n%s", prompt)
		}
	})
	t.Run("external_cli", func(t *testing.T) {
		al, _ := newExternalCLITaskTestLoop(t, &countingProvider{})
		tk := newCompletionContractTask(t, al, "ext-agent", "echo-safety external")
		prompt := al.taskExecutor.buildPrompt(tk)
		sig := parseTaskCompletionSignal(prompt)
		if sig.Verdict == verdictSuccess {
			t.Fatalf("parsing buildPrompt's own raw instruction text resolved to verdictSuccess "+
				"(echo-unsafe) — prompt:\n%s", prompt)
		}
	})
	// bulleted_paraphrase is hardening review finding H2's echo-safety
	// extension: a model doesn't always echo buildPrompt's instruction lines
	// verbatim — a real, observed reformatting is to restate them as a
	// bulleted list (e.g. summarizing its plan as "* TASK_STATUS: success -
	// if all tests pass" / "* TASK_STATUS: failure - otherwise"). Before the
	// bullet-exclusion fix, the SUCCESS bullet's leading "* " was just more
	// whitespace to the wrapper regex, so this paraphrase could resolve to
	// verdictSuccess purely from quoted instruction text. It must now fail
	// closed (verdictNotFound) or resolve to verdictFailure (the parser's
	// existing safe-direction ordering), but never verdictSuccess.
	t.Run("bulleted_paraphrase", func(t *testing.T) {
		al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
		tk := newCompletionContractTask(t, al, "native-agent", "echo-safety bulleted paraphrase")
		prompt := al.taskExecutor.buildPrompt(tk)
		bulleted := strings.ReplaceAll(prompt,
			"TASK_STATUS: success", "* `TASK_STATUS: success` - if all tests pass")
		bulleted = strings.ReplaceAll(bulleted,
			"TASK_STATUS: failure", "* `TASK_STATUS: failure` - otherwise")
		sig := parseTaskCompletionSignal(bulleted)
		if sig.Verdict == verdictSuccess {
			t.Fatalf("parsing a bulleted paraphrase of buildPrompt's instruction text resolved to "+
				"verdictSuccess (echo-unsafe) — prompt:\n%s", bulleted)
		}
	})
}

// TestBuildPrompt_TeachesTheOneClaimPathPerDispatchKind pins the founder
// decision of 2026-09-14: a native worker is taught goal_claim and is NOT
// taught the prose completion markers; a subagent_3p worker, whose CLI cannot
// call Omnipus tools, is taught the evidence line and the marker.
func TestBuildPrompt_TeachesTheOneClaimPathPerDispatchKind(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
		tk := newCompletionContractTask(t, al, "native-agent", "claim teaching native")
		prompt := al.taskExecutor.buildPrompt(tk)
		if !strings.Contains(prompt, "goal_claim") {
			t.Fatalf("native prompt does not teach goal_claim:\n%s", prompt)
		}
		if strings.Contains(prompt, taskStatusLabel) || strings.Contains(prompt, goalEvidenceLabel) {
			t.Fatalf("native prompt still teaches the prose markers:\n%s", prompt)
		}
		if strings.Contains(prompt, "update_task") {
			t.Fatalf("native prompt still offers update_task as a way to finish:\n%s", prompt)
		}
	})
	t.Run("external_cli", func(t *testing.T) {
		al, _ := newExternalCLITaskTestLoop(t, &countingProvider{})
		tk := newCompletionContractTask(t, al, "ext-agent", "claim teaching external")
		prompt := al.taskExecutor.buildPrompt(tk)
		if !strings.Contains(prompt, goalEvidenceLabel) || !strings.Contains(prompt, taskStatusLabel) {
			t.Fatalf("external-CLI prompt must teach the evidence line and the marker:\n%s", prompt)
		}
	})
}

// TestCloseRun_DuplicateCloseAfterAlreadyClosedLogsInfoNotError is the
// regression for delta-review Fix 2 (2026-07-20): a SECOND closeRun call for
// a run that is already terminal — e.g. runTask's top-level panic-recovery
// defer re-invoking closeRun after a panic in POST-completion housekeeping
// (onTaskComplete / deliverTaskCompletionUpward) that runs AFTER
// completeTaskWithResult already closed the run successfully — is a benign
// duplicate, not a stranded run. It must:
//  1. never mutate the already-closed run's terminal record (the store
//     layer rejects the duplicate with task.ErrRunAlreadyClosed and appends
//     nothing new), and
//  2. never log at ERROR ("permanently strands") for that specific,
//     distinguishable error — proven here by contrast with a genuine close
//     failure (an unknown run id), which must still log at ERROR.
func TestCloseRun_DuplicateCloseAfterAlreadyClosedLogsInfoNotError(t *testing.T) {
	te, store := newTestTaskExecutor(t)
	taskID := "task-close-dup"

	run, created, oerr := store.OpenRun(taskID, nil, task.RunKindManual, "session-a")
	if oerr != nil || !created {
		t.Fatalf("setup OpenRun: run=%+v created=%v err=%v", run, created, oerr)
	}
	active := &activeRun{runID: run.RunID}

	logFile := filepath.Join(t.TempDir(), "close-run-dup.log")
	prevLevel := logger.GetLevel()
	t.Cleanup(logger.DisableConsole())
	logger.SetLevel(logger.ERROR)
	if ferr := logger.EnableFileLogging(logFile); ferr != nil {
		t.Fatalf("EnableFileLogging: %v", ferr)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	// First close: the real, successful completion (mirrors
	// completeTaskWithResult's own closeRun call).
	te.closeRun(taskID, active, task.StatusDone, "first result")

	closedRuns, lerr := store.ListRuns(taskID)
	if lerr != nil {
		t.Fatalf("ListRuns after first close: %v", lerr)
	}
	if len(closedRuns) != 1 || closedRuns[0].Status != task.StatusDone || closedRuns[0].Result != "first result" {
		t.Fatalf("unexpected state after first close: %+v", closedRuns)
	}

	// Second close: simulates the panic-recovery defer re-invoking closeRun
	// on the SAME already-closed *activeRun with a different (panic/failed)
	// outcome, exactly as runTask's defer would after a housekeeping panic.
	te.closeRun(taskID, active, task.StatusFailed, "panic during task execution: boom")

	afterDup, lerr2 := store.ListRuns(taskID)
	if lerr2 != nil {
		t.Fatalf("ListRuns after duplicate close: %v", lerr2)
	}
	if len(afterDup) != 1 || afterDup[0].Status != task.StatusDone || afterDup[0].Result != "first result" {
		t.Fatalf("duplicate close must not mutate the already-terminal run record, got %+v", afterDup)
	}

	logged, rerr := os.ReadFile(logFile)
	if rerr != nil {
		t.Fatalf("read log file: %v", rerr)
	}
	if strings.Contains(string(logged), "Could not close task run record") {
		t.Fatalf(
			"duplicate-close-of-an-already-closed-run must NOT log at ERROR (false on-call alert), got log:\n%s",
			logged,
		)
	}

	// Contrast: a GENUINE close failure (unknown run id) must still log at
	// ERROR — proving the guard is specific to ErrRunAlreadyClosed, not a
	// blanket demotion of every CloseRun error.
	te.closeRun(taskID, &activeRun{runID: "does-not-exist"}, task.StatusFailed, "x")
	logged2, rerr2 := os.ReadFile(logFile)
	if rerr2 != nil {
		t.Fatalf("read log file (2nd read): %v", rerr2)
	}
	if !strings.Contains(string(logged2), "Could not close task run record") {
		t.Fatalf("a genuine close failure (run not found) must still log at ERROR, got log:\n%s", logged2)
	}
}
