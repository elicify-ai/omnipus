// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

// capture_session_assertions.go exists solely to host the compile-time
// interface assertions below. (Historical note: this file was
// capture_session_notlite.go, //go:build !lite, until ADR-067 §10 step 14
// retired the `lite` build variant — the stub *webrtc.Session it used to
// guard against no longer exists, so the assertions now apply to every
// build.) They cannot live in pkg/tools/browser/webrtc/session.go:
// viewerOfferHandler and viewerRemover are unexported interfaces defined in
// package browser (capture_session.go), and package webrtc must not import
// package browser at all (browser already imports webrtc — an import the
// other way would be a cycle).

import "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"

// GAP 3 fix-wave finding: both viewerOfferHandler and viewerRemover
// (capture_session.go) are detected via a runtime type assertion in
// newCaptureSessionWithDeps, deliberately, so narrower test fakes don't need
// matching methods. But that means a future signature drift on either
// interface, or on *webrtc.Session's own HandleViewerOfferHandle/
// CloseViewerIfCurrent/SetOnViewerRemoved methods, would silently downgrade
// production to the UNSAFE, non-identity-checked fallback paths
// (CleanupViewerOffer's viewerID-only branch, the plain unconditional
// RemoveViewer that used to be wired as the eviction callback) with ZERO
// compiler signal — exactly the class of bug the fix-wave's CRITICAL finding
// (CleanupViewerOffer's early-failure gap) and GAP 1/GAP 2 both closed.
// These assertions turn that drift into a build failure instead.
var (
	_ viewerOfferHandler = (*webrtc.Session)(nil)
	_ viewerRemover      = (*webrtc.Session)(nil)
)
