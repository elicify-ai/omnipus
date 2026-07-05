// driver_buildargs_test.go — ADR-032 per-driver buildArgs regression coverage.
//
// Proves the flag-level fixes recorded in
// docs/internal/architecture/ADR-032-external-agent-workspace-execution.md:
//   - claude: --model is auto-set when RunOptions.Model is non-empty; --verbose
//     is always present (required for stream-json to emit); --resume is NEVER
//     emitted (a freshly generated RunID is not a real prior session).
//   - codex: --sandbox workspace-write and --ask-for-approval never are always
//     present, with --ask-for-approval correctly placed BEFORE the exec
//     subcommand (a codex CLI constraint — it errors if placed after `exec`);
//     -m <model> is auto-set when RunOptions.Model is non-empty.

package runner

import "testing"

func TestClaudeDriver_BuildArgs_ModelAutoSet(t *testing.T) {
	d := NewClaudeDriver(nil)

	withModel := d.buildArgs(RunOptions{Input: "task", Model: "claude-sonnet-4.6"})
	if !containsSeq(withModel, "--model", "claude-sonnet-4.6") {
		t.Errorf("expected --model claude-sonnet-4.6 in args; got %v", withModel)
	}

	noModel := d.buildArgs(RunOptions{Input: "task"})
	for _, a := range noModel {
		if a == "--model" {
			t.Errorf("empty Model must not produce a --model flag; args=%v", noModel)
		}
	}
}

func TestClaudeDriver_BuildArgs_VerboseAlwaysPresent(t *testing.T) {
	d := NewClaudeDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task"})
	found := false
	for _, a := range args {
		if a == "--verbose" {
			found = true
		}
	}
	if !found {
		t.Fatalf("--verbose is required for --output-format stream-json to emit; args=%v", args)
	}
}

func TestClaudeDriver_BuildArgs_NeverEmitsResume(t *testing.T) {
	d := NewClaudeDriver(nil)
	// RunID set (as it always is in production — subturn-N / ext-<nanos>):
	// --resume must NOT be appended since it is not a real prior session ID.
	args := d.buildArgs(RunOptions{Input: "task", RunID: "ext-1730000000000000000"})
	for _, a := range args {
		if a == "--resume" {
			t.Fatalf(
				"--resume must never be emitted — RunID is a fresh dispatch id, not a real claude session; args=%v",
				args,
			)
		}
	}
}

// TestClaudeDriver_BuildArgs_SkipPermissionsUnconditional locks in the
// post-2026-07-05 behavior (operator decision, issue #488): claude now runs
// permission-bypassed unconditionally, matching codex/opencode, reversing
// FR-5.3/US-5's original claude-specific acceptEdits middle ground.
func TestClaudeDriver_BuildArgs_SkipPermissionsUnconditional(t *testing.T) {
	d := NewClaudeDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task"})
	if !containsFlag(args, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions (unconditional, issue #488); args=%v", args)
	}
	for _, a := range args {
		if a == "--permission-mode" {
			t.Fatalf(
				"--permission-mode must no longer be used; the driver's own baseline is now a full bypass; args=%v",
				args,
			)
		}
	}
}

func TestCodexDriver_BuildArgs_SandboxAndApprovalPosture(t *testing.T) {
	d := NewCodexDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task"})

	if !containsSeq(args, "--sandbox", "workspace-write") {
		t.Errorf("expected --sandbox workspace-write; args=%v", args)
	}
	if !containsSeq(args, "--ask-for-approval", "never") {
		t.Errorf("expected --ask-for-approval never; args=%v", args)
	}
	for _, a := range args {
		if a == "--dangerously-bypass-approvals-and-sandbox" {
			t.Fatalf("--dangerously-bypass-approvals-and-sandbox must never be used (FR-5.3); args=%v", args)
		}
	}
}

// TestCodexDriver_BuildArgs_AskForApprovalPrecedesExec is the ADR-032 flag-
// ordering regression test: codex's -a/--ask-for-approval is a GLOBAL flag
// and errors ("unexpected argument '--ask-for-approval' found") when placed
// after the `exec` subcommand — it MUST precede `exec`. --sandbox, by
// contrast, is accepted as an exec-subcommand flag and may follow `exec`.
func TestCodexDriver_BuildArgs_AskForApprovalPrecedesExec(t *testing.T) {
	d := NewCodexDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task"})

	execIdx, approvalIdx, sandboxIdx := -1, -1, -1
	for i, a := range args {
		switch a {
		case "exec":
			execIdx = i
		case "--ask-for-approval":
			approvalIdx = i
		case "--sandbox":
			sandboxIdx = i
		}
	}
	if execIdx == -1 {
		t.Fatalf("expected \"exec\" subcommand in args; got %v", args)
	}
	if approvalIdx == -1 || approvalIdx > execIdx {
		t.Fatalf(
			"--ask-for-approval must precede the exec subcommand (global flag, codex constraint); args=%v",
			args,
		)
	}
	if sandboxIdx == -1 || sandboxIdx < execIdx {
		t.Fatalf("--sandbox must follow the exec subcommand (exec-level flag); args=%v", args)
	}
}

func TestCodexDriver_BuildArgs_ModelAutoSet(t *testing.T) {
	d := NewCodexDriver(nil)

	withModel := d.buildArgs(RunOptions{Input: "task", Model: "gpt-5-codex"})
	if !containsSeq(withModel, "-m", "gpt-5-codex") {
		t.Errorf("expected -m gpt-5-codex in args; got %v", withModel)
	}

	noModel := d.buildArgs(RunOptions{Input: "task"})
	for _, a := range noModel {
		if a == "-m" {
			t.Errorf("empty Model must not produce a -m flag; args=%v", noModel)
		}
	}
}
