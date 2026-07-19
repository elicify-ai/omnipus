package main

import (
	"encoding/json"
	"time"

	"github.com/pion/webrtc/v4"
)

// inputEvent is the wire shape the viewer sends on the "input" data
// channel -- one JSON object per pointer/keyboard event, framed as a TEXT
// data-channel message (dc.send(string) on the browser side). Q1 proved
// Pion's dc.Send() emits BINARY frames while a browser dc.send(string) is
// TEXT-typed and the mismatch silently breaks the browser-side handler, so
// every reply in this file uses dc.SendText, never dc.Send.
type inputEvent struct {
	ID      int64   `json:"id"`
	Type    string  `json:"type"` // mousemove|mousedown|mouseup|wheel|keydown|keyup|ping
	TClient float64 `json:"t_client,omitempty"`

	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Button int     `json:"button,omitempty"`
	DeltaX float64 `json:"deltaX,omitempty"`
	DeltaY float64 `json:"deltaY,omitempty"`

	Key     string `json:"key,omitempty"`
	Code    string `json:"code,omitempty"`
	KeyCode int    `json:"keyCode,omitempty"`
}

// inputAck is the reply sent back on the same data channel. The viewer
// computes its own dispatch RTT from t_client vs its local receive time;
// t_server_recv/t_dispatched are extra server-side diagnostics (their
// difference is the pure CDP-dispatch duration, isolating it from network
// RTT).
type inputAck struct {
	ID            int64  `json:"id"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	TServerRecvMS float64 `json:"t_server_recv"`
	TDispatchedMS float64 `json:"t_dispatched"`
}

func msEpoch(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e6
}

// wireInputDataChannel is called from the viewer PeerConnection's
// OnDataChannel callback when the browser's "input" data channel arrives.
// Every event is dispatched through the shared inputBridge to the Node
// encoder orchestrator (run.js), which translates it into a CDP
// Input.dispatchMouseEvent/dispatchKeyEvent call on the captured tab. This
// is the ONLY path input takes -- CDP never carries video, so unlike the
// old WebCodecs design's shared ackWorker queue, input can never contend
// with pixels.
func wireInputDataChannel(prefix string, dc *webrtc.DataChannel, bridge *inputBridge) {
	dc.OnOpen(func() {
		serverLog.Add("%s input data channel OPEN (label=%s)", prefix, dc.Label())
	})
	dc.OnClose(func() {
		serverLog.Add("%s input data channel closed", prefix)
	})
	dc.OnError(func(err error) {
		serverLog.Add("%s input data channel error: %v", prefix, err)
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !msg.IsString {
			serverLog.Add("%s WARNING: binary input frame received (want text), ignoring %d bytes", prefix, len(msg.Data))
			return
		}
		// Dispatch off the Pion callback goroutine so a slow CDP round
		// trip never blocks delivery of the NEXT data-channel message
		// (Pion invokes OnMessage serially per data channel).
		go handleInputMessage(prefix, dc, bridge, msg.Data)
	})
}

func handleInputMessage(prefix string, dc *webrtc.DataChannel, bridge *inputBridge, raw []byte) {
	tRecv := time.Now()

	var evt inputEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		serverLog.Add("%s bad input event JSON: %v", prefix, err)
		return
	}

	ack := inputAck{ID: evt.ID, TServerRecvMS: msEpoch(tRecv)}

	switch evt.Type {
	case "ping":
		// Pure data-channel round-trip baseline: acked immediately, no
		// bridge/CDP hop, so the viewer can isolate network RTT from
		// input-dispatch RTT.
		ack.OK = true
		ack.TDispatchedMS = ack.TServerRecvMS

	case "mousemove", "mousedown", "mouseup", "wheel":
		_, err := bridge.call("mouse", mouseParamsFromEvent(evt), 3*time.Second)
		ack.TDispatchedMS = msEpoch(time.Now())
		if err != nil {
			ack.OK = false
			ack.Error = err.Error()
			serverLog.Add("%s mouse dispatch failed id=%d type=%s: %v", prefix, evt.ID, evt.Type, err)
		} else {
			ack.OK = true
		}

	case "keydown", "keyup":
		_, err := bridge.call("key", keyParamsFromEvent(evt), 3*time.Second)
		ack.TDispatchedMS = msEpoch(time.Now())
		if err != nil {
			ack.OK = false
			ack.Error = err.Error()
			serverLog.Add("%s key dispatch failed id=%d type=%s key=%q: %v", prefix, evt.ID, evt.Type, evt.Key, err)
		} else {
			ack.OK = true
		}

	default:
		ack.OK = false
		ack.Error = "unknown event type: " + evt.Type
		ack.TDispatchedMS = ack.TServerRecvMS
		serverLog.Add("%s unknown input event type %q (id=%d)", prefix, evt.Type, evt.ID)
	}

	data, err := json.Marshal(ack)
	if err != nil {
		serverLog.Add("%s failed to marshal ack: %v", prefix, err)
		return
	}
	if err := dc.SendText(string(data)); err != nil {
		serverLog.Add("%s failed to send ack (id=%d): %v", prefix, evt.ID, err)
	}
}

// mouseParamsFromEvent builds the params object forwarded to run.js's
// dispatchMouse(); run.js owns the actual CDP field names/types
// (Input.dispatchMouseEvent) since it holds the pipe transport.
func mouseParamsFromEvent(e inputEvent) map[string]any {
	return map[string]any{
		"type":   e.Type,
		"x":      e.X,
		"y":      e.Y,
		"button": e.Button,
		"deltaX": e.DeltaX,
		"deltaY": e.DeltaY,
	}
}

func keyParamsFromEvent(e inputEvent) map[string]any {
	return map[string]any{
		"type":    e.Type,
		"key":     e.Key,
		"code":    e.Code,
		"keyCode": e.KeyCode,
	}
}
