// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"mime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertInlineDisposition asserts that a Content-Disposition header names the
// INLINE disposition, and — when wantFilename is non-empty — that it carries
// that filename.
//
// It exists because five separate tests spelled this as
// `assert.Equal(t, "inline", …)`, an exact match on the whole header. That is
// wrong for a reason worth writing down: an inline response legitimately
// carries the filename too (`inline; filename="report.html"`), which is what
// the browser offers when the reader saves from an inline view, and what
// serveMedia sent before the routes were unified onto one helper. When the
// helper briefly dropped it, only tests/integration/media_store_swap_test.go
// noticed; when it was restored, all five of these went red at once for the
// opposite reason. Neither direction was a real defect in the header.
//
// Parsing the media type instead of comparing the string means the assertion
// says what it means — "this is inline" — and stops caring how the value is
// spelled. Use this rather than an equality check on the raw header.
func assertInlineDisposition(t *testing.T, header, wantFilename string, msgAndArgs ...any) {
	t.Helper()

	got, params, err := mime.ParseMediaType(header)
	require.NoErrorf(t, err,
		"Content-Disposition %q must be a well-formed RFC 6266 value", header)
	assert.Equal(t, "inline", got, msgAndArgs...)

	if wantFilename != "" {
		assert.Equal(t, wantFilename, params["filename"],
			"an inline response keeps the filename — it is what the browser "+
				"offers when the reader saves from an inline view")
	}
}
