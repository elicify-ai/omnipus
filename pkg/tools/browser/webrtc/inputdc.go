//go:build !lite

package webrtc

import (
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
)

// inputQueueCapacity bounds one viewer's pending input-event backlog
// (fix-wave finding 2c). 64 is generous for a live cursor/keystream under
// normal dispatch latency (Q4 spike: p95 CDP round trip ~21ms, so a
// consumer keeping pace drains this in well under a second even at a heavy
// input rate) while still bounding memory for a viewer whose sink has
// genuinely wedged.
const inputQueueCapacity = 64

// wireInputDataChannel is called from a viewer PeerConnection's
// OnDataChannel callback when the browser's "input" data channel arrives.
// Every message is handed RAW (unparsed) to the Session's InputSink, tagged
// with viewerID -- this package never interprets the payload, so it has no
// dependency on pkg/api/generated; the gateway decodes it as a
// BrowserInputFrame (or whatever wire type it chooses) and dispatches it.
//
// Q1/Q4 gotcha (see wv1-spike-results.md): a browser's dc.send(string) is a
// TEXT-typed data-channel frame; Pion's dc.Send() emits BINARY. A text vs.
// binary mismatch is silently dropped by the browser-side onmessage handler.
// Every reply this package sends (SendToViewer) therefore uses SendText,
// never Send. Incoming binary frames are logged and dropped rather than
// forwarded, since the browser side of this protocol only ever sends text.
//
// Fix-wave finding 2c (dispatch ordering): messages are handed to a single,
// PER-VIEWER serialized queue/worker (enqueueInput/runInputQueue) rather
// than the previous "go s.sink(viewerID, raw)" spawned fresh per message.
// Pion invokes OnMessage serially per data channel, but that guarantee was
// being thrown away the instant each message got its own goroutine --
// nothing serialized WHICH goroutine's sink call actually ran first once
// more than one was in flight, so a slow dispatch for an EARLIER message
// (the gateway's real CDP round trip) could complete after a fast dispatch
// for a LATER one, delivering e.g. keydown/keyup out of order to the CDP
// layer. The queue+worker preserves order while still never letting a slow
// sink block Pion's own OnMessage callback: enqueueInput is non-blocking
// (drops the OLDEST queued event under backpressure, mirroring live.go's
// queueAck coalescing pattern -- a live cursor/key stream that has fallen
// behind is more useful caught up on the FRESHEST state than stalled
// replaying a backlog), and the worker goroutine that actually calls
// s.sink runs independently of Pion's dispatch goroutine.
func (s *Session) wireInputDataChannel(prefix, viewerID string, dc *webrtc.DataChannel) {
	queue := make(chan []byte, inputQueueCapacity)
	var closeQueueOnce sync.Once
	closeQueue := func() { closeQueueOnce.Do(func() { close(queue) }) }

	go s.runInputQueue(viewerID, queue)

	dc.OnOpen(func() {
		s.logf("%s input data channel OPEN (label=%s)", prefix, dc.Label())
	})
	dc.OnClose(func() {
		s.logf("%s input data channel closed", prefix)
		closeQueue()
	})
	dc.OnError(func(err error) {
		s.logf("%s input data channel error: %v", prefix, err)
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !msg.IsString {
			s.logf("%s WARNING: binary input frame received (want text), ignoring %d bytes", prefix, len(msg.Data))
			return
		}
		if s.sink == nil {
			return
		}
		// Copy before handing off: Pion may reuse/release the underlying
		// buffer once OnMessage returns, and the queue worker consumes this
		// asynchronously.
		raw := make([]byte, len(msg.Data))
		copy(raw, msg.Data)
		s.enqueueInput(prefix, viewerID, queue, raw)
	})
}

// enqueueInput pushes raw onto queue without ever blocking the caller (Pion's
// own OnMessage callback -- see wireInputDataChannel's doc comment): if the
// queue is full, the OLDEST queued item is dropped to make room, not the new
// one, mirroring live.go's queueAck coalescing discipline. Logged at
// Session's normal logf (the gateway's webrtcRelayLogf classifies a line
// with neither "failed" nor "warning" in it to Debug, matching the fix's
// "log drops at Debug" requirement).
func (s *Session) enqueueInput(prefix, viewerID string, queue chan []byte, raw []byte) {
	for {
		select {
		case queue <- raw:
			return
		default:
			select {
			case <-queue:
				s.logf("%s input queue full for viewer %s, dropped oldest queued event", prefix, viewerID)
			default:
			}
		}
	}
}

// runInputQueue is the single goroutine that drains one viewer's input data-
// channel queue and invokes the Session's InputSink for each message IN
// ORDER, until queue is closed (dc.OnClose) -- draining whatever remains
// queued first so a viewer's very last few input events right before
// disconnect are not silently discarded. A slow InputSink call here (the
// gateway's real CDP round trip) only delays the NEXT queued message for
// THIS viewer; it never blocks Pion's OnMessage callback (enqueueInput is
// always non-blocking) and never blocks any OTHER viewer's own queue
// (each data channel gets its own worker/queue pair).
func (s *Session) runInputQueue(viewerID string, queue chan []byte) {
	for raw := range queue {
		if s.sink != nil {
			s.sink(viewerID, raw)
		}
	}
}

// SendToViewer sends msg (typically a JSON ack or state frame the gateway
// built after handling an InputSink callback) to viewerID's "input" data
// channel as a TEXT frame. Returns an error if the viewer is unknown or its
// data channel has not opened yet (the channel opens asynchronously shortly
// after HandleViewerOffer returns the answer).
//
// Fix-wave comment note: this method currently has NO production caller —
// the gateway (pkg/gateway/browser_webrtc.go) chose the main
// /api/v1/browser/ws connection, not this data channel, as the path for
// surfacing input-dispatch acks/errors back to a viewer (see
// surfaceWebRTCInputError), so replies never round-trip over the same
// channel input arrived on. Kept as part of this type's public contract
// (exercised directly by this package's own Go<->Go tests) since the
// data-channel round trip it implements is still real and may gain a
// production caller later; it is not dead code to be deleted, just currently
// unused outside tests.
func (s *Session) SendToViewer(viewerID string, msg []byte) error {
	s.viewersMu.Lock()
	vc, exists := s.viewers[viewerID]
	var dc *webrtc.DataChannel
	if exists {
		dc = vc.dc
	}
	s.viewersMu.Unlock()

	if !exists {
		return fmt.Errorf("webrtc: send to viewer %q: no such viewer", viewerID)
	}
	if dc == nil {
		return fmt.Errorf("webrtc: send to viewer %q: input data channel not open yet", viewerID)
	}
	if err := dc.SendText(string(msg)); err != nil {
		return fmt.Errorf("webrtc: send to viewer %q: %w", viewerID, err)
	}
	return nil
}
