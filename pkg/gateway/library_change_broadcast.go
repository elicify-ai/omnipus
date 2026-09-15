// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// D-107 — the library_changed frame, both halves of its short journey.
//
// THE DEFECT THIS CLOSES: two browser tabs never reconciled a Library
// listing. The tab that performed a write updated its own queries; the other
// tab kept serving its stale listing indefinitely — UAT measured a listing
// still showing files that had been gone for 17 minutes — and clicking one
// of those ghost rows opened a preview of a deleted file (the browser's own
// broken-image glyph, no error, no cure). The SPA now ALSO invalidates on
// window focus/visibility (the pull half), but a pull can only fire on an
// event the tab receives: two windows side by side never blur, never
// visibilitychange, never reconcile. This frame is the push half — the
// gateway TELLS every connected client that a workspace's tree changed.
//
// WHY SCOPE IS THE WORKSPACE, NOT THE FOLDER: computing "which listings
// changed" for a delete of `a/b/c.md` means knowing that `a/b`'s row for
// `c.md` is gone AND possibly `a`'s subfolder listing too — a set that is
// easy to get subtly wrong, and getting it wrong reintroduces exactly this
// defect for the folders that were missed. Clients invalidate every cached
// listing for the workspace; a few redundant refetches of unchanged folders
// is the cheap side of that trade.

package gateway

import (
	"encoding/json"
	"log/slog"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// broadcastLibraryChange fans one library_changed frame out to every
// connected WS client. Same construction + drop-log shape as
// broadcastAskUserCard (ws_ask_user.go), over the shared broadcastRaw helper:
// a client whose send buffer is full drops the frame (counted, never
// blocking) and recovers via the focus-invalidation pull half.
//
// Broadcast, not addressed: the single-user model has no per-account fanout,
// and the ORIGINATING tab receiving its own frame is harmless — its local
// invalidation already ran, and a second invalidate of fresh data is a no-op.
func (h *WSHandler) broadcastLibraryChange(f gen.LibraryChangedFrame) {
	f.Type = string(gen.WsFrameTypeLibraryChanged)
	data, err := json.Marshal(f)
	if err != nil {
		// Cannot happen for this shape (two strings and two optional
		// strings), but a marshal failure must never take the write path
		// down — the mutation already landed.
		slog.Error("ws: marshal library_changed frame failed", "error", err)
		return
	}
	h.broadcastRaw(data, "ws: library_changed frame dropped (send buffer full)")
}

// emitLibraryChange is the restAPI's side: called by the Library write
// handlers AFTER a mutation has landed (never on a refusal — the frame means
// "the tree changed", and a 4xx changed nothing). Nil-safe by design: the
// broadcaster is wired in at boot once the WS handler exists, and every
// restAPI constructed without it (tests, partial boots) must keep working.
func (a *restAPI) emitLibraryChange(workspaceID, path, reason string) {
	if workspaceID == "" {
		return
	}
	fn := a.libraryChangeBroadcast.Load()
	if fn == nil {
		return
	}
	f := gen.LibraryChangedFrame{WorkspaceId: workspaceID}
	if path != "" {
		p := path
		f.Path = &p
	}
	if reason != "" {
		r := reason
		f.Reason = &r
	}
	(*fn)(f)
}
