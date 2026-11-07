// Package issue1664 holds the #1664 assertions that NAME the new
// common.ErrEmptyStream sentinel directly (verify-report.md §B8/B9/B10).
// They are RED-by-missing-symbol: until the developer adds the sentinel the
// package does not compile, and that build failure is the recorded RED
// state (saved log, exit code and "undefined: common.ErrEmptyStream" line)
// — never a skip, and with zero collateral: no existing package's build is
// touched. Once the sentinel lands, this package compiles and the
// assertions below go green with the fix.
package issue1664

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// B8: a stream that ends (EOF or [DONE]) with no finish_reason, no content
// and no tool calls returns the typed common.ErrEmptyStream (errors.Is-able
// through every wrap), with the usage seen on the stream attached — the way
// StreamProviderError attaches it. Driven through the real exported
// transport against a local SSE server, so the assertion is on the
// provider's public behaviour, not on the unexported parser.
func TestIssue1664_B8_EmptyStreamErrorIsErrEmptyStreamWithUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A usage chunk (usage is billed even for an empty answer), then the
		// bare [DONE]: no finish_reason, no content, no tool calls.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":0,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider, err := providers.NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", server.URL, "", "", 0, nil)
	require.NoError(t, err, "SETUP: transport provider")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, streamErr := provider.ChatStream(ctx, []providers.Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
	require.Error(t, streamErr, "#1664 B8: the empty stream must fail, not return a synthesized success")
	require.ErrorIs(t, streamErr, common.ErrEmptyStream,
		"#1664 B8: the error is errors.Is-able to the new common.ErrEmptyStream sentinel")
	var spe *common.StreamProviderError
	require.ErrorAs(t, streamErr, &spe,
		"#1664 B8: the usage seen on the stream is attached to the error as with StreamProviderError (if the fix chooses a different carrier, adapt THIS line in the fix round — the requirement is the billed usage on the error, not a concrete type)")
	require.NotNil(t, spe.Usage, "#1664 B8: the attached usage is present")
}

// B9: the sentinel classifies as FailoverTimeout (so a configured fallback
// model is tried) and sits outside the #1280 retry plan (RetryCauseOf "").
func TestIssue1664_B9_SentinelClassifiesAsFailoverTimeoutOutsideRetryPlan(t *testing.T) {
	fe := providers.ClassifyError(common.ErrEmptyStream, "p", "m")
	require.NotNil(t, fe, "#1664 B9: the empty-stream sentinel must classify")
	require.Equal(t, providers.FailoverTimeout, fe.Reason,
		"#1664 B9 (founder Q3 A): a configured fallback model is tried for the empty stream")
	require.Equal(t, "", providers.RetryCauseOf(common.ErrEmptyStream),
		"#1664 B9: the #1280 plan does not take the empty stream")
}

// B10: the used-up empty-stream attempts translate to the new
// provider_empty_reply code — attribution provider, retryable — with the
// founder-approved sentence (Q2, 2026-11-07), never the fixed
// "The model returned an empty response…" text.
func TestIssue1664_B10_TranslatesToProviderEmptyReply(t *testing.T) {
	le := agent.TranslateTurnError(common.ErrEmptyStream)
	require.Equal(t, "provider_empty_reply", string(le.Code),
		"#1664 B10: the empty stream's used-up attempts end in provider_empty_reply")
	require.Equal(t, "The model provider ended its answer without sending anything. Retry — if it keeps happening, open Verbose chat for details.",
		le.Message, "#1664 B10: the founder-approved sentence (Q2, 2026-11-07)")
	require.True(t, le.Retryable, "#1664 B10: the code is retryable")
	require.Equal(t, generated.LLMErrorAttributionProvider, agent.AttributionForCode(le.Code),
		"#1664 B10: attribution provider")
	require.NotEqual(t, "The model returned an empty response. This may indicate a provider error or token limit.",
		le.Message, "#1664 B10: the fixed empty-response text is not used for this case")
}
