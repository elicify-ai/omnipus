// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestParseTaskCompletionSignal_Success verifies the basic success marker with
// no summary: Found()/Status() report success and Result falls back to the
// full raw output (ADR-043's "if absent, the full response remains the
// Result").
func TestParseTaskCompletionSignal_Success(t *testing.T) {
	out := "I finished the work.\nTASK_STATUS: success"
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true")
	}
	if sig.Verdict != verdictSuccess {
		t.Errorf("Verdict = %v, want verdictSuccess", sig.Verdict)
	}
	if sig.Status() != task.StatusDone {
		t.Errorf("Status() = %v, want task.StatusDone", sig.Status())
	}
	if sig.Result != out {
		t.Errorf("Result = %q, want the full raw output %q (no TASK_SUMMARY present)", sig.Result, out)
	}
}

// TestParseTaskCompletionSignal_Failure verifies the failure marker: Found()
// is true, Status() is Failed, and (with no summary) Result is the agent's
// own words in full — never the old "no signal" framing, since the agent DID
// report.
func TestParseTaskCompletionSignal_Failure(t *testing.T) {
	out := "I could not finish: missing credentials.\nTASK_STATUS: failure"
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true")
	}
	if sig.Verdict != verdictFailure {
		t.Errorf("Verdict = %v, want verdictFailure", sig.Verdict)
	}
	if sig.Status() != task.StatusFailed {
		t.Errorf("Status() = %v, want task.StatusFailed", sig.Status())
	}
	if !strings.Contains(sig.Result, "missing credentials") {
		t.Errorf("Result = %q, want it to contain the agent's own words", sig.Result)
	}
}

// TestParseTaskCompletionSignal_WithSummary verifies that a TASK_SUMMARY line
// following TASK_STATUS becomes the Result, replacing the full response.
func TestParseTaskCompletionSignal_WithSummary(t *testing.T) {
	out := "Lots of reasoning here.\nMore reasoning.\nTASK_STATUS: success\nTASK_SUMMARY: Deployed the fix to prod."
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() || sig.Verdict != verdictSuccess {
		t.Fatalf("Found()=%v Verdict=%v, want true/verdictSuccess", sig.Found(), sig.Verdict)
	}
	want := "Deployed the fix to prod."
	if sig.Result != want {
		t.Errorf("Result = %q, want %q (summary only, reasoning excluded)", sig.Result, want)
	}
}

// TestParseTaskCompletionSignal_SummaryWithTrailingLines verifies that lines
// after TASK_SUMMARY (until end of output) are folded into Result too.
func TestParseTaskCompletionSignal_SummaryWithTrailingLines(t *testing.T) {
	out := "TASK_STATUS: success\nTASK_SUMMARY: Migrated the database.\nAll 12 tables migrated.\nNo data loss."
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() || sig.Verdict != verdictSuccess {
		t.Fatalf("Found()=%v Verdict=%v, want true/verdictSuccess", sig.Found(), sig.Verdict)
	}
	want := "Migrated the database.\nAll 12 tables migrated.\nNo data loss."
	if sig.Result != want {
		t.Errorf("Result = %q, want %q", sig.Result, want)
	}
}

// TestParseTaskCompletionSignal_SummaryWithTrailingLines_CRLF is the review A4
// regression test: a CRLF-terminated output must not leak literal \r bytes
// into the folded trailing lines of Result.
func TestParseTaskCompletionSignal_SummaryWithTrailingLines_CRLF(t *testing.T) {
	out := "TASK_STATUS: success\r\nTASK_SUMMARY: Migrated the database.\r\nAll 12 tables migrated.\r\nNo data loss.\r\n"
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() || sig.Verdict != verdictSuccess {
		t.Fatalf("Found()=%v Verdict=%v, want true/verdictSuccess", sig.Found(), sig.Verdict)
	}
	if strings.Contains(sig.Result, "\r") {
		t.Errorf("Result = %q, must not contain a literal \\r (CRLF must be normalized)", sig.Result)
	}
	want := "Migrated the database.\nAll 12 tables migrated.\nNo data loss."
	if sig.Result != want {
		t.Errorf("Result = %q, want %q", sig.Result, want)
	}
}

// TestParseTaskCompletionSignal_LastOccurrenceWins verifies that when the
// marker appears more than once as a genuine standalone line (e.g. a model
// echoing the instruction early in its own reasoning), the LAST occurrence is
// authoritative. Both lines here are real, standalone matches (review A3):
// a prior version of this test used a first "line" that was actually
// embedded mid-sentence and never matched the parser at all, so it only ever
// exercised "one match, and it wins" — this version has two real matches.
func TestParseTaskCompletionSignal_LastOccurrenceWins(t *testing.T) {
	out := "TASK_STATUS: failure\n" +
		"I actually succeeded though.\n" +
		"TASK_STATUS: success\n" +
		"TASK_SUMMARY: All good."
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true")
	}
	if sig.Verdict != verdictSuccess {
		t.Error("Verdict != verdictSuccess — the LAST TASK_STATUS line must win, not the first")
	}
	if sig.Result != "All good." {
		t.Errorf("Result = %q, want %q", sig.Result, "All good.")
	}
}

// TestParseTaskCompletionSignal_UnfencedLaterMarkerFlipsVerdict_KnownLimitation
// pins review A3's accepted, documented limitation: a genuine TASK_STATUS
// marker whose TASK_SUMMARY body later quotes an UNFENCED standalone
// TASK_STATUS line has its verdict flipped by that later line. This is
// deliberate (last-occurrence-wins is simple and predictable) and is
// documented in ADR-043 §3 — fencing the quote (or avoiding bare marker lines
// in prose) is the mitigation, not a parser special-case.
func TestParseTaskCompletionSignal_UnfencedLaterMarkerFlipsVerdict_KnownLimitation(t *testing.T) {
	out := "TASK_STATUS: success\n" +
		"TASK_SUMMARY: All done.\n" +
		"TASK_STATUS: failure"
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true")
	}
	if sig.Verdict != verdictFailure {
		t.Errorf("Verdict = %v, want verdictFailure — an unfenced later marker line must still flip the verdict "+
			"(known, accepted limitation, ADR-043 §3)", sig.Verdict)
	}
}

// TestParseTaskCompletionSignal_FencedMarkerIgnored_FailsClosed is review A1's
// exact repro: a model explains the expected format by quoting the marker
// inside a fenced code block, while the surrounding prose makes clear it is
// NOT actually done yet. Before fence-awareness this was a false success; it
// must now fail closed (Found() == false).
func TestParseTaskCompletionSignal_FencedMarkerIgnored_FailsClosed(t *testing.T) {
	out := "I'll report like this:\n" +
		"```\n" +
		"TASK_STATUS: success\n" +
		"```\n" +
		"I'm about 60% done and still working on the remaining tests."
	sig := parseTaskCompletionSignal(out)
	if sig.Found() {
		t.Errorf("Found() = true, want false — a marker quoted inside a fenced code block must not "+
			"count as a real signal (verdict=%v result=%q)", sig.Verdict, sig.Result)
	}
}

// TestParseTaskCompletionSignal_FencedMarkerIgnored_TildeFence covers the
// ~~~ fence delimiter variant (CommonMark allows either).
func TestParseTaskCompletionSignal_FencedMarkerIgnored_TildeFence(t *testing.T) {
	out := "Example:\n~~~\nTASK_STATUS: success\n~~~\nStill working on it."
	sig := parseTaskCompletionSignal(out)
	if sig.Found() {
		t.Error("Found() = true, want false — a marker inside a ~~~ fence must not count as real")
	}
}

// TestParseTaskCompletionSignal_FencedExampleThenRealMarkerOutside_Found
// verifies fence-awareness does not over-suppress: a real, unfenced marker
// AFTER a fenced formatting example must still be found and must still win
// (last-occurrence-wins operates over non-fenced lines only).
func TestParseTaskCompletionSignal_FencedExampleThenRealMarkerOutside_Found(t *testing.T) {
	out := "Example format:\n```\nTASK_STATUS: success\n```\n" +
		"Actual result:\nTASK_STATUS: failure\nTASK_SUMMARY: Ran into an error."
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true — the real, unfenced marker must still be found")
	}
	if sig.Verdict != verdictFailure {
		t.Errorf("Verdict = %v, want verdictFailure (the fenced example must be ignored)", sig.Verdict)
	}
	if sig.Result != "Ran into an error." {
		t.Errorf("Result = %q, want %q", sig.Result, "Ran into an error.")
	}
}

// TestComputeFencedLines_MixedDelimiterTypes is hardening-review finding H1:
// CommonMark requires a fence's closing delimiter to match its opener's type
// (``` closes only ```, ~~~ closes only ~~~). Before the fix, computeFencedLines
// toggled a single bool on EITHER delimiter, so the OTHER type wrongly closed
// an open fence early. Both orderings are covered directly against
// computeFencedLines (rather than only through parseTaskCompletionSignal) so a
// future change to the fenced-boolean semantics is caught at the unit level.
func TestComputeFencedLines_MixedDelimiterTypes(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []bool
	}{
		{
			name:  "backtick-opened fence: a ~~~ line inside does not close it",
			lines: []string{"```", "example text", "~~~", "TASK_STATUS: success", "```"},
			want:  []bool{true, true, true, true, true},
		},
		{
			name:  "tilde-opened fence: a ``` line inside does not close it",
			lines: []string{"~~~", "example text", "```", "TASK_STATUS: success", "~~~"},
			want:  []bool{true, true, true, true, true},
		},
		{
			name: "backtick-opened fence properly closed by matching backtick: line after is unfenced",
			lines: []string{
				"```", "example text", "~~~", "TASK_STATUS: success", "```", "TASK_STATUS: failure",
			},
			want: []bool{true, true, true, true, true, false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeFencedLines(tc.lines)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("fenced[%d] = %v, want %v (line %q)", i, got[i], tc.want[i], tc.lines[i])
				}
			}
		})
	}
}

// TestParseTaskCompletionSignal_MixedFenceDelimiters_DoNotCrossClose is the
// integration-level repro for finding H1: "```\nexample\n~~~\nTASK_STATUS:
// success\n```" previously parsed as SUCCESS because the ~~~ line wrongly
// closed the ``` fence early, un-fencing the marker. With matching-delimiter
// fence tracking, the marker stays fenced all the way through the real
// (matching-type) closing delimiter, so there is no unfenced TASK_STATUS line
// anywhere in the output — Found() must be false. Both delimiter orderings are
// covered.
func TestParseTaskCompletionSignal_MixedFenceDelimiters_DoNotCrossClose(t *testing.T) {
	cases := []struct{ name, out string }{
		{
			"backtick-opened fence with a ~~~ line inside stays fenced until the matching ``` closes",
			"```\nexample\n~~~\nTASK_STATUS: success\n```",
		},
		{
			"tilde-opened fence with a ``` line inside stays fenced until the matching ~~~ closes",
			"~~~\nexample\n```\nTASK_STATUS: success\n~~~",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := parseTaskCompletionSignal(tc.out)
			if sig.Found() {
				t.Errorf("Found() = true (verdict=%v), want false — the mismatched delimiter inside the "+
					"fence must not close it early, so the marker stays fenced", sig.Verdict)
			}
		})
	}
}

// TestParseTaskCompletionSignal_MixedFenceDelimiters_RealMarkerAfterProperCloseFound
// proves the H1 fix does not over-fence: once a mixed-delimiter fenced example
// is properly closed by its OWN opening delimiter type, a real marker after it
// must still be found — matching-delimiter fence tracking must not leak into
// "fenced forever" once the true close is seen.
func TestParseTaskCompletionSignal_MixedFenceDelimiters_RealMarkerAfterProperCloseFound(t *testing.T) {
	out := "```\nexample\n~~~\nTASK_STATUS: success\n```\nTASK_STATUS: failure"
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true — the real marker after the properly-closed fence must be found")
	}
	if sig.Verdict != verdictFailure {
		t.Errorf("Verdict = %v, want verdictFailure (the mixed-delimiter fenced example must be ignored)",
			sig.Verdict)
	}
}

// TestParseTaskCompletionSignal_BulletPrefixedLine_NotAMarker is hardening
// review finding H2: a model paraphrasing the TASK_STATUS instruction as a
// markdown bullet or numbered-list item must not be read as a real signal —
// including the false-success repro where the bulleted SUCCESS variant
// happens to come last (last-occurrence-wins would otherwise pick it up).
// Bold emphasis (`**TASK_STATUS**: ...`) is deliberately NOT a bullet (no
// whitespace immediately after the first `*`) and must still match.
func TestParseTaskCompletionSignal_BulletPrefixedLine_NotAMarker(t *testing.T) {
	t.Run("asterisk bullet, plain", func(t *testing.T) {
		sig := parseTaskCompletionSignal("* TASK_STATUS: success")
		if sig.Found() {
			t.Errorf("Found() = true (verdict=%v), want false — a `* ` bulleted line is not a marker line",
				sig.Verdict)
		}
	})
	t.Run("asterisk-bulleted echo of both variants, success LAST (false-success repro)", func(t *testing.T) {
		out := "* `TASK_STATUS: failure` - otherwise\n* `TASK_STATUS: success` - if all tests pass"
		sig := parseTaskCompletionSignal(out)
		if sig.Found() {
			t.Errorf("Found() = true (verdict=%v), want false — a bulleted paraphrase of the instruction "+
				"lines must never resolve to a signal, even when the SUCCESS bullet comes last", sig.Verdict)
		}
	})
	t.Run("hyphen bullet rejected", func(t *testing.T) {
		sig := parseTaskCompletionSignal("- TASK_STATUS: success")
		if sig.Found() {
			t.Error("Found() = true, want false — a `- ` bulleted line is not a marker line")
		}
	})
	t.Run("plus bullet rejected", func(t *testing.T) {
		sig := parseTaskCompletionSignal("+ TASK_STATUS: success")
		if sig.Found() {
			t.Error("Found() = true, want false — a `+ ` bulleted line is not a marker line")
		}
	})
	t.Run("numbered list rejected", func(t *testing.T) {
		sig := parseTaskCompletionSignal("1. TASK_STATUS: success")
		if sig.Found() {
			t.Error("Found() = true, want false — a numbered-list-prefixed line is not a marker line")
		}
	})
	t.Run("bold marker still matches despite leading double-asterisk", func(t *testing.T) {
		sig := parseTaskCompletionSignal("**TASK_STATUS**: success")
		if !sig.Found() || sig.Verdict != verdictSuccess {
			t.Errorf("Found()=%v Verdict=%v, want true/verdictSuccess — bold emphasis (`**`, no whitespace "+
				"after the first `*`) must not be mistaken for a bullet", sig.Found(), sig.Verdict)
		}
	})
}

// TestParseTaskCompletionSignal_IndentedCodeBlock_NotAMarker is hardening
// review finding H3: markdown treats a line with a leading tab, or
// 4-or-more leading spaces, as an indented code block — a marker shown that
// way is a formatting example, not a real signal. A 2-space indent (what
// buildPrompt's own instruction lines use, task_executor.go) must still match.
func TestParseTaskCompletionSignal_IndentedCodeBlock_NotAMarker(t *testing.T) {
	t.Run("4-space indent excluded (indented code block)", func(t *testing.T) {
		sig := parseTaskCompletionSignal("Here is the code:\n    TASK_STATUS: success\nStill working on it.")
		if sig.Found() {
			t.Errorf("Found() = true (verdict=%v), want false — a >=4-space-indented line is a markdown "+
				"indented code block, not a marker line", sig.Verdict)
		}
	})
	t.Run("2-space indent still matches", func(t *testing.T) {
		sig := parseTaskCompletionSignal("  TASK_STATUS: success")
		if !sig.Found() || sig.Verdict != verdictSuccess {
			t.Errorf("Found()=%v Verdict=%v, want true/verdictSuccess — a 2-space indent is below the "+
				"4-space indented-code-block threshold and must still match", sig.Found(), sig.Verdict)
		}
	})
	t.Run("leading tab excluded", func(t *testing.T) {
		sig := parseTaskCompletionSignal("\tTASK_STATUS: success")
		if sig.Found() {
			t.Error("Found() = true, want false — a leading-tab-indented line is a markdown indented code block")
		}
	})
}

// TestParseTaskCompletionSignal_UnclosedFence_FailsClosed_KnownLimitation is
// hardening review finding H4 — the accepted-safe direction, unchanged by this
// hardening pass: an unbalanced opening fence with no matching close anywhere
// in the output causes computeFencedLines to treat everything from the opener
// through the end of the output as fenced (its documented, conservative
// choice), so a genuine trailing marker inside that unterminated fence is
// swallowed and the task fails closed. This is pinned deliberately — do not
// "fix" it by closing an unterminated fence at EOF; fail-closed is the safe
// direction here, not a bug (ADR-043 §3).
func TestParseTaskCompletionSignal_UnclosedFence_FailsClosed_KnownLimitation(t *testing.T) {
	out := "```\nsome example\nTASK_STATUS: success\n"
	sig := parseTaskCompletionSignal(out)
	if sig.Found() {
		t.Errorf("Found() = true (verdict=%v), want false — an unterminated fence must swallow a genuine "+
			"trailing marker and fail closed (known, accepted limitation, ADR-043 §3)", sig.Verdict)
	}
}

// TestParseTaskCompletionSignal_BlockquotePrefixed_NotAMarker pins a
// deliberate, accepted gap (N1 in the hardening review): a blockquote-prefixed
// marker line ("> TASK_STATUS: success") does not match — the leading "> " is
// not part of the tolerated wrapper class — so the task fails closed rather
// than a future reader mistaking the silent non-match for an oversight.
func TestParseTaskCompletionSignal_BlockquotePrefixed_NotAMarker(t *testing.T) {
	sig := parseTaskCompletionSignal("> TASK_STATUS: success")
	if sig.Found() {
		t.Error("Found() = true, want false — a blockquote-prefixed marker line does not match (accepted gap)")
	}
}

// TestParseTaskCompletionSignal_MarkdownWrapped verifies tolerance of common
// markdown emphasis wrapping the marker line (bold, backticks).
func TestParseTaskCompletionSignal_MarkdownWrapped(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"bold whole line", "**TASK_STATUS: success**"},
		{"backtick whole line", "`TASK_STATUS: success`"},
		{"bold value only", "TASK_STATUS: **success**"},
		{"bold label only, colon inside wrapper", "**TASK_STATUS:** success"},
		{"bold label only, colon outside wrapper", "**TASK_STATUS**: success"},
		{"leading/trailing whitespace", "   TASK_STATUS: success   "},
		{"lowercase", "task_status: success"},
		{"mixed case", "Task_Status: SUCCESS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := parseTaskCompletionSignal("some output\n" + tc.line)
			if !sig.Found() {
				t.Fatalf("Found() = false for line %q, want true", tc.line)
			}
			if sig.Verdict != verdictSuccess {
				t.Errorf("Verdict = %v for line %q, want verdictSuccess", sig.Verdict, tc.line)
			}
		})
	}
}

// TestParseTaskCompletionSignal_TrailingContentTolerance is review A2's
// positive matrix: a marker line followed by trailing punctuation or short
// trailing prose must still parse, and the captured value must be the FIRST
// success|failure token after the colon — a different status word appearing
// later in the same line's trailing prose must never flip the captured value.
func TestParseTaskCompletionSignal_TrailingContentTolerance(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantVerdict completionVerdict
	}{
		{"trailing period", "TASK_STATUS: success.", verdictSuccess},
		{"trailing prose with em dash", "TASK_STATUS: success — all tests pass", verdictSuccess},
		{"bold label only, colon outside wrapper", "**TASK_STATUS**: success", verdictSuccess},
		{
			"failure with trailing prose mentioning success",
			"TASK_STATUS: failure but almost succeeded",
			verdictFailure,
		},
		{
			"success with trailing prose mentioning failure",
			"TASK_STATUS: success but almost failure occurred",
			verdictSuccess,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := parseTaskCompletionSignal("some output\n" + tc.line)
			if !sig.Found() {
				t.Fatalf("Found() = false for line %q, want true", tc.line)
			}
			if sig.Verdict != tc.wantVerdict {
				t.Errorf("Verdict = %v for line %q, want %v", sig.Verdict, tc.line, tc.wantVerdict)
			}
		})
	}
}

// TestParseTaskCompletionSignal_TrailingContentTolerance_Negatives is review
// A2's negative matrix: hedge values still never match, and a value that
// merely starts with "success"/"failure" as a text prefix (e.g. "successful")
// must not be misread as the recognized value — the \b word-boundary guard.
func TestParseTaskCompletionSignal_TrailingContentTolerance_Negatives(t *testing.T) {
	cases := []struct{ name, line string }{
		{"hedge value maybe", "TASK_STATUS: maybe"},
		{"successful is not success", "TASK_STATUS: successful"},
		{"failured is not failure", "TASK_STATUS: failured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := parseTaskCompletionSignal(tc.line)
			if sig.Found() {
				t.Errorf("Found() = true for line %q, want false (verdict=%v)", tc.line, sig.Verdict)
			}
		})
	}
}

// TestParseTaskCompletionSignal_AbsentMarker verifies that output with no
// TASK_STATUS line at all yields Found() == false — the fail-closed trigger.
func TestParseTaskCompletionSignal_AbsentMarker(t *testing.T) {
	sig := parseTaskCompletionSignal("I did some work and that's it, no marker here.")
	if sig.Found() {
		t.Error("Found() = true, want false — there is no TASK_STATUS line in this output")
	}
}

// TestParseTaskCompletionSignal_EmptyOutput verifies that empty (or
// whitespace-only) output yields Found() == false, never an implicit success
// — this is the specific regression the old "Task completed" default caused.
func TestParseTaskCompletionSignal_EmptyOutput(t *testing.T) {
	for _, out := range []string{"", "   ", "\n\n\t \n"} {
		sig := parseTaskCompletionSignal(out)
		if sig.Found() {
			t.Errorf("Found() = true for output %q, want false", out)
		}
	}
}

// TestParseTaskCompletionSignal_MalformedValueIgnored verifies that a
// TASK_STATUS line with a value other than success/failure does not match —
// it is simply not a recognized signal (fail-closed if it's the only line).
func TestParseTaskCompletionSignal_MalformedValueIgnored(t *testing.T) {
	sig := parseTaskCompletionSignal("TASK_STATUS: maybe\nTASK_STATUS: unknown")
	if sig.Found() {
		t.Error("Found() = true, want false — neither line has a valid success/failure value")
	}
}

// TestParseTaskCompletionSignal_SummaryAbsentUsesFullOutput double-checks the
// "no summary" branch keeps the ENTIRE raw output (including the marker line
// itself) as Result, per ADR-043 ("if absent, the full response remains the
// Result").
func TestParseTaskCompletionSignal_SummaryAbsentUsesFullOutput(t *testing.T) {
	out := "Step 1 done.\nStep 2 done.\nTASK_STATUS: success"
	sig := parseTaskCompletionSignal(out)
	if sig.Result != out {
		t.Errorf("Result = %q, want the exact full output %q", sig.Result, out)
	}
}

// TestParseTaskCompletionSignal_FoundSignalResultIsCapped is review A6's
// second half: Result is bounded even on the found-signal path (previously
// unbounded when no TASK_SUMMARY was present), at the generous
// maxFoundSignalResultChars cap.
func TestParseTaskCompletionSignal_FoundSignalResultIsCapped(t *testing.T) {
	long := strings.Repeat("x", maxFoundSignalResultChars+500)
	out := long + "\nTASK_STATUS: success"
	sig := parseTaskCompletionSignal(out)
	if !sig.Found() {
		t.Fatal("Found() = false, want true")
	}
	if len(sig.Result) >= len(out) {
		t.Errorf("Result was not truncated: len(Result)=%d, len(out)=%d", len(sig.Result), len(out))
	}
	if !strings.Contains(sig.Result, "truncated") {
		t.Error("truncated Result must note that it was truncated")
	}
}

// TestTruncateTaskOutput verifies the fail-closed output bound: short output
// passes through unchanged, long output is truncated with a note.
func TestTruncateTaskOutput(t *testing.T) {
	short := "short output"
	if got := truncateTaskOutput(short); got != short {
		t.Errorf("short output was modified: got %q, want %q", got, short)
	}

	long := strings.Repeat("x", maxFailClosedOutputChars+500)
	got := truncateTaskOutput(long)
	if len(got) >= len(long) {
		t.Errorf("truncateTaskOutput did not shorten a %d-char input", len(long))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncated output must note that it was truncated")
	}
	if !strings.HasPrefix(got, strings.Repeat("x", maxFailClosedOutputChars)) {
		t.Error("truncated output must retain the first maxFailClosedOutputChars characters")
	}
}

// TestTruncateTaskOutput_RuneSafe is review A6's regression test: truncation
// must cut on a rune boundary, never split a multi-byte UTF-8 rune. "é" is a
// 2-byte UTF-8 sequence; a byte-index slice at exactly maxFailClosedOutputChars
// bytes would land mid-rune for an input built entirely from 2-byte runes.
func TestTruncateTaskOutput_RuneSafe(t *testing.T) {
	long := strings.Repeat("é", maxFailClosedOutputChars+100)
	got := truncateTaskOutput(long)
	// The kept portion (everything before the "\n... (truncated" suffix) must
	// be valid UTF-8 and must be exactly maxFailClosedOutputChars runes of "é".
	idx := strings.Index(got, "\n... (truncated")
	if idx < 0 {
		t.Fatalf("truncated output missing the truncation suffix: %q", got)
	}
	kept := got[:idx]
	if !strings.HasPrefix(kept, strings.Repeat("é", maxFailClosedOutputChars)) {
		t.Errorf("truncation split a multi-byte rune: kept = %q", kept)
	}
	if got := []rune(kept); len(got) != maxFailClosedOutputChars {
		t.Errorf("kept rune count = %d, want %d", len(got), maxFailClosedOutputChars)
	}
}

// --- ADR-052 evidence-marker gate (FR-035 / DS-8) ---

// TestEvidenceMarkerGate_ClaimWithEvidenceHonored covers DS-8's non-attack
// baseline: a genuine [goal:evidence] line immediately before TASK_STATUS
// must be honored, with no steering text.
func TestEvidenceMarkerGate_ClaimWithEvidenceHonored(t *testing.T) {
	out := "I ran the checks.\n[goal:evidence] confirmed the 5 web_search calls succeeded\nTASK_STATUS: success"
	v := checkEvidenceMarkerGate(out)

	if !v.Applicable {
		t.Fatal("Applicable = false, want true")
	}
	if !v.Honored {
		t.Errorf("Honored = false, want true")
	}
	if v.SteeringText != "" {
		t.Errorf("SteeringText = %q, want empty when honored", v.SteeringText)
	}
}

// TestEvidenceMarkerGate_BareClaimRejected covers FR-035 / DS-8
// "evidence-free completion claim": a TASK_STATUS marker with no preceding
// [goal:evidence] line at all is auto-rejected with non-empty steering text
// that names both markers.
func TestEvidenceMarkerGate_BareClaimRejected(t *testing.T) {
	out := "I finished the work.\nTASK_STATUS: success"
	v := checkEvidenceMarkerGate(out)

	if !v.Applicable {
		t.Fatal("Applicable = false, want true")
	}
	if v.Honored {
		t.Error("Honored = true, want false — no [goal:evidence] line preceded the marker")
	}
	if v.SteeringText == "" {
		t.Fatal("SteeringText is empty, want a re-prompt")
	}
	if !strings.Contains(v.SteeringText, "[goal:evidence]") {
		t.Errorf("SteeringText = %q, want it to mention the [goal:evidence] marker", v.SteeringText)
	}
	if !strings.Contains(v.SteeringText, "TASK_STATUS") {
		t.Errorf("SteeringText = %q, want it to mention the completion marker", v.SteeringText)
	}
}

// TestEvidenceMarkerGate_EmptyEvidenceLineRejected covers a [goal:evidence]
// label present with no (or whitespace-only) trailing text — a bare label is
// not evidence.
func TestEvidenceMarkerGate_EmptyEvidenceLineRejected(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"no trailing text", "[goal:evidence]\nTASK_STATUS: success"},
		{"whitespace-only trailing text", "[goal:evidence]   \nTASK_STATUS: success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := checkEvidenceMarkerGate(tc.out)
			if !v.Applicable {
				t.Fatal("Applicable = false, want true")
			}
			if v.Honored {
				t.Error("Honored = true, want false — an empty evidence line is a bare label, not evidence")
			}
			if v.SteeringText == "" {
				t.Error("SteeringText is empty, want a re-prompt")
			}
		})
	}
}

// TestEvidenceMarkerGate_NoCompletionMarker_NotApplicable covers output with
// no TASK_STATUS line at all — the gate has nothing to say (the existing
// verdictNotFound fail-closed path in parseTaskCompletionSignal already
// covers that case on its own).
func TestEvidenceMarkerGate_NoCompletionMarker_NotApplicable(t *testing.T) {
	cases := []string{
		"",
		"   \n  ",
		"just some prose with no marker at all",
		"[goal:evidence] I verified everything, but never claimed completion",
	}
	for _, out := range cases {
		v := checkEvidenceMarkerGate(out)
		if v.Applicable {
			t.Errorf("checkEvidenceMarkerGate(%q).Applicable = true, want false", out)
		}
		if v.Honored {
			t.Errorf("checkEvidenceMarkerGate(%q).Honored = true, want false when not applicable", out)
		}
	}
}

// TestEvidenceMarkerGate_FailureClaimAlsoGated proves the gate applies to the
// marker family generically (success OR failure), matching ADR-052's
// "completion claim" framing rather than only gating success claims.
func TestEvidenceMarkerGate_FailureClaimAlsoGated(t *testing.T) {
	bare := checkEvidenceMarkerGate("I gave up.\nTASK_STATUS: failure")
	if !bare.Applicable || bare.Honored {
		t.Errorf("bare failure claim: got Applicable=%v Honored=%v, want Applicable=true Honored=false",
			bare.Applicable, bare.Honored)
	}

	withEvidence := checkEvidenceMarkerGate(
		"I gave up.\n[goal:evidence] attempted the fix twice, both times the tests still failed\nTASK_STATUS: failure",
	)
	if !withEvidence.Applicable || !withEvidence.Honored {
		t.Errorf("failure claim with evidence: got Applicable=%v Honored=%v, want both true",
			withEvidence.Applicable, withEvidence.Honored)
	}
}

// TestEvidenceMarkerGate_BlankLinesTolerated mirrors
// parseTaskCompletionSignal's own tolerance of blank lines between the
// TASK_STATUS marker and a following TASK_SUMMARY line — the evidence gate
// must tolerate blank lines between the evidence line and the marker too.
func TestEvidenceMarkerGate_BlankLinesTolerated(t *testing.T) {
	out := "[goal:evidence] verified the output manually\n\n\nTASK_STATUS: success"
	v := checkEvidenceMarkerGate(out)
	if !v.Applicable || !v.Honored {
		t.Errorf("got Applicable=%v Honored=%v, want both true (blank lines must be tolerated)",
			v.Applicable, v.Honored)
	}
}

// TestEvidenceMarkerGate_FencedEvidenceLineRejected covers a [goal:evidence]
// line that only appears inside a fenced code block immediately before the
// marker — quoting the vocabulary as a formatting example must not count as
// real evidence, matching parseTaskCompletionSignal's own fence-awareness.
func TestEvidenceMarkerGate_FencedEvidenceLineRejected(t *testing.T) {
	out := "Here's the format I'll use:\n```\n[goal:evidence] example text\n```\nTASK_STATUS: success"
	v := checkEvidenceMarkerGate(out)
	if !v.Applicable {
		t.Fatal("Applicable = false, want true")
	}
	if v.Honored {
		t.Error("Honored = true, want false — a fenced evidence line must not count")
	}
}

// TestEvidenceMarkerGate_BulletedEvidenceLineRejected covers a
// [goal:evidence] line quoted as a bulleted list item immediately before the
// marker — paraphrasing the vocabulary as a bullet is not emitting it,
// mirroring isExcludedMarkerLine's existing bullet exclusion for
// TASK_STATUS/TASK_SUMMARY.
func TestEvidenceMarkerGate_BulletedEvidenceLineRejected(t *testing.T) {
	out := "* [goal:evidence] fake bullet quoting the format\nTASK_STATUS: success"
	v := checkEvidenceMarkerGate(out)
	if !v.Applicable {
		t.Fatal("Applicable = false, want true")
	}
	if v.Honored {
		t.Error("Honored = true, want false — a bulleted evidence line must not count")
	}
}

// TestEvidenceMarkerGate_MarkdownEmphasisTolerated covers the same
// bold/italic/code-span tolerance the TASK_STATUS/TASK_SUMMARY regexes
// already apply, now for the evidence marker.
func TestEvidenceMarkerGate_MarkdownEmphasisTolerated(t *testing.T) {
	cases := []string{
		"**[goal:evidence]** verified thoroughly\nTASK_STATUS: success",
		"[goal:evidence]: verified thoroughly\nTASK_STATUS: success",
		"[GOAL:EVIDENCE] verified thoroughly (case-insensitive)\nTASK_STATUS: success",
	}
	for _, out := range cases {
		v := checkEvidenceMarkerGate(out)
		if !v.Applicable || !v.Honored {
			t.Errorf("checkEvidenceMarkerGate(%q): got Applicable=%v Honored=%v, want both true",
				out, v.Applicable, v.Honored)
		}
	}
}
