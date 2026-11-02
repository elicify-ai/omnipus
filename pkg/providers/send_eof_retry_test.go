// send_eof_retry_test.go — 6b UAT finding U3a (validator-report.md issue 2,
// evidence /workspace/logs/6b-uat/gateway-v.log 17:40:12): a provider that
// ACCEPTS the TCP connection and closes it without any HTTP response fails
// the send with Go's *url.Error{Op: "Post"} wrapping io.EOF, wrapped by
// "failed to send request: %w". The gateway logged exactly this shape —
// `unclassified error from fakeprov/fake-model: ... Post ".../chat/completions": EOF`
// — and the #1280 retry plan did NOT retry it: fake-status showed ONE call
// with ["drop","ok"] still queued, and the chat said "This turn didn't
// finish, and we can't tell why."
//
// Chief decision (6b fix round brief): this failure is the `network` retry
// cause — classified by a TYPED check (errors.Is io.EOF / io.ErrUnexpectedEOF
// on the send path), keeping every existing pattern. A bare-"EOF" MESSAGE
// pattern must never exist: a clean io.EOF marks normal stream completion,
// and the letters "eof" appear inside unrelated words — the negative controls
// below pin exactly that.
//
// Oracles (never the implementation's current output):
//   - the cause VALUE: RetryCauseOf == RetryCauseNetwork ("network") — the
//     cause class the retry plan retries and the provider_retry frame carries.
//   - the reason VALUE: equality with the reason ClassifyError returns for a
//     "connection reset by peer" control IN THE SAME TEST — the new failure
//     must land in the same transport-failure family the existing network
//     patterns map to, however that reason is spelled.
//   - the schedule: founder decision Q1 (issue #1280) — waits 2 s, 5 s, 10 s,
//     20 s, 40 s; this round's cases exercise the first two steps.
package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// droppedConnectionSendError builds the exact error shape the HTTP send path
// produces when the provider accepts the connection and closes it without a
// response: http.Client.Do returns *url.Error{Op: "Post"} wrapping the read
// error, and the provider wraps it with "failed to send request: %w"
// (openai_compat/provider.go::Chat — the same wrap in azure/provider.go).
// No mock of the unit under test: the error is byte-identical to production's.
func droppedConnectionSendError(endpoint string, cause error) error {
	return fmt.Errorf("failed to send request: %w", &url.Error{
		Op:  "Post",
		URL: endpoint,
		Err: cause,
	})
}

// errConnectionResetControl is a transport failure the classifier already
// maps into the network retry family (fallback_retry_plan_1280_test.go row
// "connection reset"). The EOF cases assert FAMILY MEMBERSHIP against this
// control, not a hardcoded reason spelling.
var errConnectionResetControl = errors.New("read tcp 10.0.0.1:443: read: connection reset by peer")

// TestSendEOF_DroppedConnectionOnSendClassifiesIntoTheNetworkRetryFamily —
// RED on unmodified code: ClassifyError returns nil for the bare-EOF send
// failure, so the plan sees no cause and the turn dies on the first call
// (U3a: one call, no retry line, vague failure message).
func TestSendEOF_DroppedConnectionOnSendClassifiesIntoTheNetworkRetryFamily(t *testing.T) {
	err := droppedConnectionSendError("http://127.0.0.1:15611/v1/chat/completions", io.EOF)

	fe := ClassifyError(err, "fakeprov", "fake-model")
	require.NotNil(t, fe,
		"a dropped connection on the send path must classify, not fall through as unclassified — U3a: today it returns nil and nothing retries")

	control := ClassifyError(errConnectionResetControl, "", "")
	require.NotNil(t, control)
	require.Equal(t, control.Reason, fe.Reason,
		"the bare-EOF send failure must land in the same transport-failure family as a connection reset")

	require.Equal(t, RetryCauseNetwork, RetryCauseOf(err),
		"chief decision: the dropped connection is the `network` retry cause — the #1280 plan retries it")
	require.Equal(t, "fakeprov", fe.Provider, "the FailoverError carries the caller's pinned candidate identity")
	require.Equal(t, "fake-model", fe.Model)
}

// TestSendEOF_ErrUnexpectedEOFOnTheSendPathStaysInTheNetworkFamily pins the
// sibling cause: io.ErrUnexpectedEOF already reads as a transport failure
// ("unexpected eof" in connectionDropPatterns). The typed fix must KEEP every
// existing pattern. Positive control: passes on unmodified code and must stay
// green after the fix.
func TestSendEOF_ErrUnexpectedEOFOnTheSendPathStaysInTheNetworkFamily(t *testing.T) {
	err := droppedConnectionSendError("https://openrouter.ai/api/v1/chat/completions", io.ErrUnexpectedEOF)

	fe := ClassifyError(err, "", "")
	require.NotNil(t, fe, "an unexpected EOF on the send path is already a transport failure — it must stay one")
	control := ClassifyError(errConnectionResetControl, "", "")
	require.NotNil(t, control)
	require.Equal(t, control.Reason, fe.Reason,
		"ErrUnexpectedEOF stays in the same transport-failure family as a connection reset")
	require.Equal(t, RetryCauseNetwork, RetryCauseOf(err),
		"the plan already retries an unexpected EOF as `network` — the typed fix must not change that")
}

// TestSendEOF_EOFInsideAnotherWordIsNotANetworkCause is the negative control
// for the typed fix: text that merely CONTAINS the letters "eof" inside
// another token, and wraps no io.EOF, must never read as a network failure.
// It passes on unmodified code and guards the fix against the cheap-but-wrong
// shape (a substring "eof" message pattern), which would misclassify both
// cases below — and a bare "EOF" pattern would also swallow the clean
// stream-completion EOF the classifier must keep ignoring.
func TestSendEOF_EOFInsideAnotherWordIsNotANetworkCause(t *testing.T) {
	// A plain rejection (never crossed an HTTP boundary) whose text carries
	// "EOF" inside a model-name token.
	geofence := errors.New("invalid model name GEOFENCE-1")
	// A 400 boundary rejection whose body carries "EOF" inside another word —
	// built through the REAL boundary parse (common.HandleErrorResponse).
	eofBody := peFromBoundaryErr(400, `{"error":{"message":"EOFError in user script"}}`)

	require.False(t, errors.Is(geofence, io.EOF), "precondition: the geofence error wraps no io.EOF")
	require.False(t, errors.Is(eofBody, io.EOF), "precondition: the boundary error wraps no io.EOF")

	require.NotEqual(t, RetryCauseNetwork, RetryCauseOf(geofence),
		"letters 'eof' inside another token are not a dropped connection — never a network cause")
	require.NotEqual(t, RetryCauseNetwork, RetryCauseOf(eofBody),
		"a 400 body mentioning EOF inside another word is a rejected request, not a network cause")
}

// TestSendEOF_RetryPlanRetriesTheDroppedConnectionToEndToEnd — RED on
// unmodified code, plan level (the U3a fixture end to end): the provider
// fails the first two calls with the wrapped-EOF error and answers on the
// third. Today the chain sees an unclassified failure, never schedules a
// wait, and the turn ends on the first call.
func TestSendEOF_RetryPlanRetriesTheDroppedConnectionToEndToEnd(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	endpoint := "http://127.0.0.1:15611/v1/chat/completions"
	calls := 0
	res, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "fakeprov", Model: "fake-model"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			if calls <= 2 {
				return nil, droppedConnectionSendError(endpoint, io.EOF)
			}
			return &LLMResponse{Content: "back online"}, nil
		})
	require.NoError(t, err,
		"U3a: drop, drop, ok — the third call answers; today the unclassified EOF ends the turn on the first call")
	require.Equal(t, "back online", res.Response.Content)
	require.Equal(t, 3, calls, "two dropped connections, then success — exactly 3 calls")
	require.Equal(t, issue1280PlanWaits[:2], rec.waits,
		"founder decision Q1: the plan's first two waits are 2 s and 5 s")
	require.Len(t, rec.infos, 2, "one announced retry per decided retry, from the first failure")
	for i, info := range rec.infos {
		require.Equal(t, i+2, info.Attempt, "attempt number of the call ABOUT to be made")
		require.Equal(t, 6, info.MaxAttempts, "of 6 total calls (RetryPlanMaxAttempts)")
		require.Equal(t, "network", info.Reason, "the provider_retry frame carries the network cause")
		require.Equal(t, "fakeprov", info.Provider)
	}
	require.Equal(t, 2, res.Retries, "the recovery note counts the two retries")
}
