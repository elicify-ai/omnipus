/**
 * assemble_provider_message_test.go — provider-messages spec RED tests:
 * TDD rows 7 (A-5), 10b (C-16/MAJ-110 identity → template), 13 (DG-8),
 * 11 (C-13/MAJ-001/MAJ-104 transcript), AU-2 (identity-absent → catalogue).
 *
 * Oracles come from the SPEC ONLY:
 *   - §6: the terminal rate_limited sentence is
 *     "{provider} is busy right now. You can retry the turn." — the LIVE line
 *     ("Retrying automatically in {countdown}") is CLIENT-assembled and must
 *     never exist in a server catalogue (A-5, C-1).
 *   - C-16/MAJ-110: the template's {provider} is the FAILING ATTEMPT's
 *     identity — a chain that skipped the primary and failed on the fallback
 *     names the FALLBACK (FailoverError.Provider/Model, populated by the real
 *     FallbackChain.Execute from the run closure — no HandleErrorResponse
 *     change). Identity-absent errors keep the facts-absent catalogue copy
 *     (AU-2), which must itself be unchanged.
 *   - DG-8 (row 13): the channel-reply translator renders the same
 *     facts-aware sentence.
 *   - C-13/MAJ-001/MAJ-104 (row 11): the assembled sentence persists in the
 *     transcript (trusted-set extension) — never replaced by
 *     defaultUserMessage(code) — and the persisted entry carries the
 *     provider_message subtype (the flag half is BLOCKED: no field exists
 *     on the persisted shape yet; see the placeholder test).
 *
 * COMPILE-RED (CodeQuotaBilling) plus ASSERTION-RED (template substitution
 * and transcript persistence do not exist yet).
 */

package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// boundaryErr builds the error a provider HTTP boundary produces, via the
// real HandleErrorResponse.
func boundaryErr(t *testing.T, status int, body string) error {
	t.Helper()
	header := http.Header{"Content-Type": []string{"application/json"}}
	resp := &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	err := common.HandleErrorResponse(resp, "https://api.example.com")
	if err == nil {
		t.Fatalf("HandleErrorResponse returned nil for status %d", status)
	}
	return err
}

// exhaustedFromChain drives the REAL FallbackChain.Execute: primary skipped
// via a pre-set cooldown, fallback failing with the given status/body, both
// candidates then exhausted. Returns the exhaustion error.
func exhaustedFromChain(t *testing.T, failStatus int, failBody string) error {
	t.Helper()
	ct := providers.NewCooldownTracker()
	ct.MarkFailure(providers.ModelKey("openrouter", "model-a"), providers.FailoverRateLimit)
	fc := providers.NewFallbackChain(ct)
	failErr := boundaryErr(t, failStatus, failBody)
	_, err := fc.Execute(context.Background(),
		[]providers.FallbackCandidate{
			{Provider: "openrouter", Model: "model-a"},
			{Provider: "anthropic", Model: "claude-x"},
		},
		func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
			return nil, failErr
		})
	return err
}

// ── Row 7 (A-5): terminal line in the catalogue, live line client-only ─────

func TestCatalogue_TerminalLineOnly_A5(t *testing.T) {
	// Exact §6 terminal sentence.
	if got, want := generated.LLMErrorProviderMessages["rate_limited"], "{provider} is busy right now. You can retry the turn."; got != want {
		t.Fatalf("terminal rate_limited provider_message = %q, want %q (§6, A-5)", got, want)
	}
	// The live retry line is CLIENT-assembled (C-1/C-15): no server entry may
	// carry it.
	for code, tmpl := range generated.LLMErrorProviderMessages {
		if strings.Contains(tmpl, "Retrying automatically") {
			t.Fatalf("catalogue entry %q carries the live retry line %q — that line is client-assembled from provider_retry facts (A-5/C-1)", code, tmpl)
		}
		if strings.Contains(tmpl, "{countdown}") {
			t.Fatalf("catalogue entry %q carries {countdown} — a client-side slot that must never exist server-side (A-5)", code)
		}
	}
}

// ── Row 10b (C-16/MAJ-110): the failing attempt's provider names the line ──

func TestTemplate_SubstitutesFailingAttemptProvider_C16(t *testing.T) {
	err := exhaustedFromChain(t, 401,
		`{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`)
	llm := TranslateTurnError(err)
	if llm.Code != CodeProviderAuthFailed {
		t.Fatalf("code = %q, want %q", llm.Code, CodeProviderAuthFailed)
	}
	// The chain skipped the cooling primary; the FALLBACK attempt (anthropic)
	// is the failing one — C-16 says the line names IT, never the primary.
	want := templateWith("provider_auth_failed", "anthropic")
	if llm.Message != want {
		t.Fatalf("message = %q, want the §6 template naming the failing attempt's provider: %q (C-16/MAJ-110)", llm.Message, want)
	}
	// AU-3: the key fragment from the body never reaches the message.
	if strings.Contains(llm.Message, "sk-proj") {
		t.Fatalf("message leaks the key fragment: %q (AU-3/C-19)", llm.Message)
	}
}

// ── Row 13 (DG-8): the channel-reply translator is facts-aware ─────────────

func TestChannelReplyTranslator_FactsAware_DG8(t *testing.T) {
	err := exhaustedFromChain(t, 402,
		`{"error":{"message":"insufficient credits"}}`)
	llm := TranslateTurnError(err)
	if llm.Code != CodeQuotaBilling {
		t.Fatalf("code = %q, want %q (DG-8, B-1 scenario)", llm.Code, CodeQuotaBilling)
	}
	// Both candidates failed billing; the last failing attempt is the
	// fallback (anthropic) — the sentence names it.
	want := templateWith("quota_billing", "anthropic")
	if llm.Message != want {
		t.Fatalf("message = %q, want the §6 sentence naming the failing attempt: %q (DG-8/C-16)", llm.Message, want)
	}
	if llm.Retryable {
		t.Fatal("retryable = true, want false (B-1: no auto-retry for billing)")
	}
}

// ── AU-2: identity absent → facts-absent catalogue copy, unchanged ─────────

func TestIdentityAbsent_CatalogueCopyUnchanged_AU2(t *testing.T) {
	// A bare boundary error carries no FailoverError wrapper → no provider
	// identity → the facts-absent CATALOGUE sentence must render.
	err := boundaryErr(t, 402, `{"error":{"message":"insufficient credits"}}`)
	llm := TranslateTurnError(err)
	if llm.Code != CodeQuotaBilling {
		t.Fatalf("code = %q, want %q (AU-2: the code is still the verdict)", llm.Code, CodeQuotaBilling)
	}
	want := generated.LLMErrorUserMessages["quota_billing"]
	if llm.Message != want {
		t.Fatalf("message = %q, want the unchanged facts-absent catalogue copy %q (AU-2)", llm.Message, want)
	}
}

// ── Row 11 (C-13/MAJ-001): the assembled sentence persists ─────────────────

func TestTranscript_KeepsAssembledSentence_C13(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ts := &turnState{
		turnID:              "turn-pm-1",
		agentID:             "main",
		transcriptSessionID: meta.ID,
		transcriptStore:     store,
	}

	assembled := templateWith("quota_billing", "openrouter")
	ts.writeErrorTranscriptWithAbandonment("error", "provider", assembled, CodeQuotaBilling, false)

	entries, err := store.ReadTranscript(meta.ID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Type == session.EntryTypeSystem && e.Status == "error" {
			found = true
			if e.Content != assembled {
				t.Fatalf("persisted content = %q, want the assembled sentence %q (MAJ-001: the provider-named sentence persists, never defaultUserMessage)", e.Content, assembled)
			}
			if e.ErrorCode != string(CodeQuotaBilling) {
				t.Fatalf("persisted error_code = %q, want %q", e.ErrorCode, string(CodeQuotaBilling))
			}
		}
	}
	if !found {
		t.Fatal("no persisted error entry found — the assembled sentence was not written to the transcript (C-13)")
	}
}

// The persisted subtype flag (MAJ-104's persistence half) has no field to
// assert yet — a LOUD placeholder, not a skip: GREEN adds the field to the
// persisted entry and rewrites this placeholder into the real assertions
// (flag set on provider-assembled entries; absent on own-limiter rows; replay
// carrier round-trips it).
func TestTranscript_ProviderMessageSubtypePersisted_MAJ104(t *testing.T) {
	t.Fatal("BLOCKED: the persisted transcript entry has no provider_message subtype field — required by MAJ-104/C-14 (replay must carry the flag); GREEN adds the field and satisfies: flagged entries persist provider_message=true, own-limiter rate_limited rows persist false, and the replay carrier round-trips it")
}
