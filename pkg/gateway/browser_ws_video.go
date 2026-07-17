// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_ws_video.go — Wave 2 component M (live-browser video streaming,
// ADR-044; docs/internal/specs/live-browser-video-streaming-spec.md R7). This
// file is the adapter that makes one live-browser WS viewer connection
// (*browserWSConn, component F) satisfy the stream relay's Viewer interface
// (browser.Viewer, component D) so encoded video/audio chunks fan out to the
// viewer as binary WS frames.
//
// It lives in pkg/gateway (not pkg/tools/browser) on purpose: the relay's
// Viewer interface is deliberately dependency-free so the browser package never
// imports the gateway; the concrete adapter that reaches into browserWSConn's
// unexported send path therefore has to live here, in the gateway package.

package gateway

import (
	"crypto/subtle"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// sendChunkTracked enqueues an encoded binary media chunk (video/audio,
// FR-002/017) as a WS Binary frame — the binary analog of sendFrame, with the
// identical drop-on-full, latest-wins discipline (FR-004: "a full-queue
// viewer MUST have its chunks dropped in isolation"; a stale encoded chunk is
// superseded by the next one, same rationale as the JPEG-era screencast
// frame) — but REPORTS whether the chunk was actually enqueued (true) or
// dropped because the outbound queue was full / the connection is closing
// (false). The relay needs that signal to detect an isolated slow-viewer
// drop and replay the full cached GOP on this viewer's next delivery (DS-3
// row 3, SF-H2) instead of resuming on a delta it has a decode gap before.
//
// This is the SOLE production send path for relayed video/audio chunks
// (S-F1, review-round-2 cleanup): an earlier, non-tracked
// browserWSConn.sendChunk with no production caller — this method already
// re-implemented its exact non-blocking select, just with a bool return —
// was removed.
func (c *browserWSConn) sendChunkTracked(data []byte) bool {
	select {
	case c.sendCh <- &wsSendItem{Binary: true, Data: data}:
		return true
	case <-c.doneCh:
		return false
	default:
		// Queue full → drop in isolation (a stale encoded chunk is superseded
		// by the next one). Reported as not-enqueued so the relay can recover
		// this viewer with a keyframe.
		return false
	}
}

// wsVideoViewer adapts one *browserWSConn to browser.Viewer for exactly one
// stream. It is created by the stream orchestrator only AFTER the WS-layer
// bearer authentication (BrowserWSHandler.authenticate) and the agent-manager
// resolution have already succeeded — the connection's very existence is the
// primary authorization; this adapter's Authorized adds the per-stream SCOPE
// gate (FR-015): a viewer may only pull the one stream it was authorized for.
type wsVideoViewer struct {
	wc *browserWSConn
	// authorizedStreamID is the single stream this viewer was authorized to
	// attach to. Authorized returns true only for this exact id — a viewer
	// object can never be replayed against a different stream.
	authorizedStreamID string
	// sessionID / viewerID are echoed on outgoing status frames + used for the
	// drop-context log identifier (parity with the rest of browser_ws.go).
	sessionID string
	viewerID  string
}

// newWSVideoViewer builds the adapter bound to authorizedStreamID.
func newWSVideoViewer(
	wc *browserWSConn,
	authorizedStreamID, sessionID, viewerID string,
) *wsVideoViewer {
	return &wsVideoViewer{
		wc:                 wc,
		authorizedStreamID: authorizedStreamID,
		sessionID:          sessionID,
		viewerID:           viewerID,
	}
}

// SendChunk enqueues one already-wire-encoded chunk (browser.EncodeChunk
// output) as a binary WS frame, returning true only when actually enqueued
// (FR-004). Never blocks.
func (v *wsVideoViewer) SendChunk(chunk []byte) bool {
	return v.wc.sendChunkTracked(chunk)
}

// Authorized reports whether this viewer may attach to streamID. It enforces
// the per-stream scope gate (FR-015): only the exact stream this viewer was
// authorized for passes. Constant-time comparison is defense-in-depth so the
// unguessable stream key is never leaked through a timing side channel.
func (v *wsVideoViewer) Authorized(streamID string) bool {
	return subtle.ConstantTimeCompare([]byte(streamID), []byte(v.authorizedStreamID)) == 1
}

// Failed notifies the viewer that its stream's capture failed/closed so the SPA
// moves to the unavailable state (US-5) — never a frozen last frame. Signaled
// as a browser_status(error) frame carrying the generic, install-agnostic
// message (O-3): the specific cause is logged operator-side, never surfaced to
// the end user.
//
// SF-L5: the relay's Viewer contract (stream_relay.go) requires Failed to be
// non-blocking — MarkFailed invokes it synchronously, under the agent gate
// (via failStreamLocked), for every attached viewer of a failed stream.
// sendCriticalGen's underlying sendCritical can block up to 2s on a
// backed-up connection before giving up and dropping; calling it
// synchronously here would stall the agent gate — and therefore every OTHER
// concurrent operation serialized behind it (a fresh attach, StepDown,
// another stream's relaunch) — for up to that same 2s per slow viewer. Hand
// off to a dedicated goroutine instead so Failed() itself always returns
// immediately, matching the contract's actual, documented non-blocking
// bound rather than the 2s one it previously carried in practice.
func (v *wsVideoViewer) Failed(streamID string) {
	go func() {
		msg := browserVideoUnavailableMessage
		sid := v.sessionID
		v.wc.sendCriticalGen(generated.BrowserStatusFrame{
			Type:      string(generated.WsFrameTypeBrowserStatus),
			State:     "error",
			Message:   &msg,
			SessionId: &sid,
		}, dropContext(v.sessionID, v.viewerID, "video-stream-failed"))
	}()
}

// compile-time assertion: wsVideoViewer satisfies the relay's Viewer contract.
var _ browser.Viewer = (*wsVideoViewer)(nil)
