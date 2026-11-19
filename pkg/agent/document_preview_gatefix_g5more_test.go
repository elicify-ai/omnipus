//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
// page images and 32 MiB, newest upload first. The real renderer and
// renderUploadPageJobs / uploadPDFPageImagesWithin decide which pages.
func TestOfficePreview_UploadPageImagesShareTwentyPagesNewestFirst(t *testing.T) {
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	preview.SetShared(officepreview.Service(t, false, true))
	limits := catalog.ResizeLimits{LongEdgePx: 2000, MaxBytes: 4 << 20}
	dir := t.TempDir()
	paths := make([]string, 3)
	for i, name := range []string{"oldest.pdf", "middle.pdf", "newest.pdf"} {
		paths[i] = filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(paths[i], officepreview.PDF(10), 0o600))
	}
	media := [][]string{{}, {}, {}}
	injections := [][]string{{""}, {""}, {""}}
	jobs := make([]uploadPageJob, 3)
	for i := range jobs {
		jobs[i] = uploadPageJob{
			msg: i, mediaAt: 0, injAt: 0, noteAt: 0,
			localPath: paths[i], name: filepath.Base(paths[i]), dest: paths[i],
			limits: limits,
		}
	}
	renderUploadPageJobs(context.Background(), jobs, media, injections)
	require.Len(t, media[2], 10, "the newest upload's 10 pages are rendered first")
	require.Len(t, media[1], 10, "the next newest upload takes the rest of the 20 page allowance")
	require.Empty(t, media[0], "the oldest upload is not rendered once the newer uploads have used the 20 pages")
	require.Contains(t, injections[0][0], "newer uploads used this request's 20 page images")
	decoded := 0
	for _, refs := range media {
		decoded += gatefixDecodedImages(t, refs)
	}
	require.LessOrEqual(t, decoded, previewRequestBudget, "automatic upload page images stay within the 32 MiB request budget")
}

// (f) This turn's uploads alone over 32 MiB. The assembled request's document
// media stays at most 32 MiB: the oversized native PDF falls back to text,
// rather than staying a document while previews are merely clamped to zero.
func TestOfficePreview_UploadOverRequestBudgetFallsBackToText(t *testing.T) {
	var doc map[string]any
	require.NoError(t, json.Unmarshal(catalogDocJSON(t, "gatefix-provider", "gatefix-model", "text", "pdf"), &doc))
	rows, ok := doc["providers"].([]any)
	require.True(t, ok && len(rows) > 0, "catalog providers must be a non-empty list, got %T", doc["providers"])
	row, ok := rows[0].(map[string]any)
	require.True(t, ok, "catalog provider row must be an object, got %T", rows[0])
	row["pdf_limits"] = map[string]any{"max_document_bytes": 50 << 20, "max_documents_per_request": 8}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	data := bytesRepeat(previewRequestBudget+1, 0x33)
	copy(data, officepreview.PDF(1))
	store := mediatest.NewFileMediaStore(t)
	ref := officeUpload(t, data, "over-budget.pdf", store)
	got := resolveMediaRefsWithOffload(
		[]providers.Message{{Role: "user", Content: gatefixCurrentTurn, Media: []string{ref}}},
		store, 50<<20, "gatefix-provider", "gatefix-model", &offloadSink{workDir: t.TempDir()}, cat, nil, "",
	)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.LessOrEqual(t, decoded, previewRequestBudget, "this turn's uploads alone must not put more than 32 MiB of document media in the request")
	require.Equal(t, 0, count, "an uploaded native PDF over the 32 MiB request budget falls back to text, even when the model's own limit is 50 MiB")
	require.Contains(t, got[0].Content, "[Attached file", "the text fallback is the attached-file text, not a silent drop")
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
	go func() {
		defer close(done)
		media := [][]string{{}}
		injections := [][]string{{""}}
		renderUploadPageJobs(turnCtx, []uploadPageJob{{
			localPath: pdfPath, name: "upload.pdf",
			limits: catalog.ResizeLimits{LongEdgePx: 2000, MaxBytes: 4 << 20},
		}}, media, injections)
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
