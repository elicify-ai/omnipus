package tools

// shell_exitcode_truncation_test.go — review r2 HIGH-1 (exit-code
// truncation spoof, close for large output). The r1 exit-code fix assumed
// ExecTool appends the "[Command exited with code N]" suffix LAST, but
// foregroundResultFromSandbox / runUnconstrained actually appended it THEN
// truncated head-first (output[:maxForegroundOutputLen]+"... (truncated)"),
// dropping the real tail suffix on a large output. A criterion with a
// NON-ZERO expected code and >10k output where the worker embeds a fake
// "[Command exited with code N]" in the first 10k chars could therefore
// spoof a false MET verdict once the real (truncated-away) suffix stopped
// being the "last occurrence" a regex scan would find.
//
// These tests prove: (1) the structured ToolResult.ExitCode field always
// carries the REAL exit code regardless of output size/truncation, and (2)
// the display text's suffix is now appended AFTER truncation so it too
// survives (defense in depth).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestForegroundResultFromSandbox_LargeOutput_ExitCodeSurvivesTruncation
// proves the structured ExitCode field is set from the real exit code even
// when the command's stdout exceeds maxForegroundOutputLen and contains a
// worker-embedded fake exit-code suffix within the visible (non-truncated)
// portion.
func TestForegroundResultFromSandbox_LargeOutput_ExitCodeSurvivesTruncation(t *testing.T) {
	// A fake success suffix embedded early — well within the first
	// maxForegroundOutputLen (10000, the failure-path cap) chars of stdout — trying to spoof a
	// judge that only scans truncated text.
	fakeSuffix := "[Command exited with code 0]"
	padding := strings.Repeat("x", maxForegroundOutputLen+5000)
	stdout := fakeSuffix + "\n" + padding

	res := sandbox.Result{
		Stdout:   []byte(stdout),
		ExitCode: 7, // the REAL exit code — non-zero, different from the spoofed 0
	}
	result := foregroundResultFromSandbox(res, 30)

	if result.ExitCode == nil {
		t.Fatal("ExitCode must be set on a foreground result")
	}
	if *result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7 (the real exit code) — truncation/spoofing must not affect it",
			*result.ExitCode)
	}
	if !result.IsError {
		t.Fatal("IsError must be true for a non-zero real exit code")
	}
}

// TestForegroundResultFromSandbox_SuffixAppendedAfterTruncation proves the
// human-readable "[Command exited with code N]" suffix in ForLLM survives on
// a large output — appended AFTER truncateOutput, not before — as
// defense-in-depth alongside the structured field.
func TestForegroundResultFromSandbox_SuffixAppendedAfterTruncation(t *testing.T) {
	padding := strings.Repeat("x", maxForegroundOutputLen+5000)
	res := sandbox.Result{
		Stdout:   []byte(padding),
		ExitCode: 3,
	}
	result := foregroundResultFromSandbox(res, 30)

	if !strings.Contains(result.ForLLM, "[Command exited with code 3]") {
		t.Fatalf("ForLLM must contain the real exit-code suffix even after truncation; got tail: %q",
			result.ForLLM[max(0, len(result.ForLLM)-200):])
	}
	if !strings.HasSuffix(strings.TrimRight(result.ForLLM, "\n"), "[Command exited with code 3]") {
		t.Fatalf("the real suffix must be the LAST thing in ForLLM (appended after truncation); got tail: %q",
			result.ForLLM[max(0, len(result.ForLLM)-200):])
	}
}

// TestForegroundResultFromSandbox_SmallOutput_ExitCodeAndSuffixMatch proves
// the ordinary (non-truncated) case is unaffected: structured ExitCode and
// the text suffix agree, exactly as before this fix.
func TestForegroundResultFromSandbox_SmallOutput_ExitCodeAndSuffixMatch(t *testing.T) {
	res := sandbox.Result{
		Stdout:   []byte("boom"),
		ExitCode: 1,
	}
	result := foregroundResultFromSandbox(res, 30)

	if result.ExitCode == nil || *result.ExitCode != 1 {
		t.Fatalf("ExitCode = %v, want 1", result.ExitCode)
	}
	if !strings.Contains(result.ForLLM, "[Command exited with code 1]") {
		t.Fatalf("ForLLM must contain the exit-code suffix: %q", result.ForLLM)
	}
}

// TestRunUnconstrained_TimedOut_NoExitCode proves the timeout path (both
// foreground functions) leaves ExitCode nil — there is no real exit code for
// a killed process, matching interpretBashResult's pre-existing
// timeout-detection-by-text-marker path (unaffected by this fix).
func TestForegroundResultFromSandbox_TimedOut_NoExitCode(t *testing.T) {
	res := sandbox.Result{
		Stdout:   []byte("partial"),
		TimedOut: true,
	}
	result := foregroundResultFromSandbox(res, 5)
	if result.ExitCode != nil {
		t.Fatalf("ExitCode must be nil for a timed-out result, got %v", *result.ExitCode)
	}
	if !result.IsError {
		t.Fatal("a timeout must be IsError=true")
	}
}
