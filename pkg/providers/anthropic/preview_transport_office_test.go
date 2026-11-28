// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package anthropicprovider

// Office-document preview — Anthropic transport (office-preview spec §4.2,
// FR-039; TDD row T-44, BDD B-44).
//
// Oracles from the spec's §4.2 matrix, never from the code:
//   - Upload (user message): "document block (exists)" — regression guard.
//   - read_file / preview_document result: "document block inside the tool
//     result if the current API documents it (S-2), else companion document
//     block after the tool results in the same user message". Until S-2
//     records an in-tool-result form, the invariant this test enforces is
//     the one both branches share: the authorized PDF reaches the wire as a
//     document block with the source bytes, never an omission warning.
//   - Rules: "Omnipus serialization gaps are implementation work and never
//     a reason to withhold native PDF" (§4.2).
//
// RED reason on the pre-change code: anthropicToolResult counts a PDF as an
// unsupported media type and emits "[Tool-result media omitted for Anth:
// ...]" instead of a document block (verified by reading provider.go before
// writing this test).

import (
	"encoding/json"
	"strings"
	"testing"
)

// base64("%PDF-1.4\n") — valid base64; the SDK decodes document sources.
const officePDFDataURL = "data:application/pdf;base64,JVBERi0xLjQK"

// TestOfficePreview_UserUploadPDF_DocumentBlock (§4.2 upload column,
// regression guard): user-message PDFs already ride document blocks.
func TestOfficePreview_UserUploadPDF_DocumentBlock(t *testing.T) {
	params, err := buildParams([]Message{{Role: "user", Content: "summarise", Media: []string{officePDFDataURL}}}, nil, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type":"document"`) || !strings.Contains(s, "JVBERi0xLjQK") {
		t.Fatalf("user PDF must serialize as a document block (spec §4.2 upload column), got %s", s)
	}
}

// TestOfficePreview_ToolResultPDF_DocumentBlockReachesWire (§4.2
// tool-result column, FR-039): the tool-result PDF becomes a document block
// with the source bytes — never the unsupported-media omission text.
func TestOfficePreview_ToolResultPDF_DocumentBlockReachesWire(t *testing.T) {
	params, err := buildParams([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "preview_document"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "pages 1-4 of 4", Media: []string{officePDFDataURL}},
	}, nil, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)

	if !strings.Contains(s, "JVBERi0xLjQK") {
		t.Fatalf("BLOCKED: tool-result PDF dropped from the outbound request — spec §4.2 requires a document block (companion after the tool results until S-2 documents an in-result form) (T-44, B-44); got %s", s)
	}
	if !strings.Contains(s, `"type":"document"`) {
		t.Fatalf("PDF bytes present but not as a document block (spec §4.2), got %s", s)
	}
	if strings.Contains(s, "Tool-result media omitted") {
		t.Fatalf("tool-result PDF must never be reported as omitted media (§4.2: gaps are implementation work), got %s", s)
	}
}
