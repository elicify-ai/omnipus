// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_tool_media_test.go — ADR-051 Rev 4 Gap 4 regression tests.
//
// The outbound tool-result media attach site (loop.go:8326-8339) used to
// build a data:<mime>;base64,... URL from meta.ContentType verbatim and
// append it to the tool message that is then persisted to session
// history (loop.go:8442-8443) and replayed on every subsequent turn.
// For an SVG the data URL was image/svg+xml;base64,... — Gemini, GPT-4V
// and Claude 3.x all reject that block with HTTP 400, so the bad URL
// poisoned every turn after the send_file that produced it. The fix
// (attachToolResultMedia) gates a fixed set of non-universal image MIMEs
// and rasterizes SVG to PNG via the existing pure-Go oksvg/rasterx
// path before attach.
//
// These tests assert the fixed contract at the helper boundary: SVG
// must NOT surface as image/svg+xml; PNG must still attach verbatim.
// The integration through the full runTurn is covered by the UAT plan
// (ADR-051 Rev 4 / Gap 4 evidence) — the helper-level tests here pin
// the wire-format invariant.

package agent

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// toolResultMediaBlue64x64SVG is the fixture SVG used by the Gap 4
// regression test: a 64×64 blue circle. Same shape as svg_raster_test.go's
// circleSVG, sized to the spec's 64×64 to keep the rasterized PNG byte
// budget bounded for the unit test (encodeSVGToDataURL enforces the
// same maxSize cap as the production path).
const toolResultMediaBlue64x64SVG = `<svg xmlns="http://www.w3.org/2000/svg"` +
	` width="64" height="64">` +
	`<circle cx="32" cy="32" r="28" fill="blue"/></svg>`

// toolResultMediaMaxSize is the byte budget attachToolResultMedia uses
// in the test. 1 MiB is the production default (cfg.Agents.Defaults.
// GetMaxMediaSize) and is comfortably larger than the 64×64 SVG and
// the 1×1 PNG fixtures; any regression that drops the byte budget guard
// would still pass the small-fixture assertions.
const toolResultMediaMaxSize = 1 << 20

// TestToolResultMedia_NonUniversalImage_NoDataURLInHistory (Gap 4 spec test
// #33 / SC-003, ADR-051 Rev 4): when a tool produces both an SVG and a
// PNG, the data URL attached to the tool message — and therefore
// persisted into session history — must NEVER be image/svg+xml; the SVG
// is rasterized to PNG instead. The PNG sibling in the same tool result
// must still attach as image/png (regression guard against
// over-suppression).
func TestToolResultMedia_NonUniversalImage_NoDataURLInHistory(t *testing.T) {
	store := media.NewFileMediaStore()

	svgRef, _ := storeFile(t, store, "blue.svg", "image/svg+xml", []byte(toolResultMediaBlue64x64SVG))
	pngRef, _ := storeFile(t, store, "blue.png", "image/png", realPNGBytes())

	msg := &providers.Message{Role: "tool", Content: "tool output", ToolCallID: "call_1"}
	attachToolResultMedia(msg, []string{svgRef, pngRef}, store, toolResultMediaMaxSize)

	require.Len(t, msg.Media, 2,
		"both refs must produce a data URL — the SVG must be rasterized, not dropped")

	for _, url := range msg.Media {
		assert.False(t, strings.HasPrefix(url, "data:image/svg+xml"),
			"Gap 4: SVG must NEVER surface as image/svg+xml data URL in history; got %q",
			url[:min(64, len(url))])
		assert.False(t, strings.HasPrefix(url, "data:image/svg "),
			"Gap 4: SVG mime alias must NEVER surface as image/svg data URL in history")
	}

	// At least one of the two URLs MUST be a PNG data URL: either the
	// rasterized SVG, the verbatim PNG, or both. The SVG rasterizer
	// produces PNG via oksvg/rasterx, and the PNG fixture is its own
	// data:image/png;base64,... payload.
	hasPNG := false
	for _, url := range msg.Media {
		if strings.HasPrefix(url, "data:image/png;base64,") {
			hasPNG = true
			break
		}
	}
	assert.True(t, hasPNG,
		"Gap 4: at least one of the data URLs must be image/png (rasterized SVG or PNG sibling); got %d urls",
		len(msg.Media))

	// Decode the first PNG data URL and assert it is a real, valid PNG.
	// Catches a regression where the prefix is right but the body is not
	// actually base64-encoded PNG (e.g. raw SVG bytes slipped through).
	var pngURL string
	for _, url := range msg.Media {
		if strings.HasPrefix(url, "data:image/png;base64,") {
			pngURL = url
			break
		}
	}
	require.NotEmpty(t, pngURL, "must have found an image/png data URL")
	payload, decErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(pngURL, "data:image/png;base64,"))
	require.NoError(t, decErr, "PNG data URL body must be valid base64")
	// PNG magic header: 89 50 4E 47 0D 0A 1A 0A
	require.GreaterOrEqual(t, len(payload), 8, "PNG payload must be at least 8 bytes")
	assert.Equal(t, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, payload[:8],
		"PNG data URL body must start with the PNG magic header")
}

// TestToolResultMedia_PlainPNG_StillAttached: regression guard for the
// over-suppression risk — a plain PNG must still attach as a data URL
// (the pre-fix behavior). If attachToolResultMedia gated plain PNG by
// mistake, the model would lose its vision input.
func TestToolResultMedia_PlainPNG_StillAttached(t *testing.T) {
	store := media.NewFileMediaStore()
	ref, _ := storeFile(t, store, "plain.png", "image/png", realPNGBytes())

	msg := &providers.Message{Role: "tool", Content: "tool output", ToolCallID: "call_1"}
	attachToolResultMedia(msg, []string{ref}, store, toolResultMediaMaxSize)

	require.Len(t, msg.Media, 1, "plain PNG must still attach as a data URL")
	assert.True(t, strings.HasPrefix(msg.Media[0], "data:image/png;base64,"),
		"plain PNG must attach as image/png data URL; got prefix %q",
		msg.Media[0][:min(64, len(msg.Media[0]))])

	// Body must decode and round-trip to the original bytes (the path is
	// "build verbatim data URL", not "rasterize").
	payload, decErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(msg.Media[0], "data:image/png;base64,"))
	require.NoError(t, decErr)
	assert.Equal(t, realPNGBytes(), payload,
		"plain PNG data URL body must be byte-identical to the source PNG")
}

// TestToolResultMedia_AVIFSkipsInlineAttach keeps the AVIF/HEIC/ICO half
// of the contract tight: the inline attach is skipped (encodeImageToDataURL
// returns "" for those formats — no pure-Go decoder), the artifact tag is
// the model's only hook to the file path, and crucially no poison
// data:image/avif URL leaks into session history.
func TestToolResultMedia_AVIFSkipsInlineAttach(t *testing.T) {
	for _, mime := range []string{"image/avif", "image/heic", "image/heif", "image/x-icon"} {
		t.Run(mime, func(t *testing.T) {
			store := media.NewFileMediaStore()
			ref, _ := storeFile(t, store,
				"photo"+strings.ReplaceAll(mime, "/", "_"),
				mime,
				[]byte("unsupported-fake-bytes"))

			msg := &providers.Message{Role: "tool", Content: "tool output", ToolCallID: "call_1"}
			attachToolResultMedia(msg, []string{ref}, store, toolResultMediaMaxSize)

			assert.Empty(t, msg.Media,
				"%s has no pure-Go decoder — inline attach must be skipped, not poisoned into history",
				mime)
		})
	}
}

// TestIsNonUniversalImageMIME pins the membership table. If anyone adds a
// new MIME to the set the test must be updated — but a stray change
// that drops an existing entry would break a downstream provider
// contract, so we assert the full set.
func TestIsNonUniversalImageMIME(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		// Non-universal: every entry must be guarded.
		{"image/svg+xml", true},
		{"image/svg", true},
		{"image/avif", true},
		{"image/heic", true},
		{"image/heif", true},
		{"image/heif-sequence", true},
		{"image/x-icon", true},
		{"image/ico", true},
		{"image/vnd.microsoft.icon", true},
		// Case-insensitive — operators occasionally upper-case the suffix.
		{"IMAGE/SVG+XML", true},
		// Universal: must NOT be gated.
		{"image/png", false},
		{"image/jpeg", false},
		{"image/webp", false},
		{"image/gif", false},
		{"image/bmp", false},
		{"image/tiff", false},
		// Non-image MIME: not the function's concern but must be safe.
		{"application/pdf", false},
		{"audio/ogg", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			assert.Equal(t, tc.want, isNonUniversalImageMIME(tc.mime),
				"isNonUniversalImageMIME(%q)", tc.mime)
		})
	}
}
