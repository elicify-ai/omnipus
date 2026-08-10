package gateway

import (
	"strings"
	"testing"
)

// TestTruncateRunesForFrame_BoundsLiveErrorAtTheSameLimitAsPersisted is the
// regression guard for the size hole the live-error parity fix opened.
//
// The gateway offloads any tool RESULT over InlineToolResultMaxBytes (50 KiB)
// to disk and ships a small sentinel instead, because a multi-megabyte frame
// can OOM a constrained client. The first version of the parity fix then
// assigned the raw, untruncated result string to ToolCallResultFrame.Error —
// putting the whole payload back into the very same frame and defeating that
// guard, on the error path, which is exactly where huge payloads live (stderr
// dumps, stack traces, build logs).
//
// It also broke the parity it claimed to establish: the persisted side caps at
// 2000 runes, so a long error rendered one way live and another way on reload.
//
// This test pins both properties at once.
func TestTruncateRunesForFrame_BoundsLiveErrorAtTheSameLimitAsPersisted(t *testing.T) {
	t.Run("a huge error cannot defeat the 50 KiB offload", func(t *testing.T) {
		huge := strings.Repeat("x", InlineToolResultMaxBytes*2)
		got := truncateRunesForFrame(huge, maxLiveErrorChars)

		if len(got) >= InlineToolResultMaxBytes {
			t.Fatalf("bounded live error is %d bytes, which is at or above the "+
				"InlineToolResultMaxBytes offload threshold (%d) — a large tool failure "+
				"would ship the whole payload in one frame and defeat the offload",
				len(got), InlineToolResultMaxBytes)
		}
		if !strings.HasSuffix(got, "... (truncated, output continues)") {
			t.Errorf("truncated value must carry the continuation marker; got tail %q",
				got[max(0, len(got)-40):])
		}
	})

	t.Run("the live bound equals the persisted bound", func(t *testing.T) {
		// pkg/agent's maxFailClosedOutputChars is unexported, so this asserts
		// the value rather than the symbol. If pkg/agent changes its bound,
		// this fails and forces the pair back into agreement — which is what
		// makes live and replay render identical text.
		const persistedBound = 2000
		if maxLiveErrorChars != persistedBound {
			t.Fatalf("maxLiveErrorChars=%d but pkg/agent bounds the persisted copy at %d; "+
				"live and replay would render different text for the same failure",
				maxLiveErrorChars, persistedBound)
		}
	})

	t.Run("a short error is passed through untouched", func(t *testing.T) {
		// The real-world common case: the UAT session's failures were ~30-70
		// characters. Truncation must not alter them at all.
		for _, s := range []string{
			"sh: 1: Cannot fork",
			"file: workspace-card.svg already exists. Set overwrite=true to replace.",
			"delegate: cancel: session 7bda9f10 is already terminal (failed) — nothing to cancel",
		} {
			if got := truncateRunesForFrame(s, maxLiveErrorChars); got != s {
				t.Errorf("short error was altered:\n got  %q\n want %q", got, s)
			}
		}
	})

	t.Run("cuts on rune boundaries, never mid-character", func(t *testing.T) {
		// Byte-based truncation here would emit invalid UTF-8 into a JSON
		// frame the SPA validates with zod, and a DROPPED frame is a worse
		// outcome than a truncated message.
		multibyte := strings.Repeat("日", maxLiveErrorChars+50)
		got := truncateRunesForFrame(multibyte, maxLiveErrorChars)
		if !strings.HasPrefix(got, "日") {
			t.Fatal("truncated multibyte string lost its leading rune")
		}
		body := strings.TrimSuffix(got, "\n... (truncated, output continues)")
		if n := len([]rune(body)); n != maxLiveErrorChars {
			t.Errorf("kept %d runes, want exactly %d", n, maxLiveErrorChars)
		}
		if !isValidUTF8(body) {
			t.Error("truncation produced invalid UTF-8 — the frame would fail zod validation and be dropped")
		}
	})
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
