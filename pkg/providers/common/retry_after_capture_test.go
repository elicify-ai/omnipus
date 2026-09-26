/**
 * retry_after_capture_test.go — provider-messages spec RED tests, TDD rows 1–2
 * (spec §11; dataset D1 §12; C-9, C-22, C-20; MIN-101; OBS-003).
 *
 * Row 1 — header parse: retry-after seconds / future HTTP-date / past
 *         HTTP-date / malformed / ms / duplicate / absent / 0 / 3600.
 * Row 2 — request-id header candidates; cf-ray labelled last-resort.
 *
 * Oracles come from the SPEC ONLY (§7.2 + §12 D1), never from the code:
 *   - ProviderError gains RetryAfterSeconds (a plain int — no presence flag,
 *     no pointer: 0 means "fact absent, take the backoff schedule") and
 *     RequestID, parsed at HandleErrorResponse (C-9/MIN-101).
 *   - retry-after parses integer seconds or HTTP-date; a past date or a
 *     malformed value → 0; duplicates → FIRST value; retry-after-ms rounds
 *     up and is ≥ 1 when present-and-nonzero; absent → 0.
 *   - retry-after: 0 is treated exactly like a missing header (MIN-101).
 *   - over-ceiling values (3600) are still CAPTURED as a fact (the ≤120 s
 *     auto-retry ceiling is a downstream decision, not a capture decision).
 *   - ONLY retry-after and retry-after-ms are parsed — no x-ratelimit-reset*
 *     parsing exists (C-22 / MAJ-008).
 *   - request-id candidates in priority order: x-request-id, request-id,
 *     x-amzn-requestid, cf-ray (last resort — OBS-003).
 *
 * This file is COMPILE-RED today: ProviderError has neither field yet. GREEN
 * (Wave 3) adds them and this file compiles; every assertion then must pass.
 *
 * Note on WrapHTMLResponseError: its signature takes no headers, so the HTML
 * path is driven through its only caller that has them — HandleErrorResponse —
 * which §7.2 also lists as a parse site. GREEN may keep that shape.
 */

package common

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mkErrResp builds the *http.Response HandleErrorResponse consumes.
func mkErrResp(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// providerErrorFrom unwraps the *ProviderError HandleErrorResponse returns.
func providerErrorFrom(t *testing.T, err error) *ProviderError {
	t.Helper()
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("HandleErrorResponse returned %T, want *ProviderError: %v", err, err)
	}
	return pe
}

func TestRetryAfterCapture_D1(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   int
		// bounded is set only for the future-HTTP-date row: the expected
		// value is "delta seconds at parse time" (D1), a genuine wall-clock
		// tolerance, so want is a midpoint and the assertion is bounded.
		bounded   bool
		tolerance int
	}{
		{
			name:   "D1 seconds at ceiling: retry-after 120 parses to 120",
			header: http.Header{"Retry-After": []string{"120"}},
			want:   120,
		},
		{
			name:   "D1 seconds small: retry-after 3 parses to 3",
			header: http.Header{"Retry-After": []string{"3"}},
			want:   3,
		},
		{
			name:   "D1 over-ceiling: 3600 is still captured as a fact (no auto-retry is a downstream decision)",
			header: http.Header{"Retry-After": []string{"3600"}},
			want:   3600,
		},
		{
			name:   "D1 zero: retry-after 0 parses to 0 — indistinguishable from absent (MIN-101)",
			header: http.Header{"Retry-After": []string{"0"}},
			want:   0,
		},
		{
			name:   "D1 absent: no header means fact absent (0)",
			header: http.Header{},
			want:   0,
		},
		{
			name:   "D1 malformed: 'soon' parses to 0 (backoff schedule)",
			header: http.Header{"Retry-After": []string{"soon"}},
			want:   0,
		},
		{
			name:   "D1 http-date past: fact absent (no negative countdown)",
			header: http.Header{"Retry-After": []string{"Wed, 21 Oct 2015 07:28:00 GMT"}},
			want:   0,
		},
		{
			name:      "D1 http-date future: delta seconds at parse time (~90s)",
			header:    nil, // built below — needs the current time
			want:      90,
			bounded:   true,
			tolerance: 2,
		},
		{
			name:   "D1 ms variant: retry-after-ms 1500 → 2 (rounded up, >= 1)",
			header: http.Header{"Retry-After-Ms": []string{"1500"}},
			want:   2,
		},
		{
			name:   "D1 ms rounding floor: 999ms → 1 (>= 1)",
			header: http.Header{"Retry-After-Ms": []string{"999"}},
			want:   1,
		},
		{
			name:   "D1 ms rounding floor: 500ms → 1 (>= 1)",
			header: http.Header{"Retry-After-Ms": []string{"500"}},
			want:   1,
		},
		{
			name:   "D1 ms exact: 2000ms → 2",
			header: http.Header{"Retry-After-Ms": []string{"2000"}},
			want:   2,
		},
		{
			name:   "D1 ms zero: 0ms is fact absent (0, not 1)",
			header: http.Header{"Retry-After-Ms": []string{"0"}},
			want:   0,
		},
		{
			name:   "D1 duplicate retry-after: FIRST value wins",
			header: http.Header{"Retry-After": []string{"7", "99"}},
			want:   7,
		},
		{
			name:   "D1 ms loses to explicit seconds header? No — ms is a separate header; seconds wins when both present",
			header: http.Header{"Retry-After": []string{"9"}, "Retry-After-Ms": []string{"1500"}},
			want:   9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := tc.header
			if tc.bounded {
				// Build the future HTTP-date at test runtime so the delta is
				// ~want seconds regardless of when the suite runs.
				header = http.Header{"Retry-After": []string{
					time.Now().UTC().Add(time.Duration(tc.want) * time.Second).UTC().Format(http.TimeFormat),
				}}
			}
			pe := providerErrorFrom(t, HandleErrorResponse(mkErrResp(429, header, `{"error":{"message":"rate limited"}}`), "https://api.example.com"))

			// MIN-101 shape pin: RetryAfterSeconds is a plain int — no
			// presence flag, no pointer. Compiles only for an int-assignable
			// field; 0 == absent is the semantic this enables.
			var _ int = pe.RetryAfterSeconds

			if tc.bounded {
				low, high := tc.want-tc.tolerance, tc.want+tc.tolerance
				if pe.RetryAfterSeconds < low || pe.RetryAfterSeconds > high {
					t.Fatalf("RetryAfterSeconds = %d, want %d±%d (D1 http-date future: delta at parse time)",
						pe.RetryAfterSeconds, tc.want, tc.tolerance)
				}
				return
			}
			if pe.RetryAfterSeconds != tc.want {
				t.Fatalf("RetryAfterSeconds = %d, want %d (case %q, dataset D1)", pe.RetryAfterSeconds, tc.want, tc.name)
			}
		})
	}
}

// C-22 negative: the x-ratelimit-reset* family is OUT of parsing scope —
// a value that looks like seconds must NOT leak into RetryAfterSeconds
// (not parsing it yields the backoff schedule, which is always safe; MAJ-008).
func TestRetryAfterCapture_NoResetHeaderParsing_C22(t *testing.T) {
	header := http.Header{
		"X-Ratelimit-Reset":            []string{"12345"},
		"X-Ratelimit-Reset-Requests":   []string{"678"},
		"X-Ratelimit-Remaining-Tokens": []string{"999"},
	}
	pe := providerErrorFrom(t, HandleErrorResponse(mkErrResp(429, header, `{"error":{"message":"too many requests"}}`), "https://api.example.com"))
	if pe.RetryAfterSeconds != 0 {
		t.Fatalf("C-22 violated: x-ratelimit-reset* parsed into RetryAfterSeconds = %d, want 0 (backoff schedule)", pe.RetryAfterSeconds)
	}
}

// The HTML path (WrapHTMLResponseError) is also a §7.2 parse site. Its direct
// signature takes no headers, so the HTML error is driven through
// HandleErrorResponse — the caller that has them and that §7.2 names.
func TestRetryAfterCapture_HTMLPath_ParsesHeaders(t *testing.T) {
	header := http.Header{
		"Content-Type": []string{"text/html"},
		"Retry-After":  []string{"30"},
		"X-Request-Id": []string{"req-html-1"},
	}
	resp := &http.Response{
		StatusCode: 503,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("<html><body>oops</body></html>")),
	}
	pe := providerErrorFrom(t, HandleErrorResponse(resp, "https://api.example.com"))
	if pe.RetryAfterSeconds != 30 {
		t.Fatalf("HTML-path RetryAfterSeconds = %d, want 30 (retry-after parsed on the WrapHTMLResponseError path, §7.2)", pe.RetryAfterSeconds)
	}
	if pe.RequestID != "req-html-1" {
		t.Fatalf("HTML-path RequestID = %q, want \"req-html-1\"", pe.RequestID)
	}
}

func TestRequestIDCapture_Candidates_OB003(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name:   "x-request-id wins",
			header: http.Header{"X-Request-Id": []string{"req-a"}},
			want:   "req-a",
		},
		{
			name:   "request-id is a candidate",
			header: http.Header{"Request-Id": []string{"req-b"}},
			want:   "req-b",
		},
		{
			name:   "x-amzn-requestid is a candidate",
			header: http.Header{"X-Amzn-Requestid": []string{"req-c"}},
			want:   "req-c",
		},
		{
			name:   "cf-ray is the last-resort candidate (OBS-003: Cloudflare edge id, not a provider request id)",
			header: http.Header{"Cf-Ray": []string{"edge-d"}},
			want:   "edge-d",
		},
		{
			name:   "priority: x-request-id beats the others",
			header: http.Header{"X-Request-Id": []string{"first"}, "Request-Id": []string{"second"}, "Cf-Ray": []string{"edge"}},
			want:   "first",
		},
		{
			name:   "priority: request-id beats x-amzn-requestid and cf-ray",
			header: http.Header{"Request-Id": []string{"second"}, "X-Amzn-Requestid": []string{"third"}, "Cf-Ray": []string{"edge"}},
			want:   "second",
		},
		{
			name:   "priority: x-amzn-requestid beats cf-ray",
			header: http.Header{"X-Amzn-Requestid": []string{"third"}, "Cf-Ray": []string{"edge"}},
			want:   "third",
		},
		{
			name:   "absent: no candidates means empty fact",
			header: http.Header{},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := providerErrorFrom(t, HandleErrorResponse(mkErrResp(401, tc.header, `{"error":{"message":"bad key"}}`), "https://api.example.com"))
			if pe.RequestID != tc.want {
				t.Fatalf("RequestID = %q, want %q (request-id candidates, OBS-003)", pe.RequestID, tc.want)
			}
		})
	}
}
