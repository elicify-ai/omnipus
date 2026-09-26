/**
 * billing_classification_test.go — provider-messages spec RED tests:
 * TDD rows 3 (routing side), 5 (cooldown pin + compile-level absence),
 * 26 (routing half: 404 → FailoverUnknown → chain falls back, D12).
 *
 * Traces: B-1, B-2, B-4; C-5, C-6, C-7; MR-4; dataset D2 (§12).
 *
 * Oracles come from the SPEC ONLY:
 *   - C-5: billing = HTTP 402, OR structured `insufficient_quota` code field,
 *     OR one of "insufficient credits" / "insufficient balance" /
 *     "credit balance too low" / "credit balance is too low" (Anthropic) /
 *     "payment required" — 4xx ONLY, never 5xx.
 *   - C-6: the D2 negative rows (Gemini per-minute 429, Codex usage window,
 *     OpenAI 429 linking a billing page, plain "too many requests") stay
 *     rate_limited — "billing", "usage limit reached" and "exceeded your
 *     current quota" are NOT in the vocabulary (CRIT-001).
 *   - MAJ-106: billingPatterns narrowed to equal C-5; billing markers checked
 *     before the rate-limit patterns; routing otherwise preserved.
 *   - D2/MAJ-109/C-24: 404 with a retirement signal and every other 404 route
 *     FailoverUnknown (D12's stated side effect: every 404 falls back).
 *   - C-7/D2: a billing verdict takes the STANDARD failure curve — never a
 *     billing-specific cooldown or lockout; calculateBillingCooldown is
 *     DELETED from the binary.
 *
 * Rows marked "characterization pin" encode today's mapping for rows the spec
 * leaves on the existing path ("routing behaviour is otherwise preserved",
 * §7.3) — they exist so GREEN cannot drift them, not to verify new behaviour.
 */

package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// d2PE builds a *common.ProviderError the way the HTTP boundary would, for a
// D2 row: status + body. Facts (retry-after) are not needed for classification.
func d2PE(status int, body string) *common.ProviderError {
	return &common.ProviderError{Status: status, Body: body}
}

func TestClassifyError_D2Routing(t *testing.T) {
	// Verbatim dataset D2 rows (§12); expected reasons per C-5/C-6/MAJ-106/D12.
	cases := []struct {
		name   string
		status int
		body   string
		want   FailoverReason
	}{
		// ── B-1 positives (C-5) ────────────────────────────────────────────
		{name: "D2 402 any body → billing", status: 402, body: `{"error":{"message":"insufficient credits"}}`, want: FailoverBilling},
		{name: "D2 429 structured insufficient_quota → billing (before the rate-limit short-circuit, MAJ-106)", status: 429, body: `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`, want: FailoverBilling},
		{name: "D2 429 prose insufficient credits → billing", status: 429, body: `{"error":{"message":"You have insufficient credits for this request"}}`, want: FailoverBilling},
		{name: "D2 400 Anthropic credit balance is too low → billing (MAJ-106)", status: 400, body: `{"type":"error","error":{"message":"Your credit balance is too low to access the Anthropic API"}}`, want: FailoverBilling},

		// ── B-2 negatives (C-6) ────────────────────────────────────────────
		{name: "D2 429 Gemini per-minute verbatim → rate_limit", status: 429, body: `{"error":{"message":"Resource has been exhausted… You exceeded your current quota, please check your plan and billing details…"}}`, want: FailoverRateLimit},
		{name: "D2 429 Codex usage window → rate_limit", status: 429, body: `{"error":{"message":"You've hit your usage limit… try again in 3 days"}}`, want: FailoverRateLimit},
		{name: "D2 429 OpenAI billing-page link → rate_limit", status: 429, body: `{"error":{"message":"You exceeded your current quota, please check your plan and billing details. Visit https://platform.openai.com/account/billing"}}`, want: FailoverRateLimit},
		{name: "D2 429 too many requests → rate_limit", status: 429, body: `{"error":{"message":"Too many requests"}}`, want: FailoverRateLimit},
		{name: "D2 429 empty body → rate_limit", status: 429, body: ``, want: FailoverRateLimit},
		{name: "C-5 narrowing: bare 'credit balance' without 'too low' is NOT billing (dropped prefix)", status: 429, body: `{"error":{"message":"Your credit balance can be viewed in the dashboard"}}`, want: FailoverRateLimit},
		{name: "C-5 narrowing: 'plans & billing' prose alone is NOT billing (dropped pattern)", status: 429, body: `{"error":{"message":"See plans & billing for details"}}`, want: FailoverRateLimit},

		// ── auth / 5xx (existing paths; see header note) ──────────────────
		// The D2 residual-400 rows (out-of-credit prose, SVG MIME) are pinned
		// USER-side only (Area 4): D2 names no ROUTING verdict for them, so a
		// routing assertion here would have no spec oracle. Report note:
		// ClassifyError cannot see ProviderError.Status today — its
		// extractHTTPStatus re-parses the rendered text, whose `status=NNN`
		// form matches no httpStatusPatterns entry — so HandleErrorResponse-
		// produced errors classify by body text alone. GREEN closes this for
		// the 402/429/5xx rows to be implementable as specified.
		{name: "D2 401 sk-proj key → auth", status: 401, body: `{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`, want: FailoverAuth},
		{name: "C-5 never on 5xx: 500 + payment required → timeout, NOT billing", status: 500, body: `{"error":{"message":"payment required"}}`, want: FailoverTimeout},

		// ── D12: every 404 routes FailoverUnknown (retriable) ──────────────
		{name: "D2 404 retirement phrase has been decommissioned → unknown (retriable, D12)", status: 404, body: `{"error":{"message":"This model has been decommissioned"}}`, want: FailoverUnknown},
		{name: "D2 404 retirement phrase no longer supported → unknown (retriable, D12)", status: 404, body: `{"error":{"message":"This model is no longer supported"}}`, want: FailoverUnknown},
		{name: "D2 404 retirement phrase deprecated and removed → unknown (retriable, D12)", status: 404, body: `{"error":{"message":"This model was deprecated and removed"}}`, want: FailoverUnknown},
		{name: "D2 404 model_not_found + phrase → unknown (retriable, D12)", status: 404, body: `{"error":{"code":"model_not_found","message":"This model has been decommissioned"}}`, want: FailoverUnknown},
		{name: "D12 side effect: 404 names the model but no retirement phrase → unknown (retriable)", status: 404, body: `{"error":{"message":"Model gpt-4o-2024-08-06 not found"}}`, want: FailoverUnknown},
		{name: "D12 side effect: 404 OpenAI access-denied verbatim → unknown (retriable)", status: 404, body: `{"error":{"message":"The model 'gpt-4o' does not exist or you do not have access to it"}}`, want: FailoverUnknown},
		{name: "D12 side effect: 404 Ollama not-pulled verbatim → unknown (retriable)", status: 404, body: `{"error":{"message":"model 'llama3' not found, try pulling it first"}}`, want: FailoverUnknown},
		{name: "D12 side effect: 404 media-404 echoing the model id → unknown (retriable)", status: 404, body: `{"error":{"message":"Model gpt-4o-2024-08-06 not found for image request"}}`, want: FailoverUnknown},

		// ── C-24 explicit non-match: non-404 never reads as retired; the 410
		//    row's routing verdict is DEFERRED by D2 ("revisit only when real
		//    410 wording is pinned"), so only the user side pins it (Area 4).
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(d2PE(tc.status, tc.body), "openai", "gpt-4o")
			if got == nil {
				t.Fatalf("ClassifyError returned nil for status %d, want %s", tc.status, tc.want)
			}
			if got.Reason != tc.want {
				t.Fatalf("ClassifyError(status=%d).Reason = %q, want %q (dataset D2)", tc.status, got.Reason, tc.want)
			}
		})
	}
}

// MR-4 mechanics: FailoverUnknown must be RETRIABLE so a retired primary
// falls back instead of aborting the chain (D12).
func TestIsRetriable_FailoverUnknown_RetriableForD12(t *testing.T) {
	fe := &FailoverError{Reason: FailoverUnknown}
	if !fe.IsRetriable() {
		t.Fatal("IsRetriable(FailoverUnknown) = false, want true — D12 needs every 404 to fall back to the configured Fallback model")
	}
}

// ── TDD row 5: the cooldown pin + compile-level absence (B-4, C-7, D2) ─────

func TestCooldown_BillingTakesStandardCurve_C7(t *testing.T) {
	ct := NewCooldownTracker()
	base := time.Now()
	ct.nowFunc = func() time.Time { return base }

	// First billing failure → the STANDARD first step (60 s), never the
	// deleted billing curve's 5-hour base (C-7/D2).
	ct.MarkFailure("openai", FailoverBilling)
	if got := ct.CooldownRemaining("openai"); got != calculateStandardCooldown(1) {
		t.Fatalf("cooldown after billing failure = %v, want the standard curve first step %v (C-7: no billing-specific cooldown)", got, calculateStandardCooldown(1))
	}

	// Second billing failure → the standard SECOND step (5 min), never a
	// billing escalation (the deleted curve jumped to hours).
	base = base.Add(time.Minute)
	ct.MarkFailure("openai", FailoverBilling)
	if got := ct.CooldownRemaining("openai"); got != calculateStandardCooldown(2) {
		t.Fatalf("cooldown after 2nd billing failure = %v, want the standard second step %v (no billing escalation, C-7)", got, calculateStandardCooldown(2))
	}

	// The spec's actual claim, stated as an equality: a billing verdict takes
	// the SAME curve as any other retriable failure. Same fresh tracker, same
	// injected clock, same failure count — rate_limit vs billing must cool
	// down identically. This is the assertion a surviving billing curve would
	// fail (calculateStandardCooldown(1) alone could not).
	eq := NewCooldownTracker()
	eqNow := base
	eq.nowFunc = func() time.Time { return eqNow }
	eq.MarkFailure("groq", FailoverRateLimit)
	eq.MarkFailure("z-ai", FailoverBilling)
	if eq.CooldownRemaining("groq") != eq.CooldownRemaining("z-ai") {
		t.Fatalf("billing cooldown %v != rate-limit cooldown %v at equal failure counts — a billing-specific curve still exists (C-7/D2)",
			eq.CooldownRemaining("z-ai"), eq.CooldownRemaining("groq"))
	}

	// No billing LOCKOUT of any length: the separate DisabledUntil path that
	// calculateBillingCooldown used to feed must stay zero for a billing
	// verdict (D2: no lockout of the model).
	ct.mu.Lock()
	entry := ct.entries["openai"]
	ct.mu.Unlock()
	if entry == nil {
		t.Fatal("no cooldown entry recorded for the billing failure")
	}
	if !entry.DisabledUntil.IsZero() {
		t.Fatalf("billing verdict set DisabledUntil = %v; a billing verdict must never lock the model out (D2/C-7)", entry.DisabledUntil)
	}
	if entry.DisabledReason == FailoverBilling {
		t.Fatalf("billing verdict recorded DisabledReason=billing; the billing lockout path is deleted (D2/C-7)")
	}
}

// Compile-level absence (row 5): the deleted billing-cooldown function must
// not exist anywhere in the package's non-test sources. A test cannot spell
// "absent symbol" in Go, so this scans the package source — the same
// compile-level-absence shape the spec asks for ("calculateBillingCooldown
// does not exist in the binary", B-4). Only non-test files are scanned: this
// test's own name mentions the symbol and must not trip itself.
func TestCooldown_BillingCurveFunctionIsDeleted_C7(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package sources: %v", err)
	}
	offenders := []string{}
	for _, f := range matches {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(src), "calculateBillingCooldown") {
			offenders = append(offenders, f)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("C-7/D2 violated: calculateBillingCooldown still exists in %v — it must be DELETED (delete-superseded-code ruling), the standard curve is the only curve", offenders)
	}
}
