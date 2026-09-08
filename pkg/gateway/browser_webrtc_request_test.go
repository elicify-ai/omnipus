package gateway

import (
	"context"
	"math"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func requestStateFixture(t *testing.T) *browserConnState {
	t.Helper()
	state := &browserConnState{}
	epoch := state.beginAttach()
	if !state.bindAttachment(epoch, &browser.BrowserManager{}, "chat", "panel") {
		t.Fatal("fixture attachment failed")
	}
	t.Cleanup(func() { state.invalidateWebRTCOffer(); state.clearAttachment() })
	return state
}

func TestWebRTCRequestSeparatesNegotiationAndOriginalLifetime(t *testing.T) {
	state := requestStateFixture(t)
	original := state.attachmentRequest()
	firstEpoch := state.beginWebRTCOffer()
	first, ok := state.webRTCOfferRequest(firstEpoch)
	if !ok || first.attachment != original || first.ctx == nil || first.ctx.Done() == nil {
		t.Fatalf("request lacks captured bounded origin: %+v ok=%v", first, ok)
	}
	secondEpoch := state.beginWebRTCOffer()
	second, ok := state.webRTCOfferRequest(secondEpoch)
	if !ok || secondEpoch != firstEpoch+1 || first.ctx.Err() != context.Canceled || second.ctx.Err() != nil || original.ctx.Err() != nil {
		t.Fatalf("replacement lost lifetime separation: first=%v second=%+v original=%v", first.ctx.Err(), second, original.ctx.Err())
	}
	state.finishWebRTCOffer(firstEpoch)
	if second.ctx.Err() != nil {
		t.Error("old completion canceled newer negotiation")
	}
	attachment := &webrtcAttachment{capture: &browser.CaptureSession{}}
	if !state.commitWebRTCAttachmentForRequest(second, attachment) {
		t.Fatal("current request rejected at commit")
	}
	state.finishWebRTCOffer(secondEpoch)
	if second.ctx.Err() != context.Canceled || original.ctx.Err() != nil || state.peekWebRTCAttachment() != attachment {
		t.Fatal("completion did not end only temporary negotiation")
	}
}

func TestWebRTCRequestCannotResolveReplacementAttachment(t *testing.T) {
	state := requestStateFixture(t)
	epoch := state.beginWebRTCOffer()
	original := state.attachmentRequest()
	state.beginAttach()
	got, ok := state.webRTCOfferRequest(epoch)
	if ok || got != nil {
		t.Fatalf("old offer resolved replacement attachment: %+v", got)
	}
	if original.ctx.Err() != context.Canceled {
		t.Fatal("fixture did not cancel original")
	}
}

func TestWebRTCRequestCannotCommitAfterAttachmentReplacement(t *testing.T) {
	state := requestStateFixture(t)
	epoch := state.beginWebRTCOffer()
	request, ok := state.webRTCOfferRequest(epoch)
	if !ok {
		t.Fatal("fixture request missing")
	}
	next := state.beginAttach()
	if !state.bindAttachment(next, &browser.BrowserManager{}, "new-chat", "new-panel") {
		t.Fatal("replacement fixture failed")
	}
	if state.commitWebRTCAttachmentForRequest(request, &webrtcAttachment{capture: &browser.CaptureSession{}}) || state.peekWebRTCAttachment() != nil {
		t.Error("old request committed into replacement attachment")
	}
}

func TestWebRTCRequestInvalidateCancelsNegotiation(t *testing.T) {
	state := requestStateFixture(t)
	epoch := state.beginWebRTCOffer()
	request, ok := state.webRTCOfferRequest(epoch)
	if !ok {
		t.Fatal("request missing")
	}
	state.invalidateWebRTCOffer()
	if request.ctx.Err() != context.Canceled {
		t.Error("invalidation left negotiation alive")
	}
	if got, ok := state.webRTCOfferRequest(epoch); ok || got != nil {
		t.Error("invalidated request still resolves")
	}
}

func TestWebRTCRequestEpochExhaustionDoesNotWrap(t *testing.T) {
	state := requestStateFixture(t)
	state.webrtcEpoch = math.MaxUint64 - 1
	last := state.beginWebRTCOffer()
	if last != math.MaxUint64 {
		t.Fatalf("last valid epoch=%d", last)
	}
	if rejected := state.beginWebRTCOffer(); rejected != 0 || state.webrtcEpoch != math.MaxUint64 {
		t.Fatalf("exhausted epoch wrapped: returned=%d stored=%d", rejected, state.webrtcEpoch)
	}
	state.invalidateWebRTCOffer()
	if state.webrtcEpoch != math.MaxUint64 {
		t.Fatalf("invalidation revived exhausted epoch: %d", state.webrtcEpoch)
	}
}
