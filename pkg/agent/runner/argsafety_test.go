// argsafety_test.go — ADR-032 fix M-1 regression coverage for
// filterDangerousCLIArgs and its wiring into each driver's buildArgs.

package runner

import (
	"strings"
	"testing"
)

// --- unit tests: filterDangerousCLIArgs -----------------------------------

func TestFilterDangerousCLIArgs_Claude_DropsSkipPermissions(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("claude", []string{"--verbose", "--dangerously-skip-permissions", "--foo"})
	if containsFlag(kept, "--dangerously-skip-permissions") {
		t.Fatalf("--dangerously-skip-permissions must be dropped for claude; kept=%v", kept)
	}
	if !containsFlag(kept, "--verbose") || !containsFlag(kept, "--foo") {
		t.Fatalf("benign flags must be preserved; kept=%v", kept)
	}
	if len(dropped) != 1 || dropped[0] != "--dangerously-skip-permissions" {
		t.Fatalf("expected exactly one dropped token; dropped=%v", dropped)
	}
}

func TestFilterDangerousCLIArgs_Claude_PermissionModeEscalation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantDrp bool
	}{
		{"two-token bypassPermissions dropped", []string{"--permission-mode", "bypassPermissions"}, true},
		{"equals-form bypassPermissions dropped", []string{"--permission-mode=bypassPermissions"}, true},
		{"case-insensitive value dropped", []string{"--permission-mode", "BypassPermissions"}, true},
		{"narrowing to plan kept", []string{"--permission-mode", "plan"}, false},
		{"narrowing to default kept", []string{"--permission-mode", "default"}, false},
		{"redundant acceptEdits kept", []string{"--permission-mode", "acceptEdits"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := filterDangerousCLIArgs("claude", tc.args)
			if tc.wantDrp {
				if len(dropped) == 0 {
					t.Fatalf("expected the flag+value pair to be dropped; kept=%v dropped=%v", kept, dropped)
				}
				if containsFlag(kept, "--permission-mode") {
					t.Fatalf("dangerous --permission-mode value must not remain in kept args; kept=%v", kept)
				}
			} else {
				if len(dropped) != 0 {
					t.Fatalf("benign --permission-mode value must not be dropped; dropped=%v", dropped)
				}
				if !containsSeq(kept, "--permission-mode", tc.args[1]) {
					t.Fatalf("benign --permission-mode value must be preserved intact; kept=%v", kept)
				}
			}
		})
	}
}

func TestFilterDangerousCLIArgs_Codex_DropsBypassAndFullAccessSandbox(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("codex", []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"--sandbox", "danger-full-access",
		"--color", "never",
	})
	if containsFlag(kept, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("--dangerously-bypass-approvals-and-sandbox must be dropped; kept=%v", kept)
	}
	if containsFlag(kept, "--sandbox") {
		t.Fatalf("--sandbox danger-full-access must be dropped entirely (flag+value); kept=%v", kept)
	}
	if !containsSeq(kept, "--color", "never") {
		t.Fatalf("benign --color never must be preserved; kept=%v", kept)
	}
	if len(dropped) != 2 { // 1 boolean-flag entry + 1 combined "--sandbox danger-full-access" entry
		t.Fatalf("expected 2 dropped entries (boolean flag, sandbox pair); dropped=%v", dropped)
	}
}

func TestFilterDangerousCLIArgs_Codex_SandboxNarrowingKept(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("codex", []string{"--sandbox", "read-only"})
	if len(dropped) != 0 {
		t.Fatalf("narrowing --sandbox to read-only must not be dropped; dropped=%v", dropped)
	}
	if !containsSeq(kept, "--sandbox", "read-only") {
		t.Fatalf("--sandbox read-only must be preserved intact; kept=%v", kept)
	}
}

func TestFilterDangerousCLIArgs_Codex_AskForApprovalAlwaysDropped(t *testing.T) {
	// Per argsafety.go's documented judgment call: since the driver's own
	// "--ask-for-approval never" is already codex's least-interactive value
	// (no safe "more permissive" direction exists), ANY operator override —
	// including the review's own cited example, "untrusted" — is dropped
	// conservatively rather than narrowed to specific values.
	for _, v := range []string{"untrusted", "on-request", "never", "on-failure"} {
		kept, dropped := filterDangerousCLIArgs("codex", []string{"--ask-for-approval", v})
		if len(dropped) != 1 {
			t.Fatalf("--ask-for-approval %q must be dropped as one flag+value entry; kept=%v dropped=%v", v, kept, dropped)
		}
		if containsFlag(kept, "--ask-for-approval") {
			t.Fatalf("--ask-for-approval %q must not remain in kept args; kept=%v", v, kept)
		}
	}
}

func TestFilterDangerousCLIArgs_Opencode_DropsDuplicateSkipPermissions(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("opencode", []string{"--pure", "--dangerously-skip-permissions"})
	if containsFlag(kept, "--dangerously-skip-permissions") {
		t.Fatalf("a redundant operator copy of --dangerously-skip-permissions must be dropped; kept=%v", kept)
	}
	if !containsFlag(kept, "--pure") {
		t.Fatalf("benign flags must be preserved; kept=%v", kept)
	}
	if len(dropped) != 1 {
		t.Fatalf("expected exactly one dropped token; dropped=%v", dropped)
	}
}

// --- unit tests: stream-format correctness guard ---------------------------
//
// These cover the second denylist category added alongside the original
// permission/sandbox-bypass guard: each driver's NDJSON stream-parser output
// format flag. Unlike the flags above, an override here does not escalate
// privilege — it silently corrupts the streamed transcript by switching the
// CLI to output the parser cannot read (correctness, not security).

func TestFilterDangerousCLIArgs_Claude_OutputFormatOverrideDropped(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantDrp bool
	}{
		{"two-token text override dropped", []string{"--output-format", "text"}, true},
		{"two-token json override dropped", []string{"--output-format", "json"}, true},
		{"equals-form override dropped", []string{"--output-format=text"}, true},
		{"redundant same value kept", []string{"--output-format", "stream-json"}, false},
		{"redundant same value case-insensitive kept", []string{"--output-format", "Stream-JSON"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := filterDangerousCLIArgs("claude", tc.args)
			if tc.wantDrp {
				if len(dropped) == 0 {
					t.Fatalf("expected --output-format override to be dropped; kept=%v dropped=%v", kept, dropped)
				}
				if containsFlag(kept, "--output-format") {
					t.Fatalf("dangerous --output-format value must not remain in kept args; kept=%v", kept)
				}
			} else {
				if len(dropped) != 0 {
					t.Fatalf("the driver's own stream-json value must not be dropped; dropped=%v", dropped)
				}
			}
		})
	}
}

func TestFilterDangerousCLIArgs_Codex_JsonDisableDropped(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"bare redundant repeat dropped", []string{"--json"}},
		{"equals-form false dropped", []string{"--json=false"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := filterDangerousCLIArgs("codex", tc.args)
			if len(dropped) != 1 {
				t.Fatalf("expected --json to be dropped as one entry; kept=%v dropped=%v", kept, dropped)
			}
			if containsFlag(kept, "--json") {
				t.Fatalf("operator --json token must not remain in kept args; kept=%v", kept)
			}
		})
	}
}

func TestFilterDangerousCLIArgs_Opencode_FormatOverrideDropped(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantDrp bool
	}{
		{"two-token text override dropped", []string{"--format", "text"}, true},
		{"equals-form override dropped", []string{"--format=text"}, true},
		{"redundant same value kept", []string{"--format", "json"}, false},
		{"redundant same value case-insensitive kept", []string{"--format", "JSON"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := filterDangerousCLIArgs("opencode", tc.args)
			if tc.wantDrp {
				if len(dropped) == 0 {
					t.Fatalf("expected --format override to be dropped; kept=%v dropped=%v", kept, dropped)
				}
				if containsFlag(kept, "--format") {
					t.Fatalf("dangerous --format value must not remain in kept args; kept=%v", kept)
				}
			} else {
				if len(dropped) != 0 {
					t.Fatalf("the driver's own json value must not be dropped; dropped=%v", dropped)
				}
			}
		})
	}
}

// --- integration tests: stream-format guard wired into buildArgs -----------

// TestClaudeDriver_BuildArgs_OutputFormatOverrideDropped proves an operator
// cli_args attempt to change --output-format away from stream-json never
// reaches the final argv, and the driver's own stream-json value survives
// exactly once.
func TestClaudeDriver_BuildArgs_OutputFormatOverrideDropped(t *testing.T) {
	d := NewClaudeDriver(nil)
	var args []string
	out := captureSlogWarnings(func() {
		args = d.buildArgs(RunOptions{
			Input:   "task",
			RunID:   "ext-argsafety-fmt-1",
			CLIArgs: []string{"--output-format", "text"},
		})
	})
	if !containsSeq(args, "--output-format", "stream-json") {
		t.Fatalf("the driver's own --output-format stream-json must remain; args=%v", args)
	}
	occurrences := 0
	for _, a := range args {
		if a == "--output-format" {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("--output-format must appear exactly once (driver's own copy only); args=%v", args)
	}
	if !strings.Contains(out, "ext-argsafety-fmt-1") {
		t.Fatalf("expected a WARN naming the run_id; log: %q", out)
	}
}

// TestCodexDriver_BuildArgs_JsonDisableDropped proves an operator cli_args
// attempt to disable --json never reaches the final argv, and the driver's
// own --json flag survives exactly once.
func TestCodexDriver_BuildArgs_JsonDisableDropped(t *testing.T) {
	d := NewCodexDriver(nil)
	var args []string
	out := captureSlogWarnings(func() {
		args = d.buildArgs(RunOptions{
			Input:   "task",
			RunID:   "ext-argsafety-fmt-2",
			CLIArgs: []string{"--json=false"},
		})
	})
	occurrences := 0
	for _, a := range args {
		if a == "--json" {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("--json must appear exactly once (driver's own copy only); args=%v", args)
	}
	if strings.Contains(strings.Join(args, " "), "--json=false") {
		t.Fatalf("operator --json=false must not reach argv; args=%v", args)
	}
	if !strings.Contains(out, "ext-argsafety-fmt-2") {
		t.Fatalf("expected a WARN naming the run_id; log: %q", out)
	}
}

// TestOpencodeDriver_BuildArgs_FormatOverrideDropped proves an operator
// cli_args attempt to change --format away from json never reaches the final
// argv, and the driver's own json value survives exactly once.
func TestOpencodeDriver_BuildArgs_FormatOverrideDropped(t *testing.T) {
	d := NewOpencodeDriver(nil)
	var args []string
	out := captureSlogWarnings(func() {
		args = d.buildArgs(RunOptions{
			Input:   "task",
			RunID:   "ext-argsafety-fmt-3",
			CLIArgs: []string{"--format", "text"},
		})
	})
	if !containsSeq(args, "--format", "json") {
		t.Fatalf("the driver's own --format json must remain; args=%v", args)
	}
	occurrences := 0
	for _, a := range args {
		if a == "--format" {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("--format must appear exactly once (driver's own copy only); args=%v", args)
	}
	if !strings.Contains(out, "ext-argsafety-fmt-3") {
		t.Fatalf("expected a WARN naming the run_id; log: %q", out)
	}
}

func TestFilterDangerousCLIArgs_UnknownCLIPassesThrough(t *testing.T) {
	args := []string{"--anything", "--dangerously-skip-permissions"}
	kept, dropped := filterDangerousCLIArgs("some-future-cli", args)
	if len(dropped) != 0 {
		t.Fatalf("an unrecognized CLI key must have no denylist and drop nothing; dropped=%v", dropped)
	}
	if len(kept) != len(args) {
		t.Fatalf("an unrecognized CLI key must pass args through unchanged; kept=%v", kept)
	}
}

func TestFilterDangerousCLIArgs_EmptyArgsNoop(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("claude", nil)
	if kept != nil || dropped != nil {
		t.Fatalf("empty input must produce empty output; kept=%v dropped=%v", kept, dropped)
	}
}

// TestFilterDangerousCLIArgs_ValueTakingFlagAtEndDropsFlagOnly covers the
// documented conservative heuristic: when a dangerous value-taking flag is
// the LAST token (no following value to inspect), only the flag token itself
// is dropped rather than guessing.
func TestFilterDangerousCLIArgs_ValueTakingFlagAtEndDropsFlagOnly(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("codex", []string{"--sandbox"})
	if len(dropped) != 1 || dropped[0] != "--sandbox" {
		t.Fatalf("a trailing dangerous flag with no value must drop just the flag; dropped=%v", dropped)
	}
	if len(kept) != 0 {
		t.Fatalf("expected nothing kept; kept=%v", kept)
	}
}

// TestFilterDangerousCLIArgs_ValueTakingFlagFollowedByAnotherFlag covers the
// same heuristic when the NEXT token is itself flag-shaped (so it cannot be
// the value) — the dangerous flag token is dropped alone, and the following
// flag is preserved untouched (never mistaken for this flag's value).
func TestFilterDangerousCLIArgs_ValueTakingFlagFollowedByAnotherFlag(t *testing.T) {
	kept, dropped := filterDangerousCLIArgs("codex", []string{"--sandbox", "--color", "never"})
	if len(dropped) != 1 || dropped[0] != "--sandbox" {
		t.Fatalf("only the flag token must be dropped when the next token looks like a flag; dropped=%v", dropped)
	}
	if !containsSeq(kept, "--color", "never") {
		t.Fatalf("the following flag+value must be preserved untouched; kept=%v", kept)
	}
}

// --- integration tests: driver buildArgs wiring ----------------------------

// TestClaudeDriver_BuildArgs_DangerousCLIArgsDropped proves the filter is
// actually wired into ClaudeDriver.buildArgs and that the driver's OWN
// --permission-mode acceptEdits flag is never stripped by the filter (the
// filter only ever inspects opts.CLIArgs).
func TestClaudeDriver_BuildArgs_DangerousCLIArgsDropped(t *testing.T) {
	d := NewClaudeDriver(nil)
	var args []string
	out := captureSlogWarnings(func() {
		args = d.buildArgs(RunOptions{
			Input:   "task",
			RunID:   "ext-argsafety-1",
			CLIArgs: []string{"--dangerously-skip-permissions", "--custom-flag", "value-x"},
		})
	})
	if containsFlag(args, "--dangerously-skip-permissions") {
		t.Fatalf("operator cli_args must not re-enable --dangerously-skip-permissions; args=%v", args)
	}
	if !containsSeq(args, "--permission-mode", "acceptEdits") {
		t.Fatalf("the driver's own --permission-mode acceptEdits must remain; args=%v", args)
	}
	if !containsSeq(args, "--custom-flag", "value-x") {
		t.Fatalf("a benign operator cli_args flag must be preserved; args=%v", args)
	}
	if !strings.Contains(out, "dropped") || !strings.Contains(out, "ext-argsafety-1") {
		t.Fatalf("expected a WARN naming the dropped flag and run_id; log: %q", out)
	}
}

// TestCodexDriver_BuildArgs_DangerousCLIArgsDropped mirrors the claude test
// for the codex driver: the driver's own --sandbox workspace-write /
// --ask-for-approval never must survive even when operator cli_args tries to
// override them.
func TestCodexDriver_BuildArgs_DangerousCLIArgsDropped(t *testing.T) {
	d := NewCodexDriver(nil)
	var args []string
	out := captureSlogWarnings(func() {
		args = d.buildArgs(RunOptions{
			Input: "task",
			RunID: "ext-argsafety-2",
			CLIArgs: []string{
				"--sandbox", "danger-full-access",
				"--ask-for-approval", "untrusted",
				"--dangerously-bypass-approvals-and-sandbox",
				"--skip-git-repo-check",
			},
		})
	})
	if !containsSeq(args, "--sandbox", "workspace-write") {
		t.Fatalf("the driver's own --sandbox workspace-write must remain (first occurrence, before exec); args=%v", args)
	}
	if !containsSeq(args, "--ask-for-approval", "never") {
		t.Fatalf("the driver's own --ask-for-approval never must remain; args=%v", args)
	}
	if containsFlag(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("operator cli_args must not re-enable the full bypass flag; args=%v", args)
	}
	if containsSeq(args, "--sandbox", "danger-full-access") {
		t.Fatalf("operator cli_args must not append a second, dangerous --sandbox value; args=%v", args)
	}
	if containsSeq(args, "--ask-for-approval", "untrusted") {
		t.Fatalf("operator cli_args must not append a second --ask-for-approval value; args=%v", args)
	}
	// The unrelated cli_args flag with no dangerous meaning survives.
	found := false
	for _, a := range args {
		if a == "--skip-git-repo-check" {
			found = true
		}
	}
	// --skip-git-repo-check is also one of the driver's OWN built-in flags,
	// so it must be present regardless — this assertion just documents that
	// a benign operator duplicate does not break anything.
	if !found {
		t.Fatalf("--skip-git-repo-check must be present; args=%v", args)
	}
	if !strings.Contains(out, "ext-argsafety-2") || !strings.Contains(out, "codex") {
		t.Fatalf("expected a WARN naming the run_id and cli; log: %q", out)
	}
	if strings.Count(out, "dropped") != 3 {
		t.Fatalf("expected one WARN per dropped flag (3: bypass, sandbox, ask-for-approval); log: %q", out)
	}
}

// TestOpencodeDriver_BuildArgs_DangerousCLIArgsDropped proves a REDUNDANT
// operator copy of --dangerously-skip-permissions in cli_args is stripped,
// while the driver's own single copy of that flag (added before the filter
// ever runs) remains exactly once in the final argv.
func TestOpencodeDriver_BuildArgs_DangerousCLIArgsDropped(t *testing.T) {
	d := NewOpencodeDriver(nil)
	var args []string
	out := captureSlogWarnings(func() {
		args = d.buildArgs(RunOptions{
			Input:   "task",
			RunID:   "ext-argsafety-3",
			CLIArgs: []string{"--dangerously-skip-permissions", "--pure"},
		})
	})
	occurrences := 0
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("--dangerously-skip-permissions must appear exactly once (driver's own copy only); args=%v", args)
	}
	if !containsFlag(args, "--pure") {
		t.Fatalf("a benign operator cli_args flag must be preserved; args=%v", args)
	}
	if !strings.Contains(out, "ext-argsafety-3") {
		t.Fatalf("expected a WARN naming the run_id; log: %q", out)
	}
}

// TestFilterDangerousCLIArgs_NeverTouchesDriverOwnFlags is a package-level
// sanity check documenting the contract: filterDangerousCLIArgs is a pure
// function over its args parameter alone — it has no way to see or mutate
// anything the driver already appended to its own argv slice before calling
// it. This is enforced by construction (each driver calls the filter ONLY on
// opts.CLIArgs, never on the accumulated args slice) — see the buildArgs call
// sites in driver_claude.go / driver_codex.go / driver_opencode.go.
func TestFilterDangerousCLIArgs_NeverTouchesDriverOwnFlags(t *testing.T) {
	driverOwnFlags := []string{"--permission-mode", "acceptEdits"}
	kept, dropped := filterDangerousCLIArgs("claude", driverOwnFlags)
	if len(dropped) != 0 {
		t.Fatalf("acceptEdits is not a dangerous value and must never be dropped; dropped=%v", dropped)
	}
	if !containsSeq(kept, "--permission-mode", "acceptEdits") {
		t.Fatalf("expected the flag+value preserved verbatim; kept=%v", kept)
	}
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// --- FilterDangerousCLIArgsDetailed (exported, used by rest_executor_preview.go) ---

// TestFilterDangerousCLIArgsDetailed_MatchesFlagOnlyVariant proves the
// exported detailed function agrees with the unexported flag-only
// filterDangerousCLIArgs on both kept and dropped-flag sets — it is a strict
// superset (adds Reason), not a different filtering decision.
func TestFilterDangerousCLIArgsDetailed_MatchesFlagOnlyVariant(t *testing.T) {
	args := []string{"--verbose", "--dangerously-skip-permissions", "--permission-mode", "bypassPermissions", "--foo"}

	wantKept, wantDropped := filterDangerousCLIArgs("claude", args)
	gotKept, gotDetailed := FilterDangerousCLIArgsDetailed("claude", args)

	if strings.Join(gotKept, ",") != strings.Join(wantKept, ",") {
		t.Fatalf("kept mismatch: detailed=%v flag-only=%v", gotKept, wantKept)
	}
	if len(gotDetailed) != len(wantDropped) {
		t.Fatalf("dropped count mismatch: detailed=%v flag-only=%v", gotDetailed, wantDropped)
	}
	for i, d := range gotDetailed {
		if d.Flag != wantDropped[i] {
			t.Fatalf("dropped[%d].Flag = %q, want %q", i, d.Flag, wantDropped[i])
		}
		if d.Reason == "" {
			t.Fatalf("dropped[%d] (%q) has an empty Reason", i, d.Flag)
		}
	}
}

// TestFilterDangerousCLIArgsDetailed_ReasonsPerCLI proves every denylisted
// entry across all three CLIs carries a non-empty Reason — a bare flag name
// with no explanation would defeat the point of the executor-preview
// endpoint's dropped_args field.
func TestFilterDangerousCLIArgsDetailed_ReasonsPerCLI(t *testing.T) {
	cases := []struct {
		cli  string
		args []string
	}{
		{"claude", []string{"--dangerously-skip-permissions"}},
		{"claude", []string{"--permission-mode", "bypassPermissions"}},
		{"claude", []string{"--output-format", "text"}},
		{"codex", []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{"codex", []string{"--sandbox", "danger-full-access"}},
		{"codex", []string{"--ask-for-approval", "on-failure"}},
		{"codex", []string{"--json"}},
		{"opencode", []string{"--dangerously-skip-permissions"}},
		{"opencode", []string{"--format", "text"}},
	}
	for _, tc := range cases {
		t.Run(tc.cli+"/"+strings.Join(tc.args, "_"), func(t *testing.T) {
			_, dropped := FilterDangerousCLIArgsDetailed(tc.cli, tc.args)
			if len(dropped) == 0 {
				t.Fatalf("expected at least one dropped entry for %v", tc.args)
			}
			for _, d := range dropped {
				if d.Reason == "" {
					t.Fatalf("dropped flag %q has an empty Reason", d.Flag)
				}
			}
		})
	}
}

// TestFilterDangerousCLIArgsDetailed_EmptyWhenNothingDropped proves a benign
// args slice returns a nil/empty dropped slice, not a slice of empty entries.
func TestFilterDangerousCLIArgsDetailed_EmptyWhenNothingDropped(t *testing.T) {
	kept, dropped := FilterDangerousCLIArgsDetailed("claude", []string{"--add-dir", "/tmp/x"})
	if len(dropped) != 0 {
		t.Fatalf("expected no dropped entries; got %v", dropped)
	}
	if !containsSeq(kept, "--add-dir", "/tmp/x") {
		t.Fatalf("expected benign args preserved; kept=%v", kept)
	}
}
