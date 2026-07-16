// Package encoderpage embeds the self-contained WebCodecs encoder page
// served to the headful managed Chrome instance for live-browser video
// streaming (ADR-044; docs/internal/specs/live-browser-video-streaming-spec.md
// R7, Wave 1 component G — FR-001, FR-011, FR-016, FR-023).
//
// The page (encoder.html) is a single static file with all JS/CSS inlined:
// no remote fetch, no external <script src>/<link href>, no CDN, no fonts.
// It is non-navigable and MUST be served only on the gateway's loopback-only,
// unguessable-origin listener for capture ingest (FR-016 MAJ-003) — this
// package supplies the bytes and a plain http.Handler; it does NOT bind any
// listener or enforce loopback/origin policy itself, which is the ingest
// endpoint's responsibility (pkg/gateway/browser_ingest.go, Wave 1 component
// E) per the plan's division of file-disjoint components.
//
// The embedded bytes carry no secret. The per-stream ingest capability
// token is injected into the live page at runtime by the gateway via CDP
// Page.addScriptToEvaluateOnNewDocument, setting
// window.__OMNIPUS_INGEST__ = {token, wsURL, streamId, ...} BEFORE this
// page's own <script> runs (FR-013) — never baked into these bytes, never
// passed via URL.
package encoderpage

import (
	_ "embed"
	"net/http"
)

// encoderHTML is the embedded encoder page: static HTML with inlined
// JS/CSS, no external assets. See encoder.html's header comment for the
// full runtime contract and the exact upstream/downstream wire byte
// layouts (browser_frame_feed, browser_ingest_chunk).
//
//go:embed encoder.html
var encoderHTML []byte

// ContentType is the MIME type EncoderHTML's bytes MUST be served with.
const ContentType = "text/html; charset=utf-8"

// EncoderHTML returns the embedded encoder page bytes.
//
// The returned slice aliases the embedded data — callers MUST NOT mutate
// it.
func EncoderHTML() []byte {
	return encoderHTML
}

// Handler returns an http.Handler that serves the embedded encoder page
// for GET/HEAD requests and rejects everything else. It sets Cache-Control:
// no-store (this page must never be cached — it exists once per stream,
// on an unguessable per-stream origin/path minted fresh on every launch)
// and X-Content-Type-Options: nosniff.
//
// The caller (the ingest/stream-orchestration component) is responsible for
// mounting this ONLY on the loopback-only, unguessable per-stream origin
// described by FR-016 — this handler has no knowledge of, and does not
// enforce, that binding.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", ContentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(encoderHTML)
		}
	})
}
