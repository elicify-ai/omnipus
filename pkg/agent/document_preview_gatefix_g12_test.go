// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// G12 (office-gatefix fixlist, team-lead adjudication 2026-11-20): a
// PDF-capable but image-incapable model whose per-request native-PDF COUNT
// is exhausted (AD-6) falls into the image-incapable case of the reason
// switch in document_preview_media.go::attachDocumentPreview and tells the
// model "; this model reads text only" — wrong on both counts: the model
// reads PDFs, and the reason is the count limit.
//
// Oracle: the spec (37e8342bf, AD-6) names the concept but no exact note
// text — "the provider's per-request PDF count still counts every PDF in
// the request". The assertions therefore require the note to name the count
// limit in AD-6's own terms ("per-request" … "count"/"limit") and forbid
// the wrong "reads text only" sentence; they do not pin a full sentence
// the spec never wrote.
//
// RED on the pre-fix code: the note is the wrong sentence, so the
// NotContains assertion fails first.

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestOfficePreview_CountLimitNoteNamesTheCountNotTextOnly(t *testing.T) {
	const toolID = "g12-newest-preview"
	toolPDF := []byte("%PDF-1.4 g12")
	carried := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(toolPDF)

	previews := &turnDocumentPreviews{}
	previews.add(toolID, &tools.DocumentPreview{
		PDF:         toolPDF,
		Text:        "G12 extracted text",
		Reauthorize: func(context.Context) error { return nil },
	})
	t.Cleanup(previews.release)

	messages := []providers.Message{
		// An earlier turn's native PDF, still in the request: it uses up the
		// catalog's per-request document count of 1 (AD-6 counts every PDF
		// in the request).
		{Role: "user", Content: "the carried PDF", Media: []string{carried}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: toolID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: toolID, Content: "preview recorded"},
	}
	// PDF-capable (pdf true), image-incapable (images false), maxPDFs 1 —
	// already used by the carried PDF. maxPDF is large so size is not the
	// reason for anything here.
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, images: false, maxPDF: 50 << 20, maxPDFs: 1,
	})
	require.NoError(t, err)

	count, _ := gatefixNativePDFTotals(t, got)
	require.LessOrEqual(t, count, 1, "AD-6: the per-request count of 1 still holds; the preview's PDF was dropped for the count, not for size")

	tool := got[len(got)-1]
	noteStart := strings.Index(tool.Content, "[model received:")
	require.GreaterOrEqual(t, noteStart, 0, "G12 instrument: the model-facing note must be present")
	note := tool.Content[noteStart:]
	require.NotContains(t, note, "reads text only",
		"G12: the model reads PDFs; the count-exhausted note must never say this model reads text only")
	lower := strings.ToLower(note)
	require.True(t, strings.Contains(lower, "per-request") || strings.Contains(lower, "per request"),
		"G12: the note must name the per-request scope of the count limit (AD-6 wording); got %q", note)
	require.True(t, strings.Contains(lower, "count") || strings.Contains(lower, "limit"),
		"G12: the note must name the count limit as the reason (AD-6 wording); got %q", note)
}
