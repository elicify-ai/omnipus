//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

const (
	gatefixCurrentTurn     = "GATEFIX_CURRENT_TURN"
	gatefixNotResentMarker = "[pages not re-sent; call preview_document again]"
	gatefixExtractedOnFull = "GATEFIX_EXTRACTED_WHEN_UPLOADS_FILL"
	gatefixPreviewWhole    = "GATEFIX_PREVIEW_IN_FULL"
	gatefixEarlierPDF      = "GATEFIX_EARLIER_TURN_PDF"
	gatefixCountedNewPDF   = "GATEFIX_NEW_PDF_COUNTED"
	gatefixCountFallback   = "GATEFIX_COUNT_FALLBACK_TEXT"

	// red3 boundary and fallback sentinels (delta CHECK F2–F5).
	gatefixBoundaryPreviewText  = "GATEFIX_BOUNDARY_PREVIEW_TEXT"
	gatefixLedgerPreviewText    = "GATEFIX_LEDGER_PREVIEW_TEXT"
	gatefixEmptyTextRecovered   = "GATEFIX_EMPTY_TEXT_RECOVERED"
	gatefixPagesNoFitStoredText = "GATEFIX_PAGES_NO_FIT_STORED_TEXT"
)

// (a) Earlier-turn screenshots, even past 32 MiB, do not spend this request's
// preview budget. The new preview_document still reaches the model in full.
func TestOfficePreview_EarlierTurnScreenshotsLeaveTheNewPreviewWhole(t *testing.T) {
	shot := gatefixDataURL("image/png", bytesRepeat(previewRequestBudget+1, 0x11))
	const previewID = "gatefix-preview-after-shots"
	pdf := []byte(gatefixPreviewWhole)
	messages := []providers.Message{
		{Role: "user", Content: "earlier turn: the page", Media: []string{shot}},
		{Role: "assistant", Content: "I saw the earlier screenshot"},
		{Role: "user", Content: gatefixCurrentTurn},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	gatefixRequireShotsBeforeCurrentTurn(t, messages, shot)
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF: pdf, Text: "not-the-full-form", Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
	})
	require.NoError(t, err)
	tool := gatefixToolMessage(t, got, previewID)
	require.NotContains(t, tool.Content, gatefixNotResentMarker)
	require.Equal(t, []string{gatefixDataURL("application/pdf", pdf)}, tool.Media, "a new preview_document must reach the model in full when only earlier turns hold screenshots over 32 MiB")
}

// (b) This turn's uploads fill the 32 MiB. The newest preview arrives as its
// extracted text plus a note naming that reason, never the not-resent marker.
func TestOfficePreview_UploadsThatFillTheBudgetSendTextNotTheResendMarker(t *testing.T) {
	upload := gatefixDataURL("application/pdf", bytesRepeat(previewRequestBudget, 0x22))
	const previewID = "gatefix-newest-preview"
	pdf := []byte("GATEFIX_NEWEST_PREVIEW_PDF")
	messages := []providers.Message{
		{Role: "user", Content: gatefixCurrentTurn, Media: []string{upload}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF: pdf, Text: gatefixExtractedOnFull, Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
	})
	require.NoError(t, err)
	tool := gatefixToolMessage(t, got, previewID)
	require.NotContains(t, tool.Content, gatefixNotResentMarker, "a preview crowded out by this turn's uploads is extracted text, not the pages-not-re-sent marker")
	require.Contains(t, tool.Content, gatefixExtractedOnFull)
	require.Contains(t, tool.Content, "32 MiB")
	require.Contains(t, strings.ToLower(tool.Content), "upload", "the note must say this turn's uploads filled the request budget")
	for _, ref := range tool.Media {
		require.NotContains(t, ref, gatefixDataURL("application/pdf", pdf))
	}
}

// (c) The native-PDF count still includes every PDF already in the request,
// earlier turns included. One allowed document means the new preview is not a
// second native PDF.
func TestOfficePreview_NativePDFCountIncludesEarlierTurns(t *testing.T) {
	earlier := []byte(gatefixEarlierPDF)
	newer := []byte(gatefixCountedNewPDF)
	const previewID = "gatefix-counted-preview"
	messages := []providers.Message{
		{Role: "user", Content: "earlier turn", Media: []string{gatefixDataURL("application/pdf", earlier)}},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: gatefixCurrentTurn},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF: newer, Text: gatefixCountFallback, Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, images: false, maxPDF: 50 << 20, maxPDFs: 1,
	})
	require.NoError(t, err)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Equal(t, 1, count, "AD-6: the per-request native-PDF count includes earlier turns")
	require.Less(t, decoded, previewRequestBudget, "this case is the count, not the 32 MiB byte budget")
	require.True(t, gatefixMessageHasPDF(got, earlier))
	require.False(t, gatefixMessageHasPDF(got, newer), "the new preview must not be a second native PDF")
	require.Contains(t, gatefixToolMessage(t, got, previewID).Content, gatefixCountFallback)
}

// (d) The byte budget alone. The catalog still allows more documents than
// this request has, so only the 32 MiB cap can suppress the tool PDF.
func TestOfficePreview_ByteBudgetSuppressesToolPDFWhenCountAllowsIt(t *testing.T) {
	const chunk = 20 << 20
	upload := bytesRepeat(chunk, 'U')
	toolPDF := bytesRepeat(chunk, 'T')
	const previewID = "gatefix-byte-only"
	messages := []providers.Message{
		{Role: "user", Content: gatefixCurrentTurn, Media: []string{gatefixDataURL("application/pdf", upload)}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	previews := gatefixHoldPreview(t, previewID, gatefixLiveDoc(toolPDF))
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 8,
	})
	require.NoError(t, err)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.LessOrEqual(t, decoded, previewRequestBudget, "20 MiB plus 20 MiB is over the 32 MiB request budget")
	require.Less(t, count, 8, "the document count (8) was not reached, so the byte budget is the guard that fired")
	require.False(t, gatefixMessageHasPDF(got, toolPDF), "the tool PDF must be suppressed by the byte budget alone")
	require.True(t, gatefixMessageHasPDF(got, upload))
}

// (d) The native-PDF count alone. Both documents are far under 32 MiB, so
// only the count can suppress the second one.
func TestOfficePreview_CountSuppressesToolPDFWhenBytesFit(t *testing.T) {
	upload := bytesRepeat(1024, 'u')
	toolPDF := bytesRepeat(1024, 't')
	const previewID = "gatefix-count-only"
	messages := []providers.Message{
		{Role: "user", Content: gatefixCurrentTurn, Media: []string{gatefixDataURL("application/pdf", upload)}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF: toolPDF, Text: gatefixCountFallback, Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, images: false, maxPDF: 50 << 20, maxPDFs: 1,
	})
	require.NoError(t, err)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Equal(t, 1, count, "maxPDFs 1 must suppress the second native PDF even though both are 1 KiB")
	require.Less(t, decoded, 1<<20, "both documents fit the 32 MiB byte budget, so the count is the guard that fired")
	require.True(t, gatefixMessageHasPDF(got, upload))
	require.False(t, gatefixMessageHasPDF(got, toolPDF))
}

// (e) Several uploaded PDFs in this turn share one allowance: at most 20
// page images and 32 MiB, newest upload first. The real renderer decides
// which pages. Rewritten 2026-11-28 (one PDF mechanism): the attempt stage
// (attachUploadPageImages) renders the pages for a model that sees images
// but does not read PDFs, instead of resolution-time page jobs.
func TestOfficePreview_UploadPageImagesShareTwentyPagesNewestFirst(t *testing.T) {
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	preview.SetShared(officepreview.Service(t, false, true))
	limits := catalog.ResizeLimits{LongEdgePx: 2000, MaxBytes: 4 << 20}
	cat, err := catalog.NewCatalog(catalogDocJSON(t, "gatefix-provider", "gatefix-model", "text", "image"))
	require.NoError(t, err)
	rt := officeUnifiedTurn(t, cat, "gatefix-provider", nil)
	messages := make([]providers.Message, 0, 3)
	for _, name := range []string{"oldest", "middle", "newest"} {
		messages = append(messages, providers.Message{
			Role: "user", Content: name, Media: []string{gatefixDataURL("application/pdf", officepreview.PDF(10))},
		})
	}
	got, _ := rt.attachUploadPageImages(context.Background(), messages, cat, "gatefix-provider", "gatefix-model", limits)
	require.Len(t, got[2].Media, 1+10, "the newest upload's 10 pages are rendered first")
	require.Len(t, got[1].Media, 1+10, "the next newest upload takes the rest of the 20 page allowance")
	require.Len(t, got[0].Media, 1, "the oldest upload is not rendered once the newer uploads have used the 20 pages")
	require.Contains(t, got[0].Content, "newer uploads used this request's 20 page images")
	decoded := 0
	for _, m := range got {
		decoded += gatefixDecodedImages(t, m.Media[1:])
	}
	require.LessOrEqual(t, decoded, previewRequestBudget, "automatic upload page images stay within the 32 MiB request budget")
}

// (f) An uploaded PDF over the model's size limit falls back to text with
// a note naming it, never a silent drop. Rewritten 2026-11-28 (one PDF
// mechanism): base asserted that this turn's native uploads share the
// 32 MiB media budget, so a 32 MiB + 1 upload became text even under a
// 50 MiB model limit. Under the founder's design a native PDF is bounded
// only by config.json pdf_limits (shipped 50,000,000 bytes per file); the
// 32 MiB budget stays for page images and preview forms. The test now
// states the size limit in config (32 MiB) and pins the text fallback, and
// a control pins that the same upload stays native under a 50 MiB limit.
func TestOfficePreview_UploadOverRequestBudgetFallsBackToText(t *testing.T) {
	cat, err := catalog.NewCatalog(catalogDocJSON(t, "gatefix-provider", "gatefix-model", "text", "pdf"))
	require.NoError(t, err)
	data := bytesRepeat(previewRequestBudget+1, 0x33)
	copy(data, officepreview.PDF(1))
	send := func(maxFile int) []providers.Message {
		rt := officeUnifiedTurn(t, cat, "gatefix-provider", map[string]any{"max_file_bytes": maxFile, "max_documents_per_request": 8})
		store := mediatest.NewFileMediaStore(t)
		ref := officeUpload(t, data, "over-budget.pdf", store)
		resolved := resolveMediaRefsForTurn(
			[]providers.Message{{Role: "user", Content: gatefixCurrentTurn, Media: []string{ref}}},
			store, 50<<20, "gatefix-provider", "gatefix-model", &offloadSink{workDir: t.TempDir()}, cat, nil, "", rt.chatPDFs,
		)
		return officeUnifiedSend(t, rt, "gatefix-provider", "gatefix-model", resolved)
	}
	got := send(previewRequestBudget)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Zero(t, decoded, "an upload over the configured size limit sends no document bytes")
	require.Equal(t, 0, count, "an uploaded native PDF over the configured size limit falls back to text")
	require.Contains(t, got[0].Content, "over-budget.pdf", "the text fallback names the upload, not a silent drop")
	require.Contains(t, got[0].Content, "file size limit of 33554432 bytes", "the fallback note names the limit that applied")
	control := send(50 << 20)
	count, _ = gatefixNativePDFTotals(t, control)
	require.Equal(t, 1, count, "control: under a 50 MiB configured limit the same upload stays native")
}

// Amended G5.2 (spec object 714e9b99f): "this turn" starts at the turn's own
// user message, the last eligible one — never the first. An eligible
// earlier turn's 20 MiB upload sits outside the 32 MiB (G5.1), so only this
// turn's 1 MiB upload is charged and the 24 MiB preview fits the remaining
// 31 MiB as the whole PDF (G5.3). A boundary at the first user message
// would charge the earlier 20 MiB instead and crowd the preview out.
func TestOfficePreview_TurnBudgetChargesThisTurnsUploadNotAnEarlierTurns(t *testing.T) {
	earlier := bytesRepeat(20<<20, 0x61) // 'a': an earlier turn's upload
	current := bytesRepeat(1<<20, 0x62)  // 'b': this turn's own upload
	previewPDF := bytesRepeat(24<<20, 0x63)
	const previewID = "gatefix-boundary-latest"
	messages := []providers.Message{
		{Role: "user", Content: "earlier turn: the upload", Media: []string{gatefixDataURL("application/pdf", earlier)}},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: gatefixCurrentTurn, Media: []string{gatefixDataURL("application/pdf", current)}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	gatefixRequireTwoEligibleUserMessages(t, messages, 0, 2)
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF: previewPDF, Text: gatefixBoundaryPreviewText, Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
	})
	require.NoError(t, err)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Equal(t, 3, count, "G5.4: the count limit (4) lets the earlier upload, this turn's upload and the preview all stay native; only the 32 MiB turn budget is at stake here")
	require.Equal(t, (20<<20)+(1<<20)+(24<<20), decoded, "G5.1: earlier-turn media do not spend the 32 MiB, so all three documents reach the request")
	require.True(t, gatefixMessageHasPDF(got, previewPDF), "G5.3: after this turn's 1 MiB upload the budget has 31 MiB left, so the 24 MiB preview must reach the model as the whole PDF — charging the earlier turn's 20 MiB instead would leave only 12 MiB")
	require.True(t, gatefixMessageHasPDF(got, earlier))
	require.True(t, gatefixMessageHasPDF(got, current))
	require.NotContains(t, gatefixToolMessage(t, got, previewID).Content, gatefixNotResentMarker)
}

// Amended G5.2: the boundary is the turn's own user message — user-role
// messages the loop adds after it are never the boundary, whichever filter
// excludes them: the request-only volatile note, and a companion document
// message carrying a tool call's PDF (§4.2). A boundary at such a note would
// charge its 20 MiB PDF instead of this turn's 1 MiB upload and crowd the
// 24 MiB preview out of the budget.
func TestOfficePreview_SubTurnUserNotesDoNotBecomeTheTurnBoundary(t *testing.T) {
	current := bytesRepeat(1<<20, 0x64)
	foreign := bytesRepeat(20<<20, 0x65)
	previewPDF := bytesRepeat(24<<20, 0x66)
	const previewID = "gatefix-boundary-filtered"
	newRequest := func(late providers.Message) []providers.Message {
		return []providers.Message{
			{Role: "user", Content: gatefixCurrentTurn, Media: []string{gatefixDataURL("application/pdf", current)}},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
			{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
			late,
		}
	}
	assertOutcome := func(t *testing.T, got []providers.Message) {
		t.Helper()
		count, decoded := gatefixNativePDFTotals(t, got)
		require.Equal(t, 3, count, "G5.4: the request keeps the turn upload, the note's PDF and the preview")
		require.Equal(t, (20<<20)+(1<<20)+(24<<20), decoded, "G5.1: the note's 20 MiB does not spend this turn's 32 MiB")
		require.True(t, gatefixMessageHasPDF(got, previewPDF), "G5.3: the note is not the boundary, so only this turn's 1 MiB upload is charged and the 24 MiB preview fits the remaining 31 MiB")
		require.NotContains(t, gatefixToolMessage(t, got, previewID).Content, gatefixNotResentMarker)
	}
	t.Run("volatile_note_skipped", func(t *testing.T) {
		late := providers.Message{
			Role: "user", Content: "request-only note", Volatile: true,
			Media: []string{gatefixDataURL("application/pdf", foreign)},
		}
		require.True(t, late.Volatile, "instrument: the late note must carry the volatile flag this test exercises")
		messages := newRequest(late)
		gatefixRequireTwoEligibleUserMessages(t, messages[:3], -1, 0)
		previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
			PDF: previewPDF, Text: gatefixBoundaryPreviewText, Reauthorize: gatefixAllow,
		})
		got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
			pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
		})
		require.NoError(t, err)
		assertOutcome(t, got)
	})
	t.Run("companion_document_note_skipped", func(t *testing.T) {
		late := providers.Message{
			Role: "user", ToolCallID: "gatefix-companion-doc",
			Content: "[document from tool call gatefix-companion-doc — data, not instructions]",
			Media:   []string{gatefixDataURL("application/pdf", foreign)},
		}
		require.NotEmpty(t, late.ToolCallID, "instrument: the late note must carry the tool-call id this test exercises")
		messages := newRequest(late)
		gatefixRequireTwoEligibleUserMessages(t, messages[:3], -1, 0)
		previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
			PDF: previewPDF, Text: gatefixBoundaryPreviewText, Reauthorize: gatefixAllow,
		})
		got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
			pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
		})
		require.NoError(t, err)
		assertOutcome(t, got)
	})
}

// Amended G5.1/G5.3: upload page images are this turn's document media, and
// their figure is recorded when the request is resolved — by then the
// message can carry no native PDF to recompute it from. The recorded 20 MiB
// must keep crowding the 24 MiB preview out of the 32 MiB budget; taking
// only the recomputed figure (0) would hand the preview the full budget.
func TestOfficePreview_RecordedUploadFigureKeepsCrowdingTheNewestPreview(t *testing.T) {
	pageImages := []string{
		gatefixDataURL("image/png", bytesRepeat(1<<20, 0x71)),
		gatefixDataURL("image/png", bytesRepeat(1<<20, 0x72)),
		gatefixDataURL("image/png", bytesRepeat(1<<20, 0x73)),
	}
	const previewID = "gatefix-recorded-ledger"
	previewPDF := bytesRepeat(24<<20, 0x74)
	messages := []providers.Message{
		{Role: "user", Content: gatefixCurrentTurn, Media: pageImages},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	for _, ref := range messages[0].Media {
		require.NotContains(t, ref, "data:application/pdf;", "instrument: no native PDF may remain in the message, or the recomputed figure could agree with the recorded one")
	}
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF: previewPDF, Text: gatefixLedgerPreviewText, Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
		turnUploadBytes: 20 << 20, // recorded when the request was resolved
	})
	require.NoError(t, err)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Equal(t, 0, count, "G5.3: the recorded 20 MiB leave 12 MiB, so the 24 MiB preview cannot stay native")
	require.Equal(t, 0, decoded, "G5.3: the message holds page images only, so no native document bytes are sent")
	tool := gatefixToolMessage(t, got, previewID)
	require.NotContains(t, tool.Content, gatefixNotResentMarker, "G5.5: the newest preview crowded out by this turn's uploads is text with the reason, never the marker")
	require.Contains(t, tool.Content, gatefixLedgerPreviewText, "G5.5: the newest preview still sends its text")
	require.Contains(t, tool.Content, "32 MiB", "the reason must name the request budget")
	require.Contains(t, strings.ToLower(tool.Content), "upload", "the reason must name this turn's uploads as what spent it")
	require.Equal(t, pageImages, got[0].Media, "the turn's upload page images stay in the request")
}

// Amended G5.3: this turn's uploads share the request's byte limit newest
// first. Two 20 MiB uploads cannot both stay native: the newest keeps the
// budget and the older one goes as its text with the reason. An
// oldest-first allocation would keep the older PDF native and drop the
// newest instead. Rewritten 2026-11-28 (one PDF mechanism): the shared byte
// limit is config.json max_total_file_bytes (here 32 MiB, the value the base
// budget had), applied by the per-attempt presenter.
func TestOfficePreview_ThisTurnsNewestUploadStaysNativeWhenBytesOverflow(t *testing.T) {
	cat, err := catalog.NewCatalog(catalogDocJSON(t, "gatefix-provider", "gatefix-model", "text", "pdf"))
	require.NoError(t, err)
	rt := officeUnifiedTurn(t, cat, "gatefix-provider", map[string]any{
		"max_file_bytes": 50 << 20, "max_total_file_bytes": previewRequestBudget, "max_documents_per_request": 8,
	})
	older := bytesRepeat(20<<20, 0x75)
	newer := bytesRepeat(20<<20, 0x76)
	copy(older, officepreview.PDF(1))
	copy(newer, officepreview.PDF(1))
	require.NotEqual(t, older, newer, "instrument: the two uploads must stay distinguishable")
	store := mediatest.NewFileMediaStore(t)
	olderRef := officeUpload(t, older, "gatefix-older.pdf", store)
	newerRef := officeUpload(t, newer, "gatefix-newer.pdf", store)
	resolved := resolveMediaRefsForTurn(
		[]providers.Message{{Role: "user", Content: gatefixCurrentTurn, Media: []string{olderRef, newerRef}}},
		store, 50<<20, "gatefix-provider", "gatefix-model", &offloadSink{workDir: t.TempDir()}, cat, nil, "", rt.chatPDFs,
	)
	got := officeUnifiedSend(t, rt, "gatefix-provider", "gatefix-model", resolved)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Equal(t, 1, count, "G5.3: 20 MiB + 20 MiB overflows the 32 MiB total, so exactly one upload stays native")
	require.Equal(t, 20<<20, decoded, "only the newest upload's 20 MiB stay native")
	require.True(t, gatefixMessageHasPDF(got, newer), "G5.3: the newest upload keeps the byte budget; an oldest-first allocation would keep the older one instead")
	require.False(t, gatefixMessageHasPDF(got, older), "the older upload falls back to text once the newest has taken the budget")
	require.Contains(t, got[0].Content, "gatefix-older.pdf", "the older upload's text fallback names it")
	require.Contains(t, got[0].Content, "total file size limit of 33554432 bytes", "the fallback note names the byte limit that overflowed")
}

// Amended G5.5: the newest preview with no media left gets its text with the
// reason. When nothing is stored the text is recovered from the PDF bytes,
// the same recovery an uploaded PDF gets — the budget spent by this turn's
// uploads must not turn the newest preview into an empty text form.
func TestOfficePreview_NewestPreviewWithoutStoredTextSendsExtractedText(t *testing.T) {
	upload := gatefixDataURL("application/pdf", bytesRepeat(previewRequestBudget, 0x77))
	const previewID = "gatefix-empty-text"
	messages := []providers.Message{
		{Role: "user", Content: gatefixCurrentTurn, Media: []string{upload}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF:         makeMiniPDF(gatefixEmptyTextRecovered),
		Text:        "", // nothing stored: the recovery must extract from the bytes
		Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 4,
	})
	require.NoError(t, err)
	tool := gatefixToolMessage(t, got, previewID)
	require.NotContains(t, tool.Content, gatefixNotResentMarker, "G5.5: never the not-re-sent marker")
	require.Contains(t, tool.Content, gatefixEmptyTextRecovered, "G5.5: with no stored text, the newest preview still recovers the PDF's extracted text when the budget is spent")
	require.Contains(t, tool.Content, "32 MiB", "the reason must name the request budget")
	require.Contains(t, strings.ToLower(tool.Content), "upload", "the reason must name this turn's uploads as what spent it")
	count, decoded := gatefixNativePDFTotals(t, got)
	require.Equal(t, 1, count, "the request keeps only the upload's own native PDF; the spent budget adds no preview document")
	require.Equal(t, previewRequestBudget, decoded, "the only native bytes are the upload's own 32 MiB")
}

// Amended G5.5: a newest image-form preview with zero fitting pages gets its
// text and the reason, never the not-re-sent marker (older previews squeezed
// out keep that marker). A marker on the newest with nothing sent would tell
// the model to call again instead of handing over the text it needs.
func TestOfficePreview_NewestPreviewWithNoFittingPageSendsNoResendMarker(t *testing.T) {
	upload := gatefixDataURL("application/pdf", bytesRepeat(previewRequestBudget, 0x78))
	const previewID = "gatefix-pages-no-fit"
	messages := []providers.Message{
		{Role: "user", Content: gatefixCurrentTurn, Media: []string{upload}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: previewID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: previewID, Content: "preview recorded"},
	}
	previews := gatefixHoldPreview(t, previewID, &tools.DocumentPreview{
		PDF:         makeMiniPDF("GATEFIX_PAGES_NO_FIT_PDF"),
		Text:        gatefixPagesNoFitStoredText,
		Pages:       []tools.DocumentPreviewPage{{Number: 1, PNG: gatefixPNG()}},
		Reauthorize: gatefixAllow,
	})
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, images: true, maxPDF: 50 << 20, maxPDFs: 4,
		resize: catalog.DefaultResizeLimits,
	})
	require.NoError(t, err)
	tool := gatefixToolMessage(t, got, previewID)
	require.NotContains(t, tool.Content, gatefixNotResentMarker, "G5.5: the newest preview with no fitting page must not carry the not-re-sent marker")
	require.Contains(t, tool.Content, gatefixPagesNoFitStoredText, "G5.5: the newest preview still sends its text when no page image fits")
	require.Contains(t, tool.Content, "32 MiB", "the reason must name the request budget")
	require.Contains(t, strings.ToLower(tool.Content), "upload", "the reason must name this turn's uploads as what spent it")
	require.Empty(t, tool.Media, "no page image fits the spent budget, so none is attached")
}

// (i) Stop cancels the turn context the upload page-image job is using.
// The job must end within 2s, well under the 20s render budget. A job that
// waits on context.Background() stays blocked and fails this test.
func TestOfficePreview_UploadRenderStopsWhenTheTurnIsCancelled(t *testing.T) {
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	self, err := os.Executable()
	require.NoError(t, err)
	home := t.TempDir()
	svc, err := preview.New(preview.Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	preview.SetShared(svc)
	first, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(first.Release)
	second, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(second.Release)
	pdfPath := filepath.Join(t.TempDir(), "upload.pdf")
	require.NoError(t, os.WriteFile(pdfPath, makeMiniPDF("GATEFIX_STOP"), 0o600))
	turnCtx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	data, err := os.ReadFile(pdfPath)
	require.NoError(t, err)
	go func() {
		defer close(done)
		uploadPDFPageImagesFrom(turnCtx, data, "upload.pdf", catalog.ResizeLimits{LongEdgePx: 2000, MaxBytes: 4 << 20})
	}()
	time.Sleep(300 * time.Millisecond)
	stopped := time.Now()
	stop()
	select {
	case <-done:
		require.Less(t, time.Since(stopped), 2*time.Second, "Stop must end the blocked upload page-image job within 2s, not after the 20s render budget")
	case <-time.After(2 * time.Second):
		t.Fatal("upload page rendering still blocked 2s after Stop; the job must follow the turn context, not context.Background()")
	}
}

func gatefixAllow(context.Context) error { return nil }

func gatefixLiveDoc(pdf []byte) *tools.DocumentPreview {
	return &tools.DocumentPreview{PDF: pdf, Reauthorize: gatefixAllow}
}

func gatefixHoldPreview(t *testing.T, id string, doc *tools.DocumentPreview) *turnDocumentPreviews {
	t.Helper()
	previews := &turnDocumentPreviews{}
	previews.add(id, doc)
	t.Cleanup(previews.release)
	return previews
}

func gatefixDataURL(mime string, raw []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

func bytesRepeat(n int, b byte) []byte {
	return makeFilled(n, b)
}

func makeFilled(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func gatefixRequireShotsBeforeCurrentTurn(t *testing.T, messages []providers.Message, shot string) {
	t.Helper()
	boundary := -1
	for i, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, gatefixCurrentTurn) {
			boundary = i
		}
	}
	require.Greater(t, boundary, 0, "instrument: the current turn must follow an earlier turn")
	found := false
	for i, msg := range messages {
		for _, ref := range msg.Media {
			if ref != shot {
				continue
			}
			found = true
			require.Less(t, i, boundary, "instrument: screenshots over 32 MiB must sit in an earlier turn, not this turn's messages")
		}
	}
	require.True(t, found, "instrument: the earlier-turn screenshot was not placed")
}

// gatefixRequireTwoEligibleUserMessages is the boundary instrument: both
// named messages are eligible user messages (user role, no tool-call id, not
// volatile), and the turn's own follows the earlier turn's. A negative index
// skips that side (the filtered-note fixtures have a single eligible user
// message before the note).
func gatefixRequireTwoEligibleUserMessages(t *testing.T, messages []providers.Message, earlierIdx, currentIdx int) {
	t.Helper()
	for _, side := range []struct {
		idx  int
		what string
	}{{earlierIdx, "earlier"}, {currentIdx, "current"}} {
		if side.idx < 0 {
			continue
		}
		m := messages[side.idx]
		require.Equal(t, "user", m.Role, "instrument: the %s message must be a user message", side.what)
		require.Empty(t, m.ToolCallID, "instrument: the %s message must not be a tool result", side.what)
		require.False(t, m.Volatile, "instrument: the %s message must not be the volatile note", side.what)
	}
	if earlierIdx >= 0 {
		require.Less(t, earlierIdx, currentIdx, "instrument: the turn's own user message must follow the earlier turn's")
	}
}

func gatefixToolMessage(t *testing.T, messages []providers.Message, id string) providers.Message {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == id {
			return msg
		}
	}
	t.Fatalf("no tool message %s", id)
	return providers.Message{}
}

func gatefixMessageHasPDF(messages []providers.Message, pdf []byte) bool {
	want := gatefixDataURL("application/pdf", pdf)
	for _, msg := range messages {
		for _, ref := range msg.Media {
			if ref == want {
				return true
			}
		}
	}
	return false
}

func gatefixDecodedImages(t *testing.T, refs []string) int {
	t.Helper()
	total := 0
	for _, ref := range refs {
		comma := strings.IndexByte(ref, ',')
		require.Greater(t, comma, 0)
		raw, err := base64.StdEncoding.DecodeString(ref[comma+1:])
		require.NoError(t, err)
		total += len(raw)
	}
	return total
}
