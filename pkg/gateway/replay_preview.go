// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"math"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Persisted keys and values of a preview_document tool-call record
// (office-preview spec §5.2): the record carries "presentation" and a
// "preview" map beside the usual "media" list.
const (
	previewPresentationPageThumbnails = "page_thumbnails"
	previewOutcomeUnavailable         = "unavailable"
)

// replayPreviewGroup reports whether a persisted tool call is a
// document-preview thumbnail group and, if so, returns its preview record
// (nil when the record is missing or incomplete — the frame then still
// replays as a group, without the record).
func replayPreviewGroup(tc session.ToolCall) (*generated.MediaPreview, bool) {
	if presentation, _ := tc.Result["presentation"].(string); presentation != previewPresentationPageThumbnails {
		return nil, false
	}
	raw, ok := tc.Result["preview"].(map[string]any)
	if !ok {
		return nil, true
	}
	preview := generated.MediaPreview{}
	preview.SourceName, _ = raw["source_name"].(string)
	preview.SourceVersion, _ = raw["source_version"].(string)
	preview.Outcome, _ = raw["outcome"].(string)
	var okCount, okFirst, okLast bool
	preview.PageCount, okCount = replayPositiveInt(raw["page_count"])
	preview.FirstPage, okFirst = replayPositiveInt(raw["first_page"])
	preview.LastPage, okLast = replayPositiveInt(raw["last_page"])
	if preview.SourceName == "" || preview.SourceVersion == "" || preview.Outcome == "" || !okCount || !okFirst || !okLast {
		return nil, true
	}
	if reason, _ := raw["unavailable_reason"].(string); reason != "" {
		preview.UnavailableReason = &reason
	}
	return &preview, true
}

// replayPositiveInt reads a persisted whole number >= 1. Transcripts decoded
// from JSON hold float64; records built in memory hold int.
func replayPositiveInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, n >= 1
	case int64:
		if n < 1 || n > math.MaxInt32 {
			return 0, false
		}
		return int(n), true
	case float64:
		if n < 1 || n > math.MaxInt32 || n != math.Trunc(n) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}
