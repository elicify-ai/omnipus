package tools

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestChokePoint_PerSurfaceCap_B15_ToolAlignment covers the B-15 rows of
// ADR-066 spec test 7 (FR-014, US-3.AC9): the shipped tools' own caps are
// aligned to the D4 figures so a builtin result never arrives at the choke
// point already larger than the surface cap it would be held to.
//
//   - shell success output ≤ builtin-success cap (64,000)
//   - shell failure output ≤ builtin-failure cap (10,000)
//   - read_file 64 KB, unchanged
//   - web_fetch defaultMaxChars stays under the builtin-success cap
//
// No per-tool opt-out exists: the constants are derived from
// config.DefaultBuiltinSuccessCap / DefaultBuiltinFailureCap.
func TestChokePoint_PerSurfaceCap_B15_ToolAlignment(t *testing.T) {
	t.Run("constants_track_D4_figures", func(t *testing.T) {
		// Pinned to the literal D4 figure AND to the seeded constant so neither
		// drifting alone goes unnoticed.
		if maxForegroundSuccessOutputLen != 64_000 {
			t.Fatalf("maxForegroundSuccessOutputLen = %d, want 64000", maxForegroundSuccessOutputLen)
		}
		if maxForegroundSuccessOutputLen != config.DefaultBuiltinSuccessCap {
			t.Fatalf("maxForegroundSuccessOutputLen = %d, want DefaultBuiltinSuccessCap %d", maxForegroundSuccessOutputLen, config.DefaultBuiltinSuccessCap)
		}
		if maxForegroundOutputLen != 10_000 {
			t.Fatalf("maxForegroundOutputLen = %d, want 10000", maxForegroundOutputLen)
		}
		if maxForegroundOutputLen != config.DefaultBuiltinFailureCap {
			t.Fatalf("maxForegroundOutputLen = %d, want DefaultBuiltinFailureCap %d", maxForegroundOutputLen, config.DefaultBuiltinFailureCap)
		}
		if MaxReadFileSize != 64*1024 {
			t.Fatalf("MaxReadFileSize = %d, want 65536 (unchanged)", MaxReadFileSize)
		}
		if defaultMaxChars > config.DefaultBuiltinSuccessCap {
			t.Fatalf("web defaultMaxChars = %d exceeds builtin-success cap %d", defaultMaxChars, config.DefaultBuiltinSuccessCap)
		}
	})

	t.Run("shell_success_200000_capped_at_64000", func(t *testing.T) {
		res := sandbox.Result{Stdout: []byte(strings.Repeat("x", 200_000)), ExitCode: 0}
		got := foregroundResultFromSandbox(res, 30)
		if got.IsError {
			t.Fatal("exit 0 must not be an error")
		}
		body := strings.SplitN(got.ForLLM, "\n... (truncated", 2)[0]
		if len(body) != 64_000 {
			t.Fatalf("success body = %d chars, want 64000", len(body))
		}
		if !strings.Contains(got.ForLLM, "136000 more chars") {
			t.Fatalf("truncation marker must state remaining chars; got tail %q", got.ForLLM[len(got.ForLLM)-80:])
		}
	})

	t.Run("shell_success_64000_unmodified", func(t *testing.T) {
		res := sandbox.Result{Stdout: []byte(strings.Repeat("y", 64_000)), ExitCode: 0}
		got := foregroundResultFromSandbox(res, 30)
		if len(got.ForLLM) != 64_000 || strings.Contains(got.ForLLM, "truncated") {
			t.Fatalf("exactly-at-cap success output must pass unmodified; len=%d", len(got.ForLLM))
		}
	})

	t.Run("shell_failure_50000_capped_at_10000", func(t *testing.T) {
		res := sandbox.Result{Stdout: []byte(strings.Repeat("z", 50_000)), ExitCode: 2}
		got := foregroundResultFromSandbox(res, 30)
		if !got.IsError {
			t.Fatal("exit 2 must be an error")
		}
		body := strings.SplitN(got.ForLLM, "\n... (truncated", 2)[0]
		if len(body) != 10_000 {
			t.Fatalf("failure body = %d chars, want 10000", len(body))
		}
		if !strings.Contains(got.ForLLM, "[Command exited with code 2]") {
			t.Fatal("exit-code suffix must survive truncation")
		}
	})

	t.Run("truncateOutput_direct", func(t *testing.T) {
		big := strings.Repeat("a", 100_000)
		if s := truncateOutput(big, 0); !strings.HasPrefix(s, big[:64_000]) || strings.HasPrefix(s, big[:64_001]) {
			t.Fatalf("success path must cut at exactly 64000; len=%d", len(s))
		}
		if s := truncateOutput(big, 1); !strings.HasPrefix(s, big[:10_000]) || strings.HasPrefix(s, big[:10_001]) {
			t.Fatalf("failure path must cut at exactly 10000; len=%d", len(s))
		}
		if s := truncateOutput("", 0); s != "(no output)" {
			t.Fatalf("empty output sentinel changed: %q", s)
		}
	})
}
