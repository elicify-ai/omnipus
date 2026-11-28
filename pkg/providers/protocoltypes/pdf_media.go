package protocoltypes

// pdf_media.go — the shared PDF data-URL rule of the four wire formats
// (issue #1968, spec FR-18). Every transport sends a PDF data URL inline as
// a document with its exact bytes; a malformed one becomes the visible
// PDFMalformedNote text instead of being dropped or forwarded. No transport
// applies a numeric limit: limits come from config.json pdf_limits
// (config.Config.ResolvePDFLimits), and the agent applies them before the
// request is built.

import (
	"encoding/base64"
	"strings"
)

// PDFDataURLPrefix is the prefix of every native PDF media entry.
const PDFDataURLPrefix = "data:application/pdf;base64,"

// PDFMalformedNote is the visible text a transport sends in place of a PDF
// data URL whose payload is not valid base64 (FR-18).
const PDFMalformedNote = "[PDF omitted: malformed data]"

// IsPDFDataURL reports whether a media entry is a PDF data URL (valid or
// not); it is how transports tell PDFs apart from images.
func IsPDFDataURL(mediaURL string) bool {
	return strings.HasPrefix(mediaURL, "data:application/pdf;")
}

// DecodePDFDataURL returns the base64 payload and the decoded bytes of a
// PDF data URL. ok is false when the entry is not a well-formed base64 PDF
// data URL with a non-empty, decodable payload.
func DecodePDFDataURL(mediaURL string) (payload string, data []byte, ok bool) {
	if !strings.HasPrefix(mediaURL, PDFDataURLPrefix) {
		return "", nil, false
	}
	payload = strings.TrimPrefix(mediaURL, PDFDataURLPrefix)
	if payload == "" {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 {
		return "", nil, false
	}
	return payload, data, true
}
