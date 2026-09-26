// Issue #711 — the curated-detail builder feature keeps working.
//
// Since #711 the WS forwarder attaches NO detail to an error frame
// (pkg/gateway/websocket_forward_hub.go::hubError): raw provider detail never
// crosses the wire, which the issue names as one of its acceptable designs,
// and the hub comment documents the site where a future per-session verbose
// opt-in would re-attach `translated.Detail` (or `agent.BuildDetail` for the
// curated branch) behind that session's flag. These tests pin the builder that
// gate will call: the exact `status=NNN body=<preview>` composition, the
// curated echo, and the registered-credential scrubbing applied BEFORE the
// 512-character preview cut (the f82d05479 ordering guarantee: the cut can
// never split a credential into a fragment the replacer no longer
// recognises).

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// buildDetailScrubFixture registers a credential for the scrubbing cases and
// restores the process-wide replacer afterwards (the house pattern from
// pkg/gateway/websocket_forward_test.go).
func buildDetailScrubFixture(t *testing.T) string {
	t.Helper()
	const secret = "sk-live-BUILDCUT-77aaQm3xVw"
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
	cfg := &config.Config{}
	cfg.RegisterSensitiveValues([]string{secret})
	return secret
}

func TestBuildDetail_CuratedEcho_WhenNoProviderError(t *testing.T) {
	got := BuildDetail(nil, "hook aborted turn: policy violation")
	want := "hook aborted turn: policy violation"
	if got != want {
		t.Fatalf("BuildDetail(nil, msg) = %q, want the curated message echoed verbatim %q", got, want)
	}
}

func TestBuildDetail_ComposesStatusAndBody(t *testing.T) {
	got := BuildDetail(&ProviderError{Status: 503, Body: "upstream unavailable"}, "ignored when a body exists")
	want := "status=503 body=upstream unavailable"
	if got != want {
		t.Fatalf("BuildDetail(pe, msg) = %q, want the documented composition %q", got, want)
	}
}

func TestBuildDetail_ScrubsRegisteredCredentialBeforeTheCut(t *testing.T) {
	secret := buildDetailScrubFixture(t)

	// The credential starts at offset 500: an UNSCRUBBED 512-character cut
	// would split it mid-token ("sk-live-BUILDCUT-77a…" — a fragment the
	// replacer would no longer recognise). Scrubbing MUST run first.
	body := strings.Repeat("x", 500) + secret + strings.Repeat("y", 300)

	got := BuildDetail(&ProviderError{Status: 503, Body: body}, "LLM call failed")

	if strings.Contains(got, secret) {
		t.Fatalf("issue #711 context: the builder's detail carries the registered credential: %q", got)
	}
	if !strings.Contains(got, "[FILTERED]") {
		t.Fatalf("the builder's detail %q does not show where the credential was scrubbed", got)
	}
	// Bounded-length oracle from the documented composition: "status=503"
	// (10) + join space (1) + "body=" (5) + preview (512 + "..."), so at most
	// 531 runes.
	const maxLen = 10 + 1 + 5 + 512 + 3
	if len(got) > maxLen {
		t.Fatalf("the builder's detail is %d chars, want at most the documented cut bound %d", len(got), maxLen)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("a body longer than the cut must end in the documented %q ellipsis, got %q", "...", got)
	}
}

func TestBuildDetail_ScrubsCuratedMessagePath_Too(t *testing.T) {
	secret := buildDetailScrubFixture(t)

	got := BuildDetail(nil, "call failed with "+secret+" and stopped")

	if strings.Contains(got, secret) {
		t.Fatalf("the message-path detail carries the registered credential: %q", got)
	}
	if !strings.Contains(got, "[FILTERED]") {
		t.Fatalf("the message-path detail %q does not show where the credential was scrubbed", got)
	}
	if !strings.Contains(got, "call failed with") {
		t.Fatalf("the message-path detail must carry the message's own words, got %q", got)
	}
}

func TestBuildDetail_EmptyInputs_YieldEmptyDetail(t *testing.T) {
	if got := BuildDetail(nil, ""); got != "" {
		t.Fatalf("BuildDetail(nil, \"\") = %q, want the empty string", got)
	}
}
