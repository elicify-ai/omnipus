package gateway

import (
	"context"
	"encoding/json"
	"errors"
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
		if action == "recapture" {
			return errors.New("capture recapture requires immutable frame admission")
		}
		frame := generated.BrowserCaptureControlFrame{Type: string(generated.WsFrameTypeBrowserCaptureControl), Action: action, Reason: reason}
		if maxBitrate > 0 {
			frame.MaxBitrate = &maxBitrate
		}
		return ic.sendJSONContext(socketCtx, frame, nil)
	}
	recapture := func(ctx context.Context, state browser.CaptureFrameState, current func() bool) error {
		generation := int(state.Generation)
		frame := generated.BrowserCaptureControlFrame{
			Type: string(generated.WsFrameTypeBrowserCaptureControl), Action: "recapture",
			CaptureGeneration: &generation, TargetId: &state.TargetID,
			ExpectedWidth: &state.Width, ExpectedHeight: &state.Height, CaptureScale: &state.Scale,
		}
		err := ic.sendJSONContext(ctx, frame, current)
		if err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			// A transport failure retires this socket so the encoder can reconnect.
			// Retired requests and writer-admission timeouts leave it available.
			cancelSocket(err)
		}
		return err
	}
	previousClose, epoch, err := cs.BindIngestRecaptureContext(socketCtx, send, recapture, closeConn)
	if err != nil {
		slog.Warn("capture-ingest: binding rejected", "error", err, "browsing_key", browsingKey)
		return
	}
	if previousClose != nil {
		previousClose()
	}
	defer cs.UnbindIngest(epoch)
	// A pending layout does not authorize a command or invalidate an otherwise
	// healthy socket. Its measured transition will issue the first recapture.
	cs.RecaptureFrameContext(socketCtx, cs.FrameState())
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
			err := ic.answerCaptureOffer(socketCtx, cs, epoch, offer)
			if socketCtx.Err() != nil || errors.Is(err, webrtc.ErrStaleIngestOffer) {
				// The loop retains the connection-wide terminal notice, while an
				// ordinary response never revives its retired request context.
				continue
			}
			if err != nil {
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
