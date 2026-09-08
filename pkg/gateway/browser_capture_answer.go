package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// answerCaptureOffer handles an already validated offer using its original
// receipt deadline. The caller owns terminal connection-wide notices.
func (ic *captureIngestConn) answerCaptureOffer(socket context.Context, cs *browser.CaptureSession, epoch uint64, offer queuedCaptureOffer) error {
	frame := offer.frame
	request, cancel := context.WithDeadline(socket, offer.deadline)
	defer cancel()
	current := func() bool {
		return cs.IsCurrentIngestOffer(epoch, uint64(*frame.OfferId), uint64(*frame.CaptureGeneration), *frame.TargetId)
	}
	send := func(response any) error {
		err := ic.sendJSONContext(request, response, current)
		if err != nil && !current() {
			return webrtc.ErrStaleIngestOffer
		}
		return err
	}
	answer, err := cs.HandleIngestOfferForBinding(request, epoch, uint64(*frame.OfferId), frame.Sdp, uint64(*frame.CaptureGeneration), *frame.TargetId)
	if errors.Is(err, webrtc.ErrStaleIngestOffer) {
		return err
	}
	if err != nil {
		if sendErr := send(generated.ErrorFrame{Type: string(generated.WsFrameTypeError), Message: fmt.Sprintf("capture ingest offer failed: %v", err)}); sendErr != nil {
			return sendErr
		}
		return err
	}
	return send(generated.BrowserCaptureAnswerFrame{Type: string(generated.WsFrameTypeBrowserCaptureAnswer), Sdp: answer, CaptureGeneration: frame.CaptureGeneration, TargetId: frame.TargetId, OfferId: frame.OfferId})
}
