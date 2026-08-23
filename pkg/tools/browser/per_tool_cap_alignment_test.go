package browser

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestChokePoint_PerSurfaceCap_B15_GetText covers the browser_get_text row
// of ADR-066 B-15 (FR-014): the tool's own cap is lowered from 100 KiB to
// the builtin-success cap (64,000 chars) with no per-tool opt-out.
func TestChokePoint_PerSurfaceCap_B15_GetText(t *testing.T) {
	if maxGetTextChars != 64_000 {
		t.Fatalf("maxGetTextChars = %d, want 64000", maxGetTextChars)
	}
	if maxGetTextChars != config.DefaultBuiltinSuccessCap {
		t.Fatalf("maxGetTextChars = %d, want DefaultBuiltinSuccessCap %d", maxGetTextChars, config.DefaultBuiltinSuccessCap)
	}

	at := strings.Repeat("a", 64_000)
	if got := capGetText(at); got != at {
		t.Fatalf("exactly 64000 chars must pass unmodified; len=%d", len(got))
	}

	over := strings.Repeat("b", 200_000)
	got := capGetText(over)
	if !strings.HasPrefix(got, over[:64_000]) || strings.HasPrefix(got, over[:64_001]) {
		t.Fatalf("must cut at exactly 64000 chars; len=%d", len(got))
	}
	if !strings.HasSuffix(got, getTextTruncationSuffix) || !strings.Contains(got, "64,000") {
		t.Fatalf("truncation suffix must name the 64,000-char cap; tail=%q", got[len(got)-60:])
	}
	if strings.Contains(got, "100KB") {
		t.Fatal("old 100KB suffix must be gone")
	}
}
