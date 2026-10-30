// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Office-document preview — replay/thumbnail-group projection (office-preview
// spec §5.2, FR-035; TDD rows T-46 contract leg, T-62 replay leg; BDD
// B-11/B-12/B-40).
//
// Oracles from the spec (§5.2), never from the code:
//   - MediaFrame gains tool_call_id, presentation ("inline" default /
//     "page_thumbnails") and a preview record {source_name, source_version
//     (12 hex), page_count, first_page, last_page, outcome complete/partial/
//     unavailable, unavailable_reason renderer_missing/renderer_damaged/
//     renderer_failed/storage_failed}.
//   - MediaPart gains page_number (>= 1); caption stays display text.
//   - "same projection from live WebSocket, replay and REST history";
//     parts may be empty ONLY with presentation page_thumbnails and outcome
//     unavailable (server-guaranteed; T-62 asserts).
//   - B-40: two identical previews plus an interleaved screenshot must
//     reload as two separate thumbnail groups plus an inline screenshot.
//
// RED-proposed persisted record shape (reconciled with backend-lead's
// storage at GREEN — qa-lead A owns the reconciliation): the persisted
// tool-call record's "media" items gain "page_number" and "caption" keys,
// and the record gains "presentation" and "preview" keys carrying the §5.2
// values. The generated wire types already landed (contract commit
// 78bdf3a3e); buildMediaFrame::buildMediaFrame populates none of them
// today (verified by reading replay.go before writing this test).
//
// The live SendMedia leg and the POST retry route need new bus/REST fields
// and preview-record persistence — they are in the parked RED tier
// (compile-red until those symbols land), not here.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// previewToolCall builds a persisted preview_document tool-call record in
// the RED-proposed shape.
func previewToolCall(id session.ToolCallID, sourceName, version string, pageCount, first, last int, outcome string, parts []map[string]any) session.ToolCall {
	media := make([]any, 0, len(parts))
	for _, p := range parts {
		media = append(media, map[string]any{
			"ref":          p["ref"],
			"filename":     p["filename"],
			"content_type": p["content_type"],
			"type":         p["type"],
			"page_number":  p["page_number"],
			"caption":      p["caption"],
		})
	}
	result := map[string]any{
		"media":        media,
		"presentation": "page_thumbnails",
		"preview": map[string]any{
			"source_name":  sourceName,
			"source_version": version,
			"page_count":   pageCount,
			"first_page":   first,
			"last_page":    last,
			"outcome":      outcome,
		},
	}
	return session.ToolCall{ID: id, Tool: "preview_document", Status: "success", Result: result}
}

func thumbPart(ref string, page int) map[string]any {
	return map[string]any{
		"ref":          ref,
		"filename":     "report.docx",
		"content_type": "image/png",
		"type":         "image",
		"page_number":  page,
		"caption":      fmt.Sprintf("report.docx · page %d of 12", page),
	}
}

// TestOfficePreview_ReplayProjection_GroupAndPreviewRecord (§5.2, T-62):
// a replayed preview_document tool call emits a MediaFrame with the tool
// call id, page_thumbnails presentation, the preview record, and parts in
// page order carrying page_number and caption.
func TestOfficePreview_ReplayProjection_GroupAndPreviewRecord(t *testing.T) {
	tc := previewToolCall("call-prev-1", "report.docx", "a1b2c3d4e5f6", 12, 1, 4, "complete",
		[]map[string]any{thumbPart("media://page1", 1), thumbPart("media://page2", 2), thumbPart("media://page3", 3), thumbPart("media://page4", 4)})

	frame, ok := buildMediaFrame("session-1", tc, nil, nil)
	if !ok {
		t.Fatalf("BLOCKED: replay dropped a preview_document media record entirely (T-62, B-11)")
	}
	if frame.ToolCallId == nil || *frame.ToolCallId != "call-prev-1" {
		t.Fatalf("BLOCKED: MediaFrame.tool_call_id not projected from the tool call (got %v) — spec §5.2 requires it as the grouping key (T-46, T-62)", frame.ToolCallId)
	}
	if frame.Presentation == nil || *frame.Presentation != "page_thumbnails" {
		t.Fatalf("BLOCKED: MediaFrame.presentation = %v, want page_thumbnails — spec §5.2 (T-46, T-62)", frame.Presentation)
	}
	if frame.Preview == nil {
		t.Fatalf("BLOCKED: MediaFrame.preview absent — spec §5.2 requires the preview record (T-46, T-62)")
	}
	wantPreview := map[string]string{
		"SourceName": "report.docx", "SourceVersion": "a1b2c3d4e5f6",
		"Outcome": "complete",
	}
	if frame.Preview.SourceName != wantPreview["SourceName"] || frame.Preview.SourceVersion != wantPreview["SourceVersion"] || frame.Preview.Outcome != wantPreview["Outcome"] {
		t.Fatalf("preview record mismatch: %+v, want %v", *frame.Preview, wantPreview)
	}
	if frame.Preview.PageCount != 12 || frame.Preview.FirstPage != 1 || frame.Preview.LastPage != 4 {
		t.Fatalf("preview record page fields wrong: %+v, want page_count=12 first=1 last=4 (spec §5.2)", *frame.Preview)
	}
	if len(frame.Parts) != 4 {
		t.Fatalf("parts = %d, want 4 (one per stored page)", len(frame.Parts))
	}
	for i, part := range frame.Parts {
		page := i + 1
		if part.PageNumber == nil || *part.PageNumber != page {
			t.Fatalf("part %d page_number = %v, want %d — parts travel in page order (spec §5.2)", i, part.PageNumber, page)
		}
		if part.Caption == nil || *part.Caption == "" {
			t.Fatalf("part %d caption dropped — §5.2: replay must persist the caption (today's buildMediaFrame drops it)", i)
		}
	}
}

// TestOfficePreview_ReplayProjection_UnavailableFrame (§5.2 outcome
// unavailable, B-34): an unavailable preview replays as a frame with NO
// parts, presentation page_thumbnails and the unavailable reason — the
// persistent "page previews unavailable" card rebuilds from replay without
// rendering.
func TestOfficePreview_ReplayProjection_UnavailableFrame(t *testing.T) {
	tc := previewToolCall("call-prev-2", "report.docx", "a1b2c3d4e5f6", 12, 1, 4, "unavailable", nil)
	frame, ok := buildMediaFrame("session-1", tc, nil, nil)
	if !ok {
		t.Fatalf("BLOCKED: replay dropped the unavailable preview frame — the person must still see the unavailable card after reload (T-62, B-12, B-34)")
	}
	if len(frame.Parts) != 0 {
		t.Fatalf("unavailable preview frame must carry no parts, got %d", len(frame.Parts))
	}
	if frame.Preview == nil || frame.Preview.Outcome != "unavailable" {
		t.Fatalf("preview record outcome = %v, want unavailable (spec §5.2)", frame.Preview)
	}
	if frame.Preview.UnavailableReason == nil || *frame.Preview.UnavailableReason == "" {
		t.Fatalf("unavailable preview must carry unavailable_reason (spec §5.2 enum: renderer_missing/renderer_damaged/renderer_failed/storage_failed)")
	}
}

// TestOfficePreview_ReplayProjection_TwoIdenticalPreviewsStaySeparate
// (B-40): two identical previews plus an interleaved screenshot reload as
// two separate thumbnail groups plus an inline screenshot — the replay
// dedup key becomes (message, tool_call_id) per §5.2, not the underlying
// file path.
func TestOfficePreview_ReplayProjection_TwoIdenticalPreviewsStaySeparate(t *testing.T) {
	// A real store: today's dedup key is the resolved file path, so the two
	// identical preview groups must share the same underlying files for the
	// collision to be real.
	store := media.NewFileMediaStore()
	page1 := filepath.Join(t.TempDir(), "page-1.png")
	page2 := filepath.Join(t.TempDir(), "page-2.png")
	shotFile := filepath.Join(t.TempDir(), "shot.png")
	for _, f := range []string{page1, page2, shotFile} {
		if err := os.WriteFile(f, []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refPage1, err := store.Store(page1, media.MediaMeta{Filename: "page-1.png", ContentType: "image/png"}, "preview-red-scope")
	if err != nil {
		t.Fatal(err)
	}
	refPage2, err := store.Store(page2, media.MediaMeta{Filename: "page-2.png", ContentType: "image/png"}, "preview-red-scope")
	if err != nil {
		t.Fatal(err)
	}
	refShot, err := store.Store(shotFile, media.MediaMeta{Filename: "shot.png", ContentType: "image/png"}, "preview-red-scope")
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]struct{}{}
	tc1 := previewToolCall("call-prev-a", "report.docx", "a1b2c3d4e5f6", 2, 1, 2, "complete",
		[]map[string]any{thumbPart(refPage1, 1), thumbPart(refPage2, 2)})
	shot := session.ToolCall{ID: "call-shot", Tool: "browser_screenshot", Status: "success", Result: map[string]any{
		"media": []any{map[string]any{"ref": refShot, "filename": "shot.png", "content_type": "image/png", "type": "image"}},
	}}
	tc2 := previewToolCall("call-prev-b", "report.docx", "a1b2c3d4e5f6", 2, 1, 2, "complete",
		[]map[string]any{thumbPart(refPage1, 1), thumbPart(refPage2, 2)})

	frames := 0
	for _, tc := range []session.ToolCall{tc1, shot, tc2} {
		if frame, ok := buildMediaFrame("session-1", tc, store, seen); ok {
			frames++
			if tc.Tool == "preview_document" && len(frame.Parts) == 0 && (frame.Preview == nil || frame.Preview.Outcome != "unavailable") {
				t.Fatalf("preview group %q collapsed to zero parts by path dedup — B-40 requires two separate groups (§5.2 dedup key = tool_call_id)", tc.ID)
			}
		}
	}
	if frames != 3 {
		t.Fatalf("BLOCKED: replay produced %d media frames, want 3 (two preview groups + one screenshot) — B-40 (T-62)", frames)
	}
}
