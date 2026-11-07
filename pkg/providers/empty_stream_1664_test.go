package providers

// Issue #1664 B9 (founder decision 2026-11-07 Q3 A): the empty stream's
// error classifies as FailoverTimeout — so a configured fallback model is
// tried — and RetryCauseOf returns "" so the #1280 retry plan does not take
// it. Oracle: /workspace/logs/fix-1664/verify-report.md §B9. The error is
// produced through the real exported transport against a local SSE server
// that answers only [DONE] (no finish_reason, no content, no tool calls) —
// never by constructing an expected error by hand. Until B8 lands, the
// transport returns a successful empty answer, so there is nothing to
// classify and this test fails on that first (the honest transitive RED;
// the failure message names it).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssue1664_B9_ClassifyEmptyStreamAsFailoverTimeoutOutsideRetryPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", server.URL, "", "", 0, nil)
	require.NoError(t, err, "SETUP: transport provider")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, streamErr := provider.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
	require.Error(t, streamErr,
		"#1664 B8 (transitive): the empty stream must fail as a typed error before it can be classified — today it parses as a successful empty answer, so there is nothing to classify")
	fe := ClassifyError(streamErr, "p", "m")
	require.NotNil(t, fe, "#1664 B9: the empty-stream error must classify (never fall through unclassified)")
	require.Equal(t, FailoverTimeout, fe.Reason,
		"#1664 B9 (founder Q3 A): the empty stream classifies as FailoverTimeout so a configured fallback model is tried")
	require.Equal(t, "", RetryCauseOf(streamErr),
		"#1664 B9: RetryCauseOf returns \"\" — the #1280 retry plan does not take the empty stream")
}
