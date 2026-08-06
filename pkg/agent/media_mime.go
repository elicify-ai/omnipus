// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "strings"

// ADR-051 Rev 4 Gap 4 — non-universal image MIME guard.
//
// Vision providers are uneven in which image/* data URLs they accept:
// Gemini, GPT-4V and Claude 3.x all reject image/svg+xml blocks; the
// pure-Go decoder set has no entries for AVIF/HEIC/HEIF/ICO. Sending a
// tool-result message that carries one of these MIMEs via a raw data URL
// causes a hard provider error on the current turn AND leaks the bad URL
// into every subsequent turn (the message is persisted to session
// history). The fix is to normalize these MIMEs BEFORE the data URL is
// built and attached to the tool message that survives in history.
//
// Inbound presentation (resolveMediaRefs in loop_media.go) already
// rasterizes SVG via oksvg/rasterx and degrades AVIF/HEIC/ICO through
// the offload + honest-marker path; the gap was the outbound tool-result
// attachment site at loop.go:8326-8339, which builds the data URL from
// meta.ContentType verbatim without consulting that policy.

// isNonUniversalImageMIME reports whether mime is one of the image MIME
// types that most vision providers reject when sent as a data URL, or
// that the pure-Go decoder set cannot normalize. The set is the union
// of:
//
//   - Raw SVG (provider side rejects image/svg+xml data URLs; we
//     rasterize to canonical PNG before attach — inbound FR-016 path).
//   - Unsupported-raster set: AVIF/HEIC/HEIF/ICO (no stdlib/x-image
//     decoder; decode fails, encodeImageToDataURL returns "", and the
//     caller falls back to "skip inline attach + keep the artifact tag").
//
// Plain PNG/JPEG/WebP/GIF/BMP/TIFF all stay outside this set, so the
// pre-existing "build data URL from meta.ContentType" path is unchanged
// for them — backward compatible (Constraint #6 / backward-compat).
func isNonUniversalImageMIME(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/svg+xml",
		"image/svg",
		"image/avif",
		"image/heic",
		"image/heif",
		"image/heif-sequence",
		"image/x-icon",
		"image/ico",
		"image/vnd.microsoft.icon":
		return true
	}
	return false
}
