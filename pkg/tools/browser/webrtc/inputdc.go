//go:build !lite

package webrtc

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

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
func (s *Session) wireInputDataChannel(prefix, viewerID string, dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		s.logf("%s input data channel OPEN (label=%s)", prefix, dc.Label())
	})
	dc.OnClose(func() {
		s.logf("%s input data channel closed", prefix)
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
		// buffer once OnMessage returns, and dispatch runs in its own
		// goroutine below.
		raw := make([]byte, len(msg.Data))
		copy(raw, msg.Data)
		// Dispatch off the Pion callback goroutine so a slow InputSink (the
		// gateway's CDP round trip) never blocks delivery of the NEXT
		// data-channel message -- Pion invokes OnMessage serially per data
		// channel. This package does not itself guarantee cross-message
		// ordering once handed to the sink; the gateway's dispatch path
		// (LiveInput's controller lock) is responsible for that if needed.
		go s.sink(viewerID, raw)
	})
}

// SendToViewer sends msg (typically a JSON ack or state frame the gateway
// built after handling an InputSink callback) to viewerID's "input" data
// channel as a TEXT frame. Returns an error if the viewer is unknown or its
// data channel has not opened yet (the channel opens asynchronously shortly
// after HandleViewerOffer returns the answer).
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
