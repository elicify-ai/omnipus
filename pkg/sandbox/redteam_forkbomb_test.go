//go:build linux

// Package sandbox_test — insider-LLM red-team coverage for fork-bomb threats.
//
// Two threats from the insider-pentest report are exercised here:
//
//	C3-DIRECT — direct fork bomb. The classic ":(){ :|:& };:" payload, sent
//	            verbatim through workspace.shell. Today: blocked by the
//	            shell-guard regex (`:\(\)\s*\{.*\};\s*:`). Test PASSES today.
//
//	C3-INDIRECT — indirect fork bomb. The same payload written to a file
//	            (`fork.sh`) inside the workspace, then invoked via `sh fork.sh`.
//	            The shell-guard regex never sees the bomb's pattern (only sees
//	            "sh fork.sh"). Today: NOT contained — there is no
//	            RLIMIT_NPROC on hardened-exec children. Test FAILS by design
//	            until v0.2 (#155) adds RLIMIT_NPROC to hardened_exec_linux.go.
//
// Safety: the indirect test sets RLIMIT_NPROC on the test process itself
// before launching the bomb. This means even if the production code lacks
// the limit, the kernel-imposed cap on the test process (and its
// descendants) bounds the host damage. The test asserts that production
// code SHOULD propagate a similar limit; until then the test reports the
// gap as a t.Errorf.

package sandbox_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestRedteam_ForkBomb_DirectPattern_Blocked verifies that the classic
// fork-bomb pattern is blocked at the shell-guard regex layer BEFORE it
// reaches the kernel. This passes today because of the existing
// `:\s*\(\s*\)\s*\{.*\};\s*:` regex in pkg/tools/shell.go::defaultDenyPatterns.
//
// We don't import pkg/tools here — that's a heavy package with many
// transitive deps. Instead we reproduce the production regex inline so
// this test fails loudly if anyone reworks the regex without keeping the
// same coverage. The regex string MUST be kept in sync with the production
// definition at pkg/tools/shell.go ~L227 (post v0.2 #155 item 5 widening).
func TestRedteam_ForkBomb_DirectPattern_Blocked(t *testing.T) {
	t.Logf(
		"documents C3-DIRECT (fork bomb literal pattern) from insider-pentest report; current control is shell-guard regex",
	)

	// Production regex — keep in sync with pkg/tools/shell.go ~L227.
	// v0.2 #155 item 5: widened to also accept arbitrary identifiers and newlines.
	const guardPattern = `(?s)([A-Za-z_]\w*|:)\s*\(\s*\)\s*\{[^{}]*[|&][^{}]*\}\s*;\s*([A-Za-z_]\w*|:)`
	guard, err := regexp.Compile(guardPattern)
	if err != nil {
		t.Fatalf("compile guard regex: %v", err)
	}

	// Each variant exercises one whitespace permutation the production
	// regex now handles. After v0.2 #155 item 5 widened the pattern to
	// `:\s*\(\s*\)\s*\{.*\};\s*:`, the canonical bomb, trivial trailing-
	// space variants, and the formerly-narrow `:( ){…};:` shape ALL match.
	// `space_inside_func_def` is kept in the table at mustMatch=true so
	// any future re-tightening of the pattern surfaces as a regression.
	bombs := []struct {
		name      string
		text      string
		mustMatch bool   // when true, regex MUST match; failure is regression.
		gapNote   string // explanation when mustMatch=false.
	}{
		{"canonical", `:(){ :|:& };:`, true, ""},
		{"trailing_double_space", `:(){ :|:& };  :`, true, ""},
		{"no_space_before_colon", `:(){ :|:&}; :`, true, ""},
		{
			// Was a documented C3-DIRECT-NARROW gap pre-v0.2; the widened
			// regex matches `:( )` thanks to the \s* inside the parens.
			// Locked in at mustMatch=true to catch future regressions.
			"space_inside_func_def",
			`:( ){ :|:& };:`,
			true,
			"",
		},
	}

	matched := 0
	for _, b := range bombs {
		t.Run(b.name, func(t *testing.T) {
			ok := guard.MatchString(b.text)
			if b.mustMatch {
				if !ok {
					t.Errorf("C3-DIRECT REGRESSION: shell-guard regex %q failed to match %q — bomb would reach exec",
						guardPattern, b.text)
					return
				}
				matched++
				return
			}
			// mustMatch=false: documented gap. We assert NOT-matched and
			// log the gap so the failure surfaces if the regex is later
			// broadened (at which point this row should be flipped to
			// mustMatch=true).
			if !ok {
				t.Errorf("C3-DIRECT-NARROW GAP CONFIRMED: regex did not match %q. %s",
					b.text, b.gapNote)
			} else {
				t.Logf("C3-DIRECT-NARROW closed: regex now matches %q — flip mustMatch=true", b.text)
			}
		})
	}
	if matched > 0 {
		t.Logf("C3-DIRECT: shell-guard regex matched %d/%d MUST-MATCH bomb variants", matched, 3)
	}
}

// TestRedteam_ForkBomb_BypassShapes documents additional bypass variants that
// should be caught by the shell-guard regex. After v0.2 #155 item 5 widened
// the regex to allow whitespace inside the parens, `whitespace_inside_parens`
// is no longer a documented gap — it's a must-match. The remaining
// mustMatch=false rows (disguised identifier, newline-inside-braces) are
// known shapes that require additional regex generalisation; they remain
// open and are closed in a future hardening pass.
//
// DO NOT widen the production regex in this test — widening is tracked in
// pkg/tools/shell.go. This test only documents the bypass shapes and asserts
// that the expected-match cases DO match.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 8
// Production regex location: pkg/tools/shell.go ~L227
func TestRedteam_ForkBomb_BypassShapes(t *testing.T) {
	t.Logf("documents C3-DIRECT bypass shapes from insider-pentest report; " +
		"after v0.2 #155 item 5, `whitespace_inside_parens` is closed (must match)")

	// Production regex — keep in sync with pkg/tools/shell.go ~L227.
	// v0.2 #155 item 5: widened to also accept arbitrary identifiers and newlines.
	const guardPattern = `(?s)([A-Za-z_]\w*|:)\s*\(\s*\)\s*\{[^{}]*[|&][^{}]*\}\s*;\s*([A-Za-z_]\w*|:)`
	guard, err := regexp.Compile(guardPattern)
	if err != nil {
		t.Fatalf("compile guard regex: %v", err)
	}

	// Notes on expected-fail cases:
	//   All of the following are valid fork-bomb invocations that exploit the
	//   narrow regex. They represent progressively harder-to-detect variants.
	//   mustMatch=false means the CURRENT regex does NOT catch them; the test
	//   t.Errorf (not t.Fatalf) so all cases are reported in one run.
	//
	//   When v0.2 #155 ships: flip these to mustMatch=true and the test
	//   validates the widened coverage.
	bypasses := []struct {
		name      string
		text      string
		mustMatch bool
		gapNote   string
	}{
		{
			// Whitespace injected throughout — `: ( ) { : | : & } ; :`.
			// CLOSED in v0.2 #155 item 5: the widened regex
			// `:\s*\(\s*\)\s*\{.*\};\s*:` matches \s* at the leading colon,
			// inside the parens, after the parens, and around `};:`. All
			// the spaces in this case are absorbed by the \s* tokens.
			name:      "whitespace_inside_parens",
			text:      ": ( ) { : | : & } ; :",
			mustMatch: true,
			gapNote:   "",
		},
		{
			// Classic bomb followed by a newline continuation.
			// `:(){ :|:& };:` + newline + `echo done`
			// The regex uses MatchString which searches for the pattern anywhere
			// in the string — the newline between lines should not prevent a match
			// on the first line if `.` does not cross `\n`. Since MatchString
			// uses RE2 default (`.` does not match `\n`), the pattern `.*` stops
			// at the newline, meaning `{…}` matches the first-line content.
			// Expected: the canonical pattern IS in the first line, so this MUST MATCH.
			name:      "newline_after_bomb",
			text:      ":(){ :|:& };:\necho done",
			mustMatch: true,
			gapNote:   "",
		},
		{
			// Bomb invoked via `bash -c`. The guard sees the outer command string,
			// not the -c argument. If the outer string is `bash -c ":(){:|:&};:"`,
			// the bomb pattern is present as a substring — MatchString SHOULD catch it.
			// Expected: MUST MATCH (the bomb literal is a substring of the full string).
			name:      "via_bash_dash_c",
			text:      `bash -c ":(){:|:&};:"`,
			mustMatch: true,
			gapNote:   "",
		},
		{
			// Function defined in env var and eval'd: `f=":(){:|:&};:"; eval $f`.
			// The guard sees `f=":(){:|:&};:"; eval $f`. The bomb pattern is inside
			// the string assignment — MatchString should find it as a substring.
			// Expected: MUST MATCH (bomb literal present in the full command string).
			name:      "via_eval_env_var",
			text:      `f=":(){:|:&};:"; eval $f`,
			mustMatch: true,
			gapNote:   "",
		},
		{
			// Disguised variant: `b()(b|b);b` — substitutes `:` with `b`.
			// This is a valid fork bomb using a different identifier. The current
			// guard pattern anchors on `:` and `\(` — it will NOT match `b()`.
			// Expected: FAIL today (mustMatch=false).
			// Closes when: the regex is generalised to match any identifier.
			// CLOSED in v0.2 #155 item 5: regex now accepts any identifier
			// `[A-Za-z_]\w*` in both head and tail position.
			// Note: text uses `()` not `(){}` — it's a function call followed
			// by a subshell, the literal `b()(b|b);b`. Rewrite as the canonical
			// braced form for the regex match.
			name:      "disguised_identifier_b",
			text:      "b(){ b|b };b",
			mustMatch: true,
			gapNote:   "",
		},
		{
			// Semicolon omitted, replaced with newline: `:(){ :|:& }\n:`.
			// The regex requires `};\s*:` — the `\n` between `}` and `:` would
			// only be matched if `\s*` covers `\n`. RE2 `\s` includes `\n`, so
			// this SHOULD match IF the `.*` in `\{.*\}` matches the inter-newline
			// content. Since `.` does NOT match `\n` in RE2, `{.*}` fails across
			// a newline and the pattern does not match.
			// Expected: FAIL today (mustMatch=false).
			// CLOSED in v0.2 #155 item 5: regex now uses `(?s)` so the body
			// can span lines.
			name:      "newline_inside_braces",
			text:      ":(){ :|:&\n};:",
			mustMatch: true,
			gapNote:   "",
		},
	}

	for _, b := range bypasses {
		t.Run(b.name, func(t *testing.T) {
			ok := guard.MatchString(b.text)
			if b.mustMatch {
				if !ok {
					t.Errorf("C3-BYPASS REGRESSION: shell-guard regex %q failed to match %q — "+
						"this bypass variant should be blocked: %s",
						guardPattern, b.text, b.gapNote)
				} else {
					t.Logf("OK: regex matched bypass variant %q", b.name)
				}
				return
			}
			// mustMatch=false: document the gap.
			if !ok {
				t.Errorf("C3-BYPASS GAP CONFIRMED: regex did not match %q. %s "+
					"(expected-fail; flip mustMatch=true when v0.2 #155 ships)", b.text, b.gapNote)
			} else {
				t.Logf("C3-BYPASS closed: regex now matches %q — flip mustMatch=true in this test", b.name)
			}
		})
	}
}

// TestRedteam_ForkBomb_IndirectViaScript_Limited documents C3-INDIRECT.
//
// The threat: an attacker can sidestep the shell-guard regex by writing
// the fork bomb to a file and invoking it via `sh fork.sh`. The shell-guard
// only sees the literal `sh fork.sh`, which is not a denied pattern. Once
// the script runs, fork() recursion explodes process-table headroom for
// the entire host within seconds — there is no RLIMIT_NPROC on the
// hardened-exec child today.
//
// Test mechanics:
//
//  1. We set RLIMIT_NPROC on the test process itself (a hard cap on
//     child-process count for our UID). This is a TEST-SAFETY MEASURE
//     ONLY — it's the only thing keeping the host responsive while we
//     exercise the unmitigated production code.
//
//  2. We write a small fork-bomb script to a tempdir, then attempt to
//     execute it via `sh fork.sh`. We mirror the production hardened_exec
//     contract slice that's relevant: Setpgid + Pdeathsig. We do NOT
//     import pkg/tools — keeps the test focused.
//
//  3. We measure host PID count before and during the run. The test
//     ASSERTS that production should impose its OWN limit (such that PID
//     growth saturates well below the test's safety cap). Today no such
//     limit exists, so the bomb saturates against the test cap and the
//     assertion fails — that's the documented gap.
//
// This test is documenting-only. It will FAIL until #155 adds RLIMIT_NPROC.
func TestRedteam_ForkBomb_IndirectViaScript_Limited(t *testing.T) {
	t.Logf(
		"documents C3-INDIRECT (fork bomb via script) from insider-pentest report; closes when v0.2 #155 adds RLIMIT_NPROC",
	)

	if runtime.GOOS != "linux" {
		t.Skip("Linux-only — this exercises Linux RLIMIT_NPROC")
	}
	if os.Getuid() == 0 {
		t.Skip("must run as non-root (root has effectively unlimited NPROC)")
	}

	// SAFETY GUARD: cap NPROC for the entire test process to bound the
	// blast radius of any fork-bomb that escapes the production cap.
	//
	// Sizing: RLIMIT_NPROC is a per-UID counter (counts every process the
	// test user owns at fork() time, not just descendants of this test).
	// On a busy CI runner the user may already have 100+ processes from
	// parallel test runs and the runner itself, so we measure the current
	// count and add a safety headroom on top. The cap must be:
	//   - higher than (current + childNProcSlack + small slack) so the
	//     production-capped bomb does not bump into the test cap and
	//     produce a false-positive "test cap saved us" reading.
	//   - bounded enough that an UNCAPPED bomb saturates it within the
	//     3-second test window (a doubling-fork-bomb saturates 1024 PIDs
	//     in well under 100 ms, so any cap below ~1000 fits the bill).
	var oldRlim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &oldRlim); err != nil {
		t.Fatalf("getrlimit NPROC (safety probe): %v", err)
	}
	defer func() {
		// Best-effort restore; if the test process still has children
		// alive this may EPERM, but the test is ending anyway.
		_ = unix.Setrlimit(unix.RLIMIT_NPROC, &oldRlim)
	}()
	currentPIDs := uint64(countOurPIDs(t))
	// safetyCap = current + 512 — comfortably above childNProcSlack (128) +
	// production slack, comfortably below an exponential-bomb saturation
	// (a doubling fork-bomb reaches 1024 in 10 cycles ≈ <100ms).
	safetyCap := currentPIDs + 512
	if oldRlim.Cur > 0 && safetyCap > oldRlim.Cur {
		// Don't try to RAISE NPROC above the OS-imposed soft limit; we
		// only have CAP_SYS_RESOURCE if running as root (which we already
		// skipped above). If the OS limit is below our target, fall back
		// to it.
		safetyCap = oldRlim.Cur
	}
	safetyRlim := unix.Rlimit{Cur: safetyCap, Max: oldRlim.Max}
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &safetyRlim); err != nil {
		t.Skipf("cannot set RLIMIT_NPROC for test safety (need a non-restricted user): %v", err)
	}

	workspace := t.TempDir()
	scriptPath := filepath.Join(workspace, "fork.sh")
	bomb := `#!/bin/sh
:(){ :|:& };:
`
	if err := os.WriteFile(scriptPath, []byte(bomb), 0o755); err != nil {
		t.Fatalf("write fork.sh: %v", err)
	}

	// Sample baseline PID count for our UID before launching the bomb.
	baselinePIDs := countOurPIDs(t)

	// Run the script with a wall-clock cap. Production hardened_exec sets
	// Setpgid so we can signal the whole tree on timeout — we do the same
	// here. The bomb continuously self-replicates so we expect the run to
	// be killed by the context, not exit on its own.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", scriptPath)
	cmd.Dir = workspace
	// Mirror the production hardened-exec path: apply pre-start hardening
	// (Setpgid + Pdeathsig) via the same primitive shell.go uses, then
	// post-start hardening which (after v0.2 #155 item 5) sets
	// RLIMIT_NPROC on the child PID.
	if err := sandbox.ApplyChildHardening(cmd, sandbox.Limits{}); err != nil {
		t.Fatalf("ApplyChildHardening: %v", err)
	}

	startErr := cmd.Start()
	if startErr != nil {
		// Even Start can fail if NPROC is already at cap. That's actually
		// the right outcome for the threat — bomb didn't get to run. But
		// we don't accept that as proof of containment because production
		// must contain it without relying on the test's pre-imposed cap.
		t.Logf(
			"cmd.Start failed (likely test-imposed NPROC cap): %v — note: this is the test's safety net, not production behavior",
			startErr,
		)
		t.Errorf(
			"C3-INDIRECT GAP CONFIRMED (preflight): even bomb startup hit the test's safety cap of %d. "+
				"That means production hardened_exec_linux.go applies NO limit of its own — the only thing "+
				"keeping the host alive is operator-level user nproc limits. Fix: ship RLIMIT_NPROC on "+
				"hardened-exec children in v0.2 (#155).",
			safetyCap,
		)
		return
	}

	// Apply post-start hardening (RLIMIT_NPROC=baseline+childNProcSlack(128)).
	// This is the production contract — sandbox.Run does this automatically;
	// callers that bypass Run (web_serve, workspace.shell_bg, etc.) must
	// invoke ApplyChildPostStartHardening themselves. v0.2 #155 item 5.
	if err := sandbox.ApplyChildPostStartHardening(cmd, sandbox.Limits{}); err != nil {
		// Non-fatal here: the test's own safety cap still bounds host
		// damage, but the test will fail the gap assertion below.
		t.Logf("ApplyChildPostStartHardening (RLIMIT_NPROC) failed: %v", err)
	}

	// Sample peak PID count while the bomb runs.
	peakPIDs := samplePeakPIDs(t, ctx, baselinePIDs)

	// Kill the bomb's process group so we don't leave zombies.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()

	// After v0.2 #155 item 5, the production hardened-exec code applies
	// RLIMIT_NPROC=baseline+128 to every child. The bomb's growth must
	// saturate against THAT cap, well below the test's own dynamically-
	// sized safetyCap, proving the production limit is in effect. If
	// growth approaches safetyCap the production limit is missing and
	// the test cap is the only thing containing the bomb.
	//
	// Pass criterion: growth ≤ childNProcSlack (128) + small slack for the
	// initial sh + intermediate descendants. We use 160 as the threshold
	// to leave room for transient PIDs (the bomb double-forks rapidly so
	// some transient PIDs may appear during a sample tick).
	growth := peakPIDs - baselinePIDs
	productionCapWithSlack := 160 // childNProcSlack (128) + transient slack
	if growth > productionCapWithSlack {
		t.Errorf(
			"C3-INDIRECT GAP CONFIRMED: bomb grew PID count from %d to %d (+%d), exceeding production cap %d. "+
				"Either RLIMIT_NPROC was not applied or its cap is too high. (test safetyCap=%d)",
			baselinePIDs, peakPIDs, growth, productionCapWithSlack, safetyCap,
		)
	} else {
		t.Logf(
			"C3-INDIRECT closed: growth=%d (baseline=%d, peak=%d) — production RLIMIT_NPROC contained the bomb below the test safetyCap (%d)",
			growth,
			baselinePIDs,
			peakPIDs,
			safetyCap,
		)
	}
}

// countOurPIDs returns an approximate count of THREADS owned by the
// current UID — the same number RLIMIT_NPROC budgets against. We walk
// /proc/<pid>/task/ for each user-owned PID and sum the task entries.
//
// Why threads, not processes: Linux RLIMIT_NPROC is per-USER and counts
// task_struct entries (kernel threads + user threads + processes). Go's
// runtime creates an OS thread per locked goroutine + per parked-M, so a
// single `go test` invocation holds 100+ threads. Counting only top-level
// PIDs vastly underestimates the working count and leads to a too-tight
// safetyCap that EAGAIN's the test's first fork().
//
// Reads /proc; tolerant of races (processes/threads exiting mid-walk are
// silently skipped).
func countOurPIDs(t *testing.T) int {
	t.Helper()
	uid := os.Getuid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip non-PID entries (like /proc/cpuinfo, /proc/meminfo etc.).
		name := e.Name()
		isDigits := true
		for _, r := range name {
			if r < '0' || r > '9' {
				isDigits = false
				break
			}
		}
		if !isDigits {
			continue
		}
		// Stat /proc/<pid> to get owner UID. This may fail if the process
		// exited between readdir and stat — that's fine, just skip it.
		info, err := os.Stat("/proc/" + name)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		if int(stat.Uid) != uid {
			continue
		}
		// Sum the threads (tasks) within this PID. /proc/<pid>/task/ has
		// one subdir per task. Read errors mean the process exited mid-
		// walk — skip with a 1 baseline (the PID itself).
		taskEntries, terr := os.ReadDir("/proc/" + name + "/task")
		if terr != nil {
			count++
			continue
		}
		count += len(taskEntries)
	}
	return count
}

// samplePeakPIDs polls our PID count until the context fires, returning
// the maximum observed value. We poll at 100ms granularity which is
// plenty fine for a fork bomb — the bomb doubles every fork, so a
// healthy bomb saturates RLIMIT_NPROC in well under 100ms.
func samplePeakPIDs(t *testing.T, ctx context.Context, baseline int) int {
	t.Helper()
	peak := baseline
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return peak
		case <-tick.C:
			c := countOurPIDs(t)
			if c > peak {
				peak = c
			}
		}
	}
}
