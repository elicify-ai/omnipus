// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ordinaryRootCompletedReply is alternatingTimeoutProvider's odd-call content:
// the ordinary root's normal answer after the timeout retry. It is the
// provider double's text, not a sentence read off production output.
const ordinaryRootCompletedReply = "turn completed after retry"

// TestRetryNotice_ProgressDisposition drives the real timeout-retry producer.
// The provider fake stops at the model boundary. retryTimeout, audience
// classification and publishResponseIfNeeded stay real.
//
// runOrdinaryRootTurn (ProcessScheduled) waits until the turn returns, and
// the retry publish happens before that return, so drainOutboundText is a
// non-blocking read of messages already buffered. ProcessScheduled does not
// publish the final reply (SendResponse is false). The returned finalContent
// is therefore published here through publishResponseIfNeeded, which the
// delivery decision names as the unmarked real-reply site.
func TestRetryNotice_ProgressDisposition(t *testing.T) {
	const chatID = "retry-progress-chat"
	al, _, recorder, msgBus := newBoundaryHarness(t, &alternatingTimeoutProvider{})
	runOrdinaryRootTurn(t, al, chatID, "hello")
	recorder.AssertBoundaryInvoked(steer.BoundaryRetryNotice)

	msgs := drainOutboundText(msgBus)
	var notices []bus.OutboundMessage
	var others []bus.OutboundMessage
	for i := range msgs {
		if msgs[i].Content == retryNoticeMarker {
			notices = append(notices, msgs[i])
			continue
		}
		others = append(others, msgs[i])
	}
	if len(notices) != 1 {
		t.Fatalf("retry sentence published %d time(s), want 1; got %+v", len(notices), msgs)
	}
	notice := notices[0]
	if notice.Channel != "webchat" || notice.ChatID != chatID {
		t.Errorf("retry notice channel/chat = %q/%q, want webchat/%q", notice.Channel, notice.ChatID, chatID)
	}
	if !notice.ProgressNotice {
		t.Errorf("retry notice ProgressNotice = false, want true (content %q)", notice.Content)
	}
	for _, m := range others {
		if m.ProgressNotice {
			t.Errorf("non-notice outbound ProgressNotice = true, want false: %+v", m)
		}
	}

	al.lastTurnResultMu.Lock()
	final := al.lastTurnResult.finalContent
	al.lastTurnResultMu.Unlock()
	if final != ordinaryRootCompletedReply {
		t.Errorf("ordinary root final content = %q, want %q", final, ordinaryRootCompletedReply)
	}
	if final == retryNoticeMarker {
		t.Errorf("final content is the retry sentence; the normal answer must be distinct")
	}

	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok || agent == nil {
		t.Fatalf("agent %q not registered", testDefaultAgentID)
	}
	before := len(drainOutboundText(msgBus))
	if before != 0 {
		t.Errorf("outbound bus still held %d message(s) after the first drain", before)
	}
	al.publishResponseIfNeeded(context.Background(), agent, "webchat", chatID, final, "")
	published := drainOutboundText(msgBus)
	if len(published) != 1 {
		t.Fatalf("publishResponseIfNeeded published %d message(s), want 1: %+v", len(published), published)
	}
	if published[0].Content != final {
		t.Errorf("final reply content = %q, want %q", published[0].Content, final)
	}
	if published[0].ProgressNotice {
		t.Errorf("final reply ProgressNotice = true, want false (content %q)", published[0].Content)
	}
	if published[0].Channel != "webchat" || published[0].ChatID != chatID {
		t.Errorf("final reply channel/chat = %q/%q, want webchat/%q", published[0].Channel, published[0].ChatID, chatID)
	}
}
