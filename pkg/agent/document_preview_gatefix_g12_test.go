// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// G12 (office-gatefix fixlist, team-lead adjudication 2026-11-20; T-133,
// office-preview spec §3.9 G5.6, 2026-11-21): a PDF-capable but
// image-incapable model whose per-request native-PDF COUNT is exhausted
// (AD-6) receives the exact count note, never "; this model reads text
// only".
//
// Oracle (T-133 tightens these two tests to the spec's exact sentence): the
// note is exactly "[model received: extracted text; the provider's
// per-request PDF count limit (1) is used up by other PDFs in this
// request]" (with stored text: followed by that text; without stored text:
// followed by the fallback extraction of the preview's own bytes). Plus
// T-133 (c): a ModelOnly preview (the read_file path) under the same
// used-up count adds no count note.
//
// RED history: the original loose assertions failed on the pre-fix code
// (the note was the wrong sentence). The behaviour already exists
// (ef706c8b2, 56e5313d3), so the tightened assertions have no RED on the
// pre-change code; the mutation proof of attachDocumentPreview is CHECK's
// and lives in the scratch clone, never committed here.

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// g12CountNote is the spec's exact note for maxPDFs = 1 (T-133).
const g12CountNote = "[model received: extracted text; the provider's per-request PDF count limit (1) is used up by other PDFs in this request]"

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
	noteStart := strings.Index(tool.Content, g12CountNote)
	require.GreaterOrEqual(t, noteStart, 0,
		"T-133: the count-exhausted preview's note must be the spec's exact sentence; got %q", tool.Content)
	require.Contains(t, tool.Content, "G12 extracted text",
		"T-133: with stored text, the note is followed by that text")
	require.Greater(t, strings.Index(tool.Content, "G12 extracted text"), noteStart,
		"T-133: the stored text follows the exact note")
	// R2-F1 (gate round 2, CHECK): the count reason never becomes the
	// text-only sentence. Across the WHOLE model-facing message, nothing may
	// tell a PDF-capable model it "reads text only" — the drop happened for
	// the used-up count (AD-6), and a contradictory capability claim would
	// misreport the model to itself. (Kills the mutant that appends the
	// wrong sentence to the exact note.)
	require.NotContains(t, tool.Content, "reads text only",
		"T-133/R2-F1: a count-exhausted PDF-capable model must never be told it reads text only; got %q", tool.Content)
}

// G12 companion (AD-6 and the fallback rules): the count-exhausted preview
// with NO stored text. AD-6: "if the newest preview gets no media it
// receives its extracted text with the reason …, never 'call again'". With
// no stored text, "its extracted text" is the fallback extraction of the
// same bytes — the recovery the provider-rejection and no-budget cases
// already take (document_preview_media.go's pdfFallbackText branch). The
// count case must not end the note at "[model received: extracted text …]"
// with nothing after it: the model would receive no content at all.
//
// TA-03 (gate round 2): the body is asserted, not just its envelope. The
// carried document and the preview carry DIFFERENT independently known page
// text (the officepreview.PDF fixture draws "(page N)" per page), so the
// assertions tell the preview's own extraction from the carried document's
// and from an extraction failure — without ever calling the production
// extractor to compute the expectation.
//
// RED on 56cd9ca03: the recovery condition tests only pdfRejected and
// noBudget, so the count case falls through to the empty-text final branch.
func TestOfficePreview_CountLimitWithoutStoredTextSendsFallbackText(t *testing.T) {
	const toolID = "g12-empty-text-preview"
	toolPDF := officepreview.PDF(1)                                                                     // known text: "page 1"
	carried := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(officepreview.PDF(2)) // known text: "page 1", "page 2"

	previews := &turnDocumentPreviews{}
	previews.add(toolID, &tools.DocumentPreview{
		PDF:         toolPDF,
		Text:        "", // nothing stored: the recovery branch is the only source of text
		Reauthorize: func(context.Context) error { return nil },
	})
	t.Cleanup(previews.release)

	messages := []providers.Message{
		{Role: "user", Content: "the carried PDF", Media: []string{carried}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: toolID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: toolID, Content: "preview recorded"},
	}
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, images: false, maxPDF: 50 << 20, maxPDFs: 1,
	})
	require.NoError(t, err)

	tool := got[len(got)-1]
	noteStart := strings.Index(tool.Content, g12CountNote)
	require.GreaterOrEqual(t, noteStart, 0,
		"T-133: the no-stored-text preview's note must be the spec's exact sentence; got %q", tool.Content)
	require.Contains(t, tool.Content, "[Attached file",
		"AD-6: the count-exhausted preview with no stored text must send the fallback extraction of its own bytes, not an empty result")
	require.Greater(t, strings.Index(tool.Content, "[Attached file"), noteStart,
		"T-133: the fallback extraction of the preview's own bytes follows the exact note")
	// TA-03: the body is the preview document's own text — the fixture's
	// independently known page text after the note, never the carried
	// document's text and never an extraction-failure notice for this valid
	// fixture.
	require.Greater(t, strings.Index(tool.Content, "page 1"), noteStart,
		"T-133/TA-03: the preview's known page text follows the exact note; got %q", tool.Content)
	require.NotContains(t, tool.Content, "page 2",
		"T-133/TA-03: the carried document's distinguishing page text must never appear — the fallback body is the preview's own bytes, not the wrong document")
	require.NotContains(t, tool.Content, "content could not be extracted",
		"T-133/TA-03: an extractable fixture must not receive the extraction-failure notice")
}

// T-133 (c): a ModelOnly preview (today's read_file path) under the same
// used-up count adds NO count note — the retained result text already is
// the text form, and the note is only for previews whose media was dropped
// from the request.
func TestOfficePreview_CountLimitModelOnlyAddsNoCountNote(t *testing.T) {
	const toolID = "g12-modelonly-preview"
	toolPDF := officepreview.PDF(1)
	previews := &turnDocumentPreviews{}
	previews.add(toolID, &tools.DocumentPreview{
		PDF:         toolPDF,
		Text:        "G12 modelonly text",
		ModelOnly:   true,
		Reauthorize: func(context.Context) error { return nil },
	})
	t.Cleanup(previews.release)

	messages := []providers.Message{
		{Role: "user", Content: "the carried PDF", Media: []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(officepreview.PDF(1))}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: toolID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: toolID, Content: "preview recorded"},
	}
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, images: false, maxPDF: 50 << 20, maxPDFs: 1,
	})
	require.NoError(t, err)

	tool := got[len(got)-1]
	require.NotContains(t, tool.Content, g12CountNote,
		"T-133(c): the read_file path adds no count note under the same used-up count")
	require.NotContains(t, tool.Content, "[model received:",
		"T-133(c): the ModelOnly branch adds no model-facing note at all when images are not expected")
}
