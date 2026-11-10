package tools

// web_fetch_fix_maxchars_test.go — RED pack for issue #1056's carried-
// forward #1025 finding: "maxChars below the minimum is silently ignored —
// maxChars=50 and maxChars=0 return the full body (length: 180,
// truncated: false) instead of being rejected."
//
// qa-lead decision (stated in the dispatch, not invented here): clamp to
// 100, with a visible `note` field in the result — not a rejection —
// matching this codebase's existing clamp-not-reject convention (ADR-096
// D12/D20's depth-ceiling clamp, pkg/tools/web_search.go::effectiveDepth /
// effectiveContentSize, which append a "Note: ... was clamped ..." line
// rather than refusing the call). fetch_url's JSON result shape has no
// free-text tail to append a note to (webFetchBuildResult's `map[string]any`
// carries fixed keys), so the equivalent is a new visible `note` key.
//
// Oracle for the two numbers this test pins: 100 is Parameters()'s own
// declared JSON Schema `"minimum": 100.0` for maxChars (pkg/tools/web.go::
// WebFetchTool.Parameters) — the tool already advertises this floor to the
// agent; webFetchParseArgs (pkg/tools/web_fetch.go) is simply not enforcing
// its own advertised minimum. This test does not invent the number 100; it
// reads it from the schema the tool already publishes.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// maxCharsClampCase is one below-minimum value this test proves gets
// clamped to the schema's own declared minimum (100), not silently ignored.
type maxCharsClampCase struct {
	name     string
	maxChars float64
}

func TestFixWebFetch_MaxCharsBelowMinimumIsClampedNotIgnored(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	// 500 chars, so a clamp to 100 is observably different from both the
	// requested value (50 or 0) and the unclamped full body (500).
	body := strings.Repeat("y", 500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	for _, tc := range []maxCharsClampCase{
		{name: "maxChars=50", maxChars: 50},
		{name: "maxChars=0", maxChars: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, err := NewWebFetchTool(50000, format, testFetchLimit)
			if err != nil {
				t.Fatalf("NewWebFetchTool: %v", err)
			}

			result := tool.Execute(context.Background(), map[string]any{
				"url":      server.URL,
				"maxChars": tc.maxChars,
			})
			if result.IsError {
				t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
			}

			var resultMap map[string]any
			if err := json.Unmarshal([]byte(result.ForLLM), &resultMap); err != nil {
				t.Fatalf("result is not valid JSON: %v\n%s", err, result.ForLLM)
			}

			truncated, _ := resultMap["truncated"].(bool)
			if !truncated {
				t.Fatalf("a 500-char body clamped to 100 must report truncated=true, got: %v", resultMap["truncated"])
			}

			text, _ := resultMap["text"].(string)
			// Strip the existing truncation-notice suffix (pkg/tools/web_fetch.go::
			// webFetchBuildResult) to isolate the clamp itself from that unrelated,
			// already-tested formatting (TestWebTool_WebFetch_Truncation pins the
			// exact suffix text).
			const noticeSuffix = "\n[Content truncated due to size limit]"
			if !strings.HasSuffix(text, noticeSuffix) {
				t.Fatalf("expected the truncation notice suffix, got text ending: %q", lastN(text, 60))
			}
			preNotice := strings.TrimSuffix(text, noticeSuffix)
			if len(preNotice) != 100 {
				t.Fatalf(
					"requested maxChars=%v is below the tool's own declared minimum (100, Parameters()'s "+
						"\"minimum\": 100.0) and must clamp the effective truncation length to exactly 100, got %d",
					tc.maxChars, len(preNotice))
			}

			note, ok := resultMap["note"].(string)
			if !ok || note == "" {
				t.Fatalf("expected a visible \"note\" field explaining the maxChars=%v request was below the "+
					"minimum and was clamped, got note=%v (full result: %s)", tc.maxChars, resultMap["note"], result.ForLLM)
			}
			lowerNote := strings.ToLower(note)
			if !strings.Contains(lowerNote, "100") {
				t.Fatalf("note must name the minimum (100) actually used, got: %q", note)
			}
			if !strings.Contains(lowerNote, "clamp") {
				t.Fatalf("note must say the value was clamped (this codebase's existing clamp-not-reject "+
					"vocabulary, e.g. pkg/tools/web_search.go's depth-ceiling notes), got: %q", note)
			}
		})
	}
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
