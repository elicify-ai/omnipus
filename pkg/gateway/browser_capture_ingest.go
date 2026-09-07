package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/gorilla/websocket"
)

const captureIngestOfferQueueCapacity = 4
const captureIngestOfferLifetime = 20 * time.Second

var errCaptureIngestOfferQueueFull = errors.New("capture ingest offer queue full")
var errCaptureIngestInvalidOffer = errors.New("invalid capture ingest offer")

type queuedCaptureOffer struct {
	frame    generated.BrowserCaptureOfferFrame
	deadline time.Time
}

func (h *captureIngestWSHandler) serveBoundIngest(conn *websocket.Conn, cs *browser.CaptureSession, browsingKey string, validate bool) {
	socketCtx, cancelSocket := context.WithCancelCause(context.Background())
	ic := &captureIngestConn{conn: conn}
	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(func() { cancelSocket(context.Canceled); _ = conn.Close() }) }
	defer closeConn()
	send := func(action string, reason *string, _, _, maxBitrate int) error {
		frame := generated.BrowserCaptureControlFrame{Type: string(generated.WsFrameTypeBrowserCaptureControl), Action: action, Reason: reason}
		if action == "recapture" {
			// Identity and geometry must come from one confirmed snapshot, including
			// callbacks queued before a newer transition committed.
			state := cs.FrameState()
			if state.Generation == 0 || state.TargetID == "" || state.Width <= 0 || state.Height <= 0 {
				return errors.New("capture recapture requires a confirmed frame")
			}
			generation := int(state.Generation)
			frame.CaptureGeneration, frame.TargetId = &generation, &state.TargetID
			frame.ExpectedWidth, frame.ExpectedHeight, frame.CaptureScale = &state.Width, &state.Height, &state.Scale
		}
		if maxBitrate > 0 {
			frame.MaxBitrate = &maxBitrate
		}
		return ic.sendJSON(frame)
	}
	previousClose, epoch, err := cs.BindIngestContext(socketCtx, send, closeConn)
	if err != nil {
		slog.Warn("capture-ingest: binding rejected", "error", err, "browsing_key", browsingKey)
		return
	}
	if previousClose != nil {
		previousClose()
	}
	defer cs.UnbindIngest(epoch)
	if err := send("recapture", nil, 0, 0, 0); err != nil {
		slog.Warn("capture-ingest: initial frame replay failed", "error", err, "browsing_key", browsingKey)
		return
	}
	offers := make(chan queuedCaptureOffer, captureIngestOfferQueueCapacity)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		h.readCaptureIngest(socketCtx, cancelSocket, conn, cs, epoch, browsingKey, validate, offers)
	}()
	defer func() { closeConn(); <-readerDone }()
	sendError := func(err error) {
		slog.Warn("capture-ingest: offer rejected", "error", err, "browsing_key", browsingKey)
		if sendErr := ic.sendJSON(generated.ErrorFrame{Type: string(generated.WsFrameTypeError), Message: err.Error()}); sendErr != nil {
			slog.Debug("capture-ingest: error response failed", "error", sendErr, "browsing_key", browsingKey)
		}
	}
	for {
		select {
		case <-socketCtx.Done():
			if cause := context.Cause(socketCtx); errors.Is(cause, errCaptureIngestOfferQueueFull) || errors.Is(cause, errCaptureIngestInvalidOffer) {
				sendError(cause)
			}
			return
		case <-cs.Done():
			return
		case offer := <-offers:
			if socketCtx.Err() != nil {
				continue
			}
			frame := offer.frame
			if frame.CaptureGeneration == nil || *frame.CaptureGeneration <= 0 || *frame.CaptureGeneration > 9007199254740991 || frame.OfferId == nil || *frame.OfferId <= 0 || *frame.OfferId > 9007199254740991 || frame.TargetId == nil || *frame.TargetId == "" {
				sendError(errors.New("capture ingest offer requires capture generation, target and offer ID"))
				return
			}
			request, cancel := context.WithDeadline(socketCtx, offer.deadline)
			answer, err := cs.HandleIngestOfferForBinding(request, epoch, uint64(*frame.OfferId), frame.Sdp, uint64(*frame.CaptureGeneration), *frame.TargetId)
			cancel()
			if socketCtx.Err() != nil {
				continue
			}
			if errors.Is(err, webrtc.ErrStaleIngestOffer) {
				continue
			}
			if err != nil {
				sendError(fmt.Errorf("capture ingest offer failed: %w", err))
				return
			}
			if err := ic.sendJSON(generated.BrowserCaptureAnswerFrame{Type: string(generated.WsFrameTypeBrowserCaptureAnswer), Sdp: answer, CaptureGeneration: frame.CaptureGeneration, TargetId: frame.TargetId, OfferId: frame.OfferId}); err != nil {
				return
			}
		}
	}
}

// One reader keeps socket death and heartbeats observable during negotiation.
// Only offers queue; their deadline starts at receipt, not after earlier work.
func (h *captureIngestWSHandler) readCaptureIngest(ctx context.Context, cancel context.CancelCauseFunc, conn *websocket.Conn, cs *browser.CaptureSession, epoch uint64, browsingKey string, validate bool, offers chan<- queuedCaptureOffer) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			cancel(err)
			return
		}
		received := time.Now()
		if ctx.Err() != nil {
			return
		}
		var typ wsTypeOnly
		if json.Unmarshal(data, &typ) != nil {
			continue
		}
		if validate {
			if schema := captureFrameSchemaName(typ.Type); schema != "" {
				if message, serverErr := ValidateInboundFrameJSON(schema, data); message != "" {
					slog.Warn("capture-ingest: invalid inbound frame", "schema", schema, "error", message, "schema_unavailable", serverErr, "browsing_key", browsingKey)
					if typ.Type == string(generated.WsFrameTypeBrowserCaptureOffer) {
						cancel(errCaptureIngestInvalidOffer)
						return
					}
					continue
				}
			}
		}
		switch typ.Type {
		case string(generated.WsFrameTypeBrowserCaptureOffer):
			var frame generated.BrowserCaptureOfferFrame
			if json.Unmarshal(data, &frame) != nil {
				cancel(errCaptureIngestInvalidOffer)
				return
			}
			select {
			case offers <- queuedCaptureOffer{frame: frame, deadline: received.Add(captureIngestOfferLifetime)}:
			case <-ctx.Done():
				return
			default:
				cancel(errCaptureIngestOfferQueueFull)
				return
			}
		case string(generated.WsFrameTypeBrowserCaptureControl):
			var frame generated.BrowserCaptureControlFrame
			if json.Unmarshal(data, &frame) != nil || frame.Action != "ping" {
				continue
			}
			if recordCaptureHealth(cs, epoch, frame) && frame.Reason != nil && *frame.Reason != "" {
				slog.Warn("capture-ingest: encoder reported a stream-quality failure", "reason", *frame.Reason, "browsing_key", browsingKey)
			}
		}
	}
}
