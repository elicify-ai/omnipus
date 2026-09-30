//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Scenario 7; MAJ-CW-006: actual beginIteration injection is not consumption.
// Control receipt/restart lifecycle is deliberately not redefined in R1.
func TestCWSlideR1_InjectedSteeringSurvivesPreSendFailureAndPressure(t *testing.T) {
	h := cwR1New(t, 20_000)
	ts := h.turn("original steering turn user")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	b := agentContextBudget(h.agent)
	h.append(t, cwR1Step("before-steer", "", strings.Repeat("readable source ", 2_000))...)
	r, p := cwR1OpenAI(t, 0)
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	rr := cwR1Flow(h, ts, messages, p)
	controls := []providers.Message{
		{Role: "user", Content: "[Steering id=cw-steer-001] Original first direction."},
		{Role: "user", Content: "[Steering id=cw-steer-002] Original second direction."},
	}
	ri := rr.rq.ri
	ri.pendingMessages = controls // Normal pending-message input to the REAL injection stage.
	ri.pendingSteeringReceipts = []string{"cw-steer-001", "cw-steer-002"}
	require.Equal(t, agentLoopRunTurnIterationNext, ri.beginIteration(), "real injection must run before the checkpoint")
	cwR1AssertControls(t, ri.messages, controls)
	// A later completed step is present in the candidate; unconsumed steering
	// must stop a prefix cut even though the steer is no longer the tail slot.
	after := cwR1Step("after-steer", "", "newest result")
	h.append(t, after...)
	ri.messages = append(ri.messages, after...)
	require.Greater(t, toolResultShareTokens(ri.messages), h.agent.ContextWindow/2, "instrument: relative-share pressure fires before first send")
	require.Less(t, requestTokens(ri.messages, nil), b, "instrument: the newest floor still fits the total budget")
	ri.messages = h.check(t, ts, ri.messages)
	cwR1AssertControls(t, ri.messages, controls)
	cwR1Prepare(t, rr)

	// Real connection refusal BEFORE an HTTP request is sent. Reserve and
	// close a local listener, then let the actual adapter attempt the dial.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadURL := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	deadProvider, err := providers.NewHTTPProviderWithTimeouts("r1-inert-fixture-key", deadURL, "", "max_tokens", 10, 0, nil)
	require.NoError(t, err)
	ri.rf.rt.activeProvider = deadProvider
	_, sendErr := ri.rf.rt.callProvider(ri.rf.callMessages, nil)
	var netErr *net.OpError
	require.ErrorAs(t, sendErr, &netErr, "instrument: real failure is the pre-send dial, not context cancellation")
	require.Equal(t, "dial", netErr.Op)
	require.Empty(t, r.requests(t), "no request reached the recording provider")
	t.Logf("instrument: original controls survived pressure; actual pre-send dial failed: %v", sendErr)

	// Retry pressure after the failed-before-send attempt. This must not mark
	// the original steering consumed or inject duplicate copies.
	later := cwR1Step("retry-newest", strings.Repeat("n", b*5/4), "retry result")
	h.append(t, later...)
	ri.messages = append(ri.messages, later...)
	ri.messages = h.check(t, ts, ri.messages)
	ri.rf.rt.activeProvider = p
	cwR1Send(t, rr)
	require.Len(t, r.requests(t), 1, "one actual send after the failed pre-send attempt")
	sent := cwR1Messages(t, r.requests(t)[0])
	cwR1AssertControls(t, sent, controls)
	cwR1AssertAnchor(t, sent, h.archive(t)[0].Message)
	cwR1AssertComplete(t, sent)
	require.Greater(t, cwR1Skip(t, h), 0, "the pre-control whole step actually left before first send")
	archiveMessages := make([]providers.Message, 0, len(h.archive(t)))
	for _, a := range h.archive(t) {
		archiveMessages = append(archiveMessages, a.Message)
	}
	cwR1AssertControls(t, archiveMessages, controls)
}

func TestCWSlideR1_ContextRejectionRetryPreservesInjectedControlOrder(t *testing.T) {
	h := cwR1New(t, 80_000)
	ts := h.turn("reactive steering user")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	h.append(t, cwR1Step("reactive-pre-control", "", strings.Repeat("older content ", 300))...)
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	r, p := cwR1OpenAI(t, 2)
	rr := cwR1Flow(h, ts, messages, p)
	controls := []providers.Message{{Role: "user", Content: "[Steering id=cw-reactive-001] First original direction."},
		{Role: "user", Content: "[Steering id=cw-reactive-002] Second original direction."}}
	rr.rq.ri.pendingMessages = controls
	rr.rq.ri.pendingSteeringReceipts = []string{"cw-reactive-001", "cw-reactive-002"}
	require.Equal(t, agentLoopRunTurnIterationNext, rr.rq.ri.beginIteration())
	newest := cwR1Step("reactive-after-control", "", cwR1Unicode(4_096))
	h.append(t, newest...)
	rr.rq.ri.messages = append(rr.rq.ri.messages, newest...)
	cwR1Fits(t, h, rr.rq.ri.messages)
	cwR1Retry(t, rr)
	require.NoError(t, rr.rq.ri.rf.err)
	requests := r.requests(t)
	require.Len(t, requests, 3)
	for _, request := range requests {
		sent := cwR1Messages(t, request)
		cwR1AssertControls(t, sent, controls)
		cwR1AssertComplete(t, sent)
	}
	require.False(t, cwR1HasCall(cwR1Messages(t, requests[1]), "reactive-pre-control"), "real relief happened, not only a notice change")
}

func cwR1AssertControls(t *testing.T, messages, controls []providers.Message) {
	t.Helper()
	// Normalization may merge adjacent user messages; exact original text/id
	// and sequence must still survive once each in the final serialized body.
	var orderedText strings.Builder
	for _, message := range messages {
		orderedText.WriteString(message.Content)
		orderedText.WriteByte('\n')
	}
	text := orderedText.String()
	previous := -1
	for _, control := range controls {
		require.Equal(t, 1, strings.Count(text, control.Content), "one exact original control copy, neither duplicate nor consumed early")
		position := strings.Index(text, control.Content)
		require.Greater(t, position, previous, "original steering sequence is preserved")
		previous = position
	}
	data, err := json.Marshal(messages)
	require.NoError(t, err)
	require.NotEmpty(t, data, "the assertion inspects actual messages, not a control receipt alone")
}
