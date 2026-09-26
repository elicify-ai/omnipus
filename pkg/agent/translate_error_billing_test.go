/**
 * translate_error_billing_test.go — provider-messages spec RED tests:
 * TDD rows 3 (user side), 4, 21, 22, 28, 31, 32.
 *
 * Traces: B-1, B-2, B-3; C-5, C-6, C-19, C-23, C-24; MAJ-103, MAJ-106,
 * MAJ-109; MR-1, MR-2, MR-3; datasets D2, D3, D11, D15; SC-3.
 *
 * Oracles come from the SPEC ONLY:
 *   - C-5 (both classifiers): 402, structured insufficient_quota, or a C-5
 *     phrase — 4xx only, never 5xx. Row 28 (SC-3/B-1) asserts the user-side
 * placement and the routing reason AGREE on every D2 row.
 *   - §6 templates (D3): quota_billing "{provider} says your account is out
 *     of credit."; model_retired "This model is no longer offered by
 *     {provider}. Pick a new model in the agent's settings."; auth
 *     "{provider} rejected the API key. Check the key in Settings → Providers."
 *     {provider} is the FAILING ATTEMPT's provider name (C-16).
 *   - C-24/MAJ-109 (MR-1/MR-2/MR-3): model_retired fires on 404 + explicit
 *     retirement phrase, or model_not_found + phrase; never on near-misses
 *     (generic 404, OpenAI access-denied, Ollama not-pulled, media-404, 410).
 *   - MAJ-103: the copy rules (a config message never advises retry) run over
 *     the provider_message variants too.
 *   - C-19 (AU-3): key-shaped material never reaches message or facts.
 *   - C-23 (RG-1): context_too_long is byte-identical to catalogue.
 *
 * COMPILE-RED: CodeQuotaBilling and CodeModelRetired do not exist yet — the
 * spec's §7 names them, so the tests spell them so GREEN has an unambiguous
 * target. Everything else referenced here exists today.
 */

package agent

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// ── Fixtures: one fabricated HTTP boundary, both classifiers ───────────────

// boundaryPair builds ONE fabricated provider response through the real
// HandleErrorResponse and hands it to both classifiers in the shapes they
// consume: the routing classifier takes the common.ProviderError directly;
// the user-side classifier takes the pkg/agent wire-shape seam
// (ProviderError{Status, Body, Err}) the agent loop populates at the same
// boundary. translate_error.go::ProviderErrorFromFailover unwraps to it.
func boundaryPair(t *testing.T, status int, header http.Header, body string) (*ProviderError, error) {
	t.Helper()
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	resp := &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	err := common.HandleErrorResponse(resp, "https://api.example.com")
	commonPE, ok := err.(*common.ProviderError)
	if !ok {
		t.Fatalf("HandleErrorResponse returned %T (%v), want *common.ProviderError", err, err)
	}
	return &ProviderError{Status: commonPE.Status, Body: commonPE.Body, Err: commonPE}, err
}

// templateWith substitutes {provider} in a §6 template with the failing
// attempt's provider name (the spec's slot semantics; names come from the
// provider catalogue's display names, providers.DisplayName).
func templateWith(code string, providerID string) string {
	return strings.ReplaceAll(generated.LLMErrorProviderMessages[code], "{provider}", providers.DisplayName(providerID))
}

// ── Row 28 (SC-3 / B-1): the two classifiers agree on every D2 row ─────────

func TestClassifierAgreement_D2(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantUser    LLMErrorCode
		wantRouting providers.FailoverReason
	}{
		// ── B-1 positives: billing on BOTH sides ──────────────────────────
		{name: "402 any body", status: 402, body: `{"error":{"message":"neutral gateway text"}}`, wantUser: CodeQuotaBilling, wantRouting: providers.FailoverBilling},
		{name: "429 structured insufficient_quota", status: 429, body: `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`, wantUser: CodeQuotaBilling, wantRouting: providers.FailoverBilling},
		{name: "429 prose insufficient credits", status: 429, body: `{"error":{"message":"You have insufficient credits for this request"}}`, wantUser: CodeQuotaBilling, wantRouting: providers.FailoverBilling},
		{name: "400 Anthropic credit balance is too low", status: 400, body: `{"type":"error","error":{"message":"Your credit balance is too low to access the Anthropic API"}}`, wantUser: CodeQuotaBilling, wantRouting: providers.FailoverBilling},

		// ── B-2 negatives: rate-limit look-alikes stay rate_limited on BOTH ─
		{name: "429 Gemini per-minute verbatim", status: 429, body: `{"error":{"message":"Resource has been exhausted… You exceeded your current quota, please check your plan and billing details…"}}`, wantUser: CodeRateLimited, wantRouting: providers.FailoverRateLimit},
		{name: "429 Codex usage window", status: 429, body: `{"error":{"message":"You've hit your usage limit… try again in 3 days"}}`, wantUser: CodeRateLimited, wantRouting: providers.FailoverRateLimit},
		{name: "429 OpenAI billing-page link", status: 429, body: `{"error":{"message":"You exceeded your current quota, please check your plan and billing details. Visit https://platform.openai.com/account/billing"}}`, wantUser: CodeRateLimited, wantRouting: providers.FailoverRateLimit},
		{name: "429 too many requests", status: 429, body: `{"error":{"message":"Too many requests"}}`, wantUser: CodeRateLimited, wantRouting: providers.FailoverRateLimit},
		{name: "429 empty body", status: 429, body: ``, wantUser: CodeRateLimited, wantRouting: providers.FailoverRateLimit},
		{name: "401 sk-proj key", status: 401, body: `{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`, wantUser: CodeProviderAuthFailed, wantRouting: providers.FailoverAuth},

		// ── MR rows: retirement vs near-miss, agreed on both sides ────────
		{name: "404 decommissioned", status: 404, body: `{"error":{"message":"This model has been decommissioned"}}`, wantUser: CodeModelRetired, wantRouting: providers.FailoverUnknown},
		{name: "404 no longer supported", status: 404, body: `{"error":{"message":"This model is no longer supported"}}`, wantUser: CodeModelRetired, wantRouting: providers.FailoverUnknown},
		{name: "404 model_not_found + phrase", status: 404, body: `{"error":{"code":"model_not_found","message":"This model has been decommissioned"}}`, wantUser: CodeModelRetired, wantRouting: providers.FailoverUnknown},
		{name: "404 generic near-miss names the model", status: 404, body: `{"error":{"message":"Model gpt-4o-2024-08-06 not found"}}`, wantUser: CodeUnknown, wantRouting: providers.FailoverUnknown},
		{name: "404 OpenAI access-denied near-miss", status: 404, body: `{"error":{"message":"The model 'gpt-4o' does not exist or you do not have access to it"}}`, wantUser: CodeUnknown, wantRouting: providers.FailoverUnknown},
		{name: "404 Ollama not-pulled near-miss", status: 404, body: `{"error":{"message":"model 'llama3' not found, try pulling it first"}}`, wantUser: CodeUnknown, wantRouting: providers.FailoverUnknown},
		{name: "404 media-404 echoing the model id", status: 404, body: `{"error":{"message":"Model gpt-4o-2024-08-06 not found for image request"}}`, wantUser: CodeUnknown, wantRouting: providers.FailoverUnknown},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			agentPE, commonErr := boundaryPair(t, tc.status, nil, tc.body)
			gotUser := classifyByHTTPStatus(agentPE)
			if gotUser != tc.wantUser {
				t.Fatalf("user-side classifyByHTTPStatus = %q, want %q (dataset D2, SC-3)", gotUser, tc.wantUser)
			}
			fe := providers.ClassifyError(commonErr, "openrouter", "model-a")
			if fe == nil {
				t.Fatalf("routing ClassifyError returned nil, want %s (dataset D2, SC-3: the routing classifier must never abort on a 404)", tc.wantRouting)
			}
			if fe.Reason != tc.wantRouting {
				t.Fatalf("routing ClassifyError.Reason = %q, want %q (dataset D2, SC-3)", fe.Reason, tc.wantRouting)
			}
			// MAJ-106: the ROUTING verdict gates retry — a billing verdict
			// must be retriable so the chain falls back. The user-side
			// isRetryable gates only the copy choice, never retry.
			if tc.wantRouting == providers.FailoverBilling && !fe.IsRetriable() {
				t.Fatalf("routing billing verdict is not retriable — billing would never reach the fallback (MAJ-106)")
			}
		})
	}
}

// ── Rows 3 (user side) + 4 (B-3): quota_billing verdict, retryable, copy ───

func TestQuotaBilling_UserSide_C5(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "402 any body", status: 402, body: `{"error":{"message":"neutral gateway text"}}`},
		{name: "429 insufficient_quota", status: 429, body: `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`},
		{name: "429 insufficient credits", status: 429, body: `{"error":{"message":"You have insufficient credits for this request"}}`},
		{name: "400 Anthropic credit balance is too low", status: 400, body: `{"type":"error","error":{"message":"Your credit balance is too low to access the Anthropic API"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, commonErr := boundaryPair(t, tc.status, nil, tc.body)
			llm := TranslateTurnError(commonErr)
			if llm.Code != CodeQuotaBilling {
				t.Fatalf("code = %q, want %q (C-5, user side)", llm.Code, CodeQuotaBilling)
			}
			if llm.Retryable {
				t.Fatalf("retryable = true, want false (B-1: no auto-retry for billing; isRetryable must return false)")
			}
			if got := AttributionForCode(CodeQuotaBilling); got != generated.LLMErrorAttributionConfig {
				t.Fatalf("attribution = %q, want %q (D15, final)", got, generated.LLMErrorAttributionConfig)
			}
			// D3 template with the failing attempt's provider. These fixture
			// errors carry no FailoverError wrapper, so facts are absent and
			// the CATALOGUE sentence renders — asserted in the identity-absent
			// test below; here the template sentence is asserted via the
			// identity-present path covered by TestQuotaBilling_Templated.
			want := templateWith("quota_billing", "openrouter")
			if want == "" || want == generated.LLMErrorUserMessages["quota_billing"] {
				t.Fatalf("template oracle degenerate: %q", want)
			}
		})
	}
}

// D3: facts present → the §6 sentence names the provider. (Identity arrives
// via the chain's FailoverError wrapper — C-16.)
func TestQuotaBilling_Templated_Sentence_C16(t *testing.T) {
	_, commonErr := boundaryPair(t, 402, nil, `{"error":{"message":"neutral"}}`)
	fe := &providers.FailoverError{
		Reason:   providers.FailoverBilling,
		Provider: "openrouter",
		Model:    "model-a",
		Status:   402,
		Wrapped:  commonErr,
	}
	llm := TranslateTurnError(fe)
	if llm.Code != CodeQuotaBilling {
		t.Fatalf("code = %q, want %q", llm.Code, CodeQuotaBilling)
	}
	want := templateWith("quota_billing", "openrouter")
	if llm.Message != want {
		t.Fatalf("message = %q, want the D3 template with the failing attempt's provider: %q", llm.Message, want)
	}
}

// ── Row 21 (MR-1): C-24 true positives → model_retired ─────────────────────

func TestModelRetired_TruePositives_C24(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "404 decommissioned", status: 404, body: `{"error":{"message":"This model has been decommissioned"}}`},
		{name: "404 no longer supported", status: 404, body: `{"error":{"message":"This model is no longer supported"}}`},
		{name: "404 deprecated and removed", status: 404, body: `{"error":{"message":"This model was deprecated and removed"}}`},
		{name: "404 model_not_found + phrase", status: 404, body: `{"error":{"code":"model_not_found","message":"This model has been decommissioned"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, commonErr := boundaryPair(t, tc.status, nil, tc.body)
			llm := TranslateTurnError(commonErr)
			if llm.Code != CodeModelRetired {
				t.Fatalf("code = %q, want %q (C-24 trigger)", llm.Code, CodeModelRetired)
			}
			if llm.Retryable {
				t.Fatal("retryable = true, want false (MR-1)")
			}
			if got := AttributionForCode(CodeModelRetired); got != generated.LLMErrorAttributionConfig {
				t.Fatalf("attribution = %q, want config (D9/D11)", got, generated.LLMErrorAttributionConfig)
			}
		})
	}
}

// ── Row 22 (MR-2/MR-3): near-misses never classify model_retired ───────────

func TestModelRetired_NearMisses_StayUnknown_C24(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "404 generic names the model", status: 404, body: `{"error":{"message":"Model gpt-4o-2024-08-06 not found"}}`},
		{name: "404 OpenAI access-denied verbatim", status: 404, body: `{"error":{"message":"The model 'gpt-4o' does not exist or you do not have access to it"}}`},
		{name: "404 Ollama not-pulled verbatim", status: 404, body: `{"error":{"message":"model 'llama3' not found, try pulling it first"}}`},
		{name: "404 media-404 echoing the model id", status: 404, body: `{"error":{"message":"Model gpt-4o-2024-08-06 not found for image request"}}`},
		{name: "410 decommissioned (D2 defers routing; user side stays unknown)", status: 410, body: `{"error":{"message":"This model has been decommissioned"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, commonErr := boundaryPair(t, tc.status, nil, tc.body)
			llm := TranslateTurnError(commonErr)
			if llm.Code != CodeUnknown {
				t.Fatalf("code = %q, want %q (MR-2/MR-3: near-miss never reads model_retired)", llm.Code, CodeUnknown)
			}
			// Byte-identical residual: the unknown verdict's copy is the
			// catalogue's unknown sentence, unchanged.
			if llm.Message != defaultUserMessage(CodeUnknown) {
				t.Fatalf("message = %q, want the residual catalogue copy %q byte-identical (row 22)", llm.Message, defaultUserMessage(CodeUnknown))
			}
		})
	}
}

// MR-3's media strip-retry gate regression note: the media-404 residual
// verdict keeps the media downgrade path eligible.
func TestModelRetired_Media404_KeepsDowngradeEligible_C24(t *testing.T) {
	agentPE, _ := boundaryPair(t, 404, nil, `{"error":{"message":"Model gpt-4o-2024-08-06 not found for image request"}}`)
	if !outcomeFallbackEligible(agentPE, CodeUnknown) {
		t.Fatal("outcomeFallbackEligible(media-404, CodeUnknown) = false — the media strip-retry gate regressed (row 22 note)")
	}
}

// ── Row 4 extended (MAJ-103): copy rules run over the template variants ────

func TestCopyRules_OverProviderMessageVariants_MAJ103(t *testing.T) {
	for _, code := range []string{"provider_auth_failed", "rate_limited", "quota_billing", "model_retired"} {
		tmpl := generated.LLMErrorProviderMessages[code]
		if tmpl == "" {
			t.Fatalf("provider_message variant missing for %q (§6 contract mechanics)", code)
		}
		if strings.Contains(tmpl, "Retrying automatically") || strings.Contains(tmpl, "{countdown}") {
			t.Fatalf("live retry line leaked into the server catalogue for %q — the live line is client-assembled (A-5/C-1)", code)
		}
		if AttributionForCode(LLMErrorCode(code)) == generated.LLMErrorAttributionConfig {
			lower := strings.ToLower(tmpl)
			for _, banned := range []string{"retry", "try again"} {
				if strings.Contains(lower, banned) {
					t.Fatalf("code %q is config-attributed but its provider_message copy advises %q: %q (MAJ-103)", code, banned, tmpl)
				}
			}
		}
	}
}

// ── Row 31 (AU-3 / C-19): key-shaped material never reaches the message ────

func TestKeyFragment_NeverInMessage_C19(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "auth body carrying a key fragment", status: 401, body: `{"error":{"message":"Incorrect API key provided: sk-proj-ABCDEF1234567890"}}`},
		{name: "billing body carrying a key fragment", status: 402, body: `{"error":{"message":"insufficient credits for key sk-proj-ZZZZ"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, commonErr := boundaryPair(t, tc.status, nil, tc.body)
			llm := TranslateTurnError(commonErr)
			if strings.Contains(llm.Message, "sk-proj") {
				t.Fatalf("message leaks the key fragment: %q (AU-3/C-19)", llm.Message)
			}
			if llm.Detail != "" && strings.Contains(llm.Detail, "sk-proj") {
				t.Fatalf("detail leaks the key fragment: %q (C-19)", llm.Detail)
			}
		})
	}
}

// ── Row 32 (RG-1 / C-23): context_too_long is byte-identical ───────────────

func TestContextTooLong_ByteIdentical_C23(t *testing.T) {
	_, commonErr := boundaryPair(t, 400, nil, `{"error":{"message":"This model's maximum context length is 16385 tokens"}}`)
	llm := TranslateTurnError(commonErr)
	if llm.Code != CodeContextTooLong {
		t.Fatalf("code = %q, want %q (C-23: context-length handling unchanged)", llm.Code, CodeContextTooLong)
	}
	if llm.Message != generated.LLMErrorUserMessages["context_too_long"] {
		t.Fatalf("message = %q, want the catalogue copy %q byte-identical (RG-1)", llm.Message, generated.LLMErrorUserMessages["context_too_long"])
	}
}
