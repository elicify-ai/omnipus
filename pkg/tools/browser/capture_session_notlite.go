// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !lite

package browser

// capture_session_notlite.go exists solely to host the compile-time
// interface assertions below. It cannot live in capture_session.go itself
// (that file is deliberately build-tag-neutral, compiling into the lite
// build too — see its own file doc comment) because the lite build's stub
// *webrtc.Session (webrtc/stub.go) implements NEITHER capability being
// asserted here; a bare `var _ viewerOfferHandler = (*webrtc.Session)(nil)`
// in a neutral file would fail to compile under `-tags lite`. It cannot live
// in pkg/tools/browser/webrtc/session.go either: viewerOfferHandler and
// viewerRemover are unexported interfaces defined in package browser
// (capture_session.go), and package webrtc must not import package browser
// at all (browser already imports webrtc — an import the other way would be
// a cycle).

import "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"

// GAP 3 fix-wave finding: both viewerOfferHandler and viewerRemover
// (capture_session.go) are detected via a runtime type assertion in
// newCaptureSessionWithDeps, deliberately, so the lite build's stub Session
// (implements neither) and narrower test fakes don't need matching methods.
// But that means a future signature drift on either interface, or on
// *webrtc.Session's own HandleViewerOfferHandle/CloseViewerIfCurrent/
// SetOnViewerRemoved methods, would silently downgrade production to the
// UNSAFE, non-identity-checked fallback paths (CleanupViewerOffer's
// viewerID-only branch, the plain unconditional RemoveViewer that used to be
// wired as the eviction callback) with ZERO compiler signal — exactly the
// class of bug the fix-wave's CRITICAL finding (CleanupViewerOffer's
// early-failure gap) and GAP 1/GAP 2 both closed. These assertions turn that
// drift into a build failure instead, on every !lite build (which is every
// build that actually links the real Pion-backed relay).
var (
	_ viewerOfferHandler = (*webrtc.Session)(nil)
	_ viewerRemover      = (*webrtc.Session)(nil)
)
