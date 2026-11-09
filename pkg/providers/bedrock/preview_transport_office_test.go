// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package bedrock

// Office-document preview — Bedrock transport (office-preview spec §4.2,
// FR-039; TDD row T-44, BDD B-44).
//
// Oracles from the spec's §4.2 matrix, never from the code:
//   - Upload (user message): "**add** document block".
//   - read_file / preview_document result: "document block in the tool
//     result if documented (S-2), else companion". Until S-2 records a
//     documented in-result form, the invariant both branches share: the
//     authorized PDF reaches the wire as a document block carrying the
//     source bytes, never the unsupported-media omission text.
//   - Rules: "Omnipus serialization gaps are implementation work and never
//     a reason to withhold native PDF" (§4.2). "let an adapter silently
//     drop a native PDF" is a §9 prohibition.
//
// RED reasons on the pre-change code (verified by reading
// provider_bedrock.go before writing this test): contentBlock has no
// document field at all — user-message PDFs are skipped with a warning and
// tool-result PDFs land in "[Tool-result media omitted for Bedrock: ...]".
// The assertions marshal the built content and require the Bedrock
// Converse document block shape ("document" with format "pdf"), so they
// compile today and fail today.

import (
	"encoding/json"
	"strings"
	"testing"
)

// base64("%PDF-1.4\n") — valid base64; Bedrock document blocks carry bytes.
const officePDFDataURL = "data:application/pdf;base64,JVBERi0xLjQK"

// TestOfficePreview_UserUploadPDF_DocumentBlock (§4.2 upload column —
// "add document block", FR-039).
func TestOfficePreview_UserUploadPDF_DocumentBlock(t *testing.T) {
	blocks := buildUserContent(Message{Role: "user", Content: "summarise", Media: []string{officePDFDataURL}})
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"document"`) || !strings.Contains(s, `"pdf"`) || !strings.Contains(s, "JVBERi0xLjQK") {
		t.Fatalf("BLOCKED: user PDF dropped — spec §4.2 requires a document block for the Bedrock adapter (T-44, B-44), got %s", s)
	}
}

// TestOfficePreview_ToolResultPDF_DocumentBlockReachesWire (§4.2
// tool-result column, FR-039): the tool result keeps its text and the PDF
// reaches the wire as a document block — never the omission warning.
func TestOfficePreview_ToolResultPDF_DocumentBlockReachesWire(t *testing.T) {
	messages, _ := convertMessages([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "preview_document"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "pages 1-4 of 4", Media: []string{officePDFDataURL}},
	})
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "JVBERi0xLjQK") || !strings.Contains(s, `"document"`) {
		t.Fatalf("BLOCKED: tool-result PDF dropped from the Bedrock request — spec §4.2 requires the document block to reach the wire (T-44, B-44), got %s", s)
	}
	if strings.Contains(s, "Tool-result media omitted") {
		t.Fatalf("tool-result PDF must never be reported as omitted media (§4.2: gaps are implementation work), got %s", s)
	}
}
