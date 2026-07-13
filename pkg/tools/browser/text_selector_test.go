// Unit tests for the text-selector primitives — parseTextPseudo,
// buildTextSelectorScript, and tokenFromMarkerSelector. These exercise the
// parsing/rendering logic without a live browser (see
// text_selector_e2e_test.go for the real-Chromium resolution tests).
//
// Root cause under test: agents write Playwright-style selectors like
// button:has-text("Confirm") that chromedp.ByQuery (standard CSS
// querySelector) rejects outright. parseTextPseudo must recognize that shape
// and split it into a CSS scope + text needle + exactness so
// resolveTextTarget can match by visible text instead.
package browser

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// parseTextPseudo
// ---------------------------------------------------------------------------

func TestParseTextPseudo(t *testing.T) {
	tests := []struct {
		name          string
		selector      string
		wantCSSPrefix string
		wantNeedle    string
		wantExact     bool
		wantIsText    bool
		wantErr       bool
	}{
		{
			name:          "has-text double quotes",
			selector:      `button:has-text("Book")`,
			wantCSSPrefix: "button",
			wantNeedle:    "Book",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:          "text-is double quotes — exact",
			selector:      `a:text-is("14")`,
			wantCSSPrefix: "a",
			wantNeedle:    "14",
			wantExact:     true,
			wantIsText:    true,
		},
		{
			name:          "text substring (no -is)",
			selector:      `td:text("14")`,
			wantCSSPrefix: "td",
			wantNeedle:    "14",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:          "empty prefix defaults to wildcard",
			selector:      `:has-text("x")`,
			wantCSSPrefix: "*",
			wantNeedle:    "x",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:          "single quotes",
			selector:      `a:has-text('Book a call')`,
			wantCSSPrefix: "a",
			wantNeedle:    "Book a call",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:          "single quotes exact",
			selector:      `a:text-is('14')`,
			wantCSSPrefix: "a",
			wantNeedle:    "14",
			wantExact:     true,
			wantIsText:    true,
		},
		{
			name:       "plain CSS — no pseudo — unchanged",
			selector:   `button.btn-primary`,
			wantIsText: false,
		},
		{
			name:       "plain CSS with id",
			selector:   `#submit-button`,
			wantIsText: false,
		},
		{
			name:       "plain CSS with unrelated pseudo-class",
			selector:   `li:nth-child(2)`,
			wantIsText: false,
		},
		{
			name:          "text with spaces and punctuation",
			selector:      `a:has-text("Pick a 30-min slot!")`,
			wantCSSPrefix: "a",
			wantNeedle:    "Pick a 30-min slot!",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:          "trailing whitespace after pseudo",
			selector:      `button:has-text("Confirm")   `,
			wantCSSPrefix: "button",
			wantNeedle:    "Confirm",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:          "compound css prefix preserved",
			selector:      `div.modal button.btn-primary:has-text("Confirm")`,
			wantCSSPrefix: `div.modal button.btn-primary`,
			wantNeedle:    "Confirm",
			wantExact:     false,
			wantIsText:    true,
		},
		{
			name:       "empty selector",
			selector:   "",
			wantIsText: false,
		},
		{
			name:          "text-is with empty text",
			selector:      `a:text-is("")`,
			wantCSSPrefix: "a",
			wantNeedle:    "",
			wantExact:     true,
			wantIsText:    true,
		},
		// --- 7-reviewer finding #8: chained text pseudos ---
		{
			name:     "chained pseudos — has-text then text-is — rejected",
			selector: `div:has-text("a"):text-is("b")`,
			wantErr:  true,
		},
		{
			name:     "chained pseudos — two has-text — rejected",
			selector: `div:has-text("a"):has-text("b")`,
			wantErr:  true,
		},
		{
			name:     "chained pseudos — mixed quote styles — rejected",
			selector: `div:has-text("a"):text-is('b')`,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cssPrefix, needle, exact, isText, err := parseTextPseudo(tc.selector)
			if tc.wantErr {
				require.Error(t, err, "expected chained-pseudo error for %q", tc.selector)
				assert.Contains(t, err.Error(), "chained text pseudos are not supported")
				assert.False(t, isText)
				assert.Equal(t, "", cssPrefix)
				assert.Equal(t, "", needle)
				assert.False(t, exact)
				return
			}
			require.NoError(t, err, "unexpected error for %q", tc.selector)
			assert.Equal(t, tc.wantIsText, isText, "isText mismatch for %q", tc.selector)
			if !tc.wantIsText {
				return
			}
			assert.Equal(t, tc.wantCSSPrefix, cssPrefix, "cssPrefix mismatch for %q", tc.selector)
			assert.Equal(t, tc.wantNeedle, needle, "needle mismatch for %q", tc.selector)
			assert.Equal(t, tc.wantExact, exact, "exact mismatch for %q", tc.selector)
		})
	}
}

// ---------------------------------------------------------------------------
// buildTextSelectorScript — injection safety
// ---------------------------------------------------------------------------

// TestBuildTextSelectorScript_InjectionSafety proves that a needle containing
// quote characters, a </script> tag, or JS-breakout syntax is embedded as a
// json.Marshal'd STRING LITERAL, never raw-concatenated into the generated
// script. This is the CRITICAL injection-safety requirement: page content or
// model-supplied text must never be able to terminate the JS string early or
// inject executable code.
func TestBuildTextSelectorScript_InjectionSafety(t *testing.T) {
	dangerousNeedles := []string{
		`"; alert(1); //`,
		`</script><script>alert(1)</script>`,
		`");alert(document.cookie);(""`,
		`say "hi" then leave`,
		"line one\nline two",
	}

	for _, needle := range dangerousNeedles {
		t.Run(needle, func(t *testing.T) {
			script, err := buildTextSelectorScript("*", needle, false, "tok-1")
			require.NoError(t, err)

			// The exact json.Marshal'd form of the needle must appear verbatim —
			// proving it went through the safe encoding path.
			wantJSON, err := json.Marshal(needle)
			require.NoError(t, err)
			assert.Contains(t, script, string(wantJSON),
				"generated script must contain the json.Marshal'd needle literal")

			// The RAW needle must never appear as an unescaped bare substring
			// outside of its JSON-encoded form — i.e. the only occurrence of the
			// dangerous payload's distinguishing content is the safely-escaped one.
			// We check this by removing the safely-encoded occurrence and
			// confirming the raw payload no longer appears.
			withEncodedRemoved := strings.Replace(script, string(wantJSON), "", 1)
			assert.NotContains(t, withEncodedRemoved, needle,
				"raw needle must not appear anywhere outside its JSON-encoded form (injection risk)")
		})
	}
}

// TestBuildTextSelectorScript_ScopeAndTokenAlsoEncoded proves cssScope and
// token are ALSO json.Marshal'd, not just needle — a scope or token
// containing a quote must not break out of its literal either.
func TestBuildTextSelectorScript_ScopeAndTokenAlsoEncoded(t *testing.T) {
	scope := `div[data-x="y"]`
	token := `tok-"quote"-injected`

	script, err := buildTextSelectorScript(scope, "Confirm", false, token)
	require.NoError(t, err)

	wantScopeJSON, err := json.Marshal(scope)
	require.NoError(t, err)
	assert.Contains(t, script, string(wantScopeJSON))

	wantTokenJSON, err := json.Marshal(token)
	require.NoError(t, err)
	assert.Contains(t, script, string(wantTokenJSON))
}

// TestBuildTextSelectorScript_ExactFlagEncoded confirms the exact boolean is
// rendered as a real JS boolean literal (true/false), not a string.
func TestBuildTextSelectorScript_ExactFlagEncoded(t *testing.T) {
	scriptExact, err := buildTextSelectorScript("*", "14", true, "tok")
	require.NoError(t, err)
	assert.Contains(t, scriptExact, "var exact = true;")

	scriptSubstring, err := buildTextSelectorScript("*", "14", false, "tok")
	require.NoError(t, err)
	assert.Contains(t, scriptSubstring, "var exact = false;")
}

// TestBuildTextSelectorScript_ReturnsValidStructure sanity-checks the
// rendered script has the expected shape (IIFE, marker attribute set, marker
// selector returned) — a coarse structural check, not a browser-driven one.
func TestBuildTextSelectorScript_ReturnsValidStructure(t *testing.T) {
	script, err := buildTextSelectorScript("button", "Confirm", false, "tok-123")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(script), "(function(){"))
	assert.True(t, strings.HasSuffix(strings.TrimSpace(script), "})()"))
	assert.Contains(t, script, "setAttribute(attr, token)")
	assert.Contains(t, script, "getClientRects()")
}

// ---------------------------------------------------------------------------
// tokenFromMarkerSelector
// ---------------------------------------------------------------------------

func TestTokenFromMarkerSelector(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		want   string
	}{
		{"well-formed marker", `[data-omnipus-tsel="omnipus-tsel-123-1"]`, "omnipus-tsel-123-1"},
		{"empty string", "", ""},
		{"missing brackets", `data-omnipus-tsel="x"`, ""},
		{"wrong attribute name", `[data-other="x"]`, ""},
		{"plain css selector", "#submit", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tokenFromMarkerSelector(tc.marker))
		})
	}
}

// TestTokenFromMarkerSelector_RoundTripsWithBuiltMarker proves the token
// extracted from a marker produced by the ACTUAL JS-template format
// (mirrored here in Go) round-trips correctly — guards against the Go-side
// prefix/suffix constants drifting from the JS template's literal format.
func TestTokenFromMarkerSelector_RoundTripsWithBuiltMarker(t *testing.T) {
	token := nextTextSelectorToken()
	marker := `[` + textMarkerAttr + `="` + token + `"]`
	assert.Equal(t, token, tokenFromMarkerSelector(marker))
}

// ---------------------------------------------------------------------------
// nextTextSelectorToken
// ---------------------------------------------------------------------------

func TestNextTextSelectorToken_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		tok := nextTextSelectorToken()
		require.False(t, seen[tok], "token collision at iteration %d: %s", i, tok)
		seen[tok] = true
	}
}

// ---------------------------------------------------------------------------
// resolveTextTarget — no-browser guard (empty needle)
// ---------------------------------------------------------------------------

// TestResolveTextTarget_EmptyNeedle_NoChromiumNeeded proves the empty-needle
// guard fires before any chromedp/CDP call — needs no browser.
func TestResolveTextTarget_EmptyNeedle_NoChromiumNeeded(t *testing.T) {
	_, err := resolveTextTarget(nil, "*", "   ", false, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty text to match")
}

// ---------------------------------------------------------------------------
// resolveTextTarget — no-browser guard (oversized needle/scope, finding #10)
// ---------------------------------------------------------------------------

// TestResolveTextTarget_OversizedNeedle_NoChromiumNeeded proves the
// maxTextSelectorInputLen cap on `needle` fires before any chromedp/CDP
// call — needs no browser (7-reviewer finding #10, security-lead minor: cap
// needle/scope length before embedding into the JS).
func TestResolveTextTarget_OversizedNeedle_NoChromiumNeeded(t *testing.T) {
	oversizedNeedle := strings.Repeat("a", maxTextSelectorInputLen+1)
	_, err := resolveTextTarget(nil, "*", oversizedNeedle, false, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text argument exceeds")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d-byte limit", maxTextSelectorInputLen))
}

// TestResolveTextTarget_OversizedScope_NoChromiumNeeded proves the identical
// cap on `cssScope`.
func TestResolveTextTarget_OversizedScope_NoChromiumNeeded(t *testing.T) {
	oversizedScope := strings.Repeat("div ", (maxTextSelectorInputLen/4)+1)
	_, err := resolveTextTarget(nil, oversizedScope, "Confirm", false, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selector scope exceeds")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d-byte limit", maxTextSelectorInputLen))
}

// TestBuildTextSelectorScript_AtLimitNeedle_EncodesFine proves the cap is a
// strict "exceeds", not "at or exceeds": an exactly-maxTextSelectorInputLen
// needle still encodes correctly through buildTextSelectorScript (the pure
// rendering step, no chromedp/CDP involved) — resolveTextTarget's guard
// itself is a simple `len(needle) > maxTextSelectorInputLen`, so this proves
// there's no off-by-one truncation anywhere downstream of a boundary-sized
// input.
func TestBuildTextSelectorScript_AtLimitNeedle_EncodesFine(t *testing.T) {
	atLimitNeedle := strings.Repeat("a", maxTextSelectorInputLen)
	script, err := buildTextSelectorScript("*", atLimitNeedle, false, "tok")
	require.NoError(t, err)
	wantJSON, err := json.Marshal(atLimitNeedle)
	require.NoError(t, err)
	assert.Contains(t, script, string(wantJSON))
}
