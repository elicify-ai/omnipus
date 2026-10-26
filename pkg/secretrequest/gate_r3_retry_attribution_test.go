package secretrequest_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// TestRetryResumes_FailedDeliveryIsAttributed is Train 2 gate r3 T2-SF-02
// (residual): a saved request whose follow-up cannot be handed to the agent on
// a boot/attach retry keeps its warning AND leaves the operator an Error-level
// diagnostic naming the request, the session, the failed stage, a safe cause
// category and the repair — never the raw lower-level error text.
func TestRetryResumes_FailedDeliveryIsAttributed(t *testing.T) {
	c := newRegistryFaultCapture(t)
	f := c.f
	sid := f.newSession("omnipus", "")
	rec := f.create(sid, "omnipus", providerInput())
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	require.NoError(t, err)

	const rawCause = "agent queue rejected session-internal-detail-7731"
	opts := c.options(f.sessions, f.store)
	opts.Resume = func(secretrequest.Record) error { return errors.New(rawCause) }
	restarted := secretrequest.NewRegistry(opts)
	require.NoError(t, restarted.Recover())
	require.Error(t, restarted.RetryResumes(), "instrument: the retry's delivery fails")
	require.True(t, unconfirmed(t, restarted, sid, rec.Card.RequestId), "the warning stays")

	var attributed []map[string]any
	for _, entry := range c.logs(t) {
		fields, _ := entry["fields"].(map[string]any)
		if fields == nil {
			fields = entry
		}
		if fields["stage"] == "continuation_dispatch" && fields["request_id"] == rec.Card.RequestId && fields["session_id"] == sid {
			attributed = append(attributed, entry)
		}
	}
	require.Len(t, attributed, 1, "one attributed diagnostic for the failed retry")
	entry := attributed[0]
	fields, _ := entry["fields"].(map[string]any)
	if fields == nil {
		fields = entry
	}
	level, isText := entry["level"].(string)
	require.True(t, isText, "the diagnostic carries a level")
	require.Equal(t, "error", strings.ToLower(level))
	require.Equal(t, "dispatch_failed", fields["cause_category"])
	require.Contains(t, fields["repair"], "restart the gateway")
	raw, err := os.ReadFile(c.logPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "session-internal-detail-7731", "the raw cause is never logged")
}
