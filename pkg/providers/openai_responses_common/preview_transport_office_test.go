// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package openai_responses_common

// Office-document preview — Responses-API transport (office-preview spec
// §4.2, FR-039; TDD row T-44, BDD B-44).
//
// Oracles from the spec's §4.2 matrix, never from the code:
//   - Upload (user message): "**add** file input part".
//   - read_file / preview_document result: "file input in the
//     function-call output if documented (S-2), else companion user item".
//     Until S-2 records a documented in-output form, the invariant both
//     branches share: the authorized PDF reaches the wire as a file input
//     carrying the source data URL, never silently dropped.
//   - Rules: "Omnipus serialization gaps are implementation work and never
//     a reason to withhold native PDF" (§4.2).
//
// RED reasons on the pre-change code (verified by reading
// responses_common.go before writing this test): BuildMultipartContent has
// no application/pdf branch — a user-message PDF is silently dropped; the
// tool-output path serializes only data:image/ media.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// base64("%PDF-1.4\n") — valid base64.
const officePDFDataURL = "data:application/pdf;base64,JVBERi0xLjQK"

// TestOfficePreview_UserUploadPDF_FileInputPart (§4.2 upload column —
// "add file input part", FR-039): a user-message PDF must ride an
// OfInputFile part with the data URL as FileData.
func TestOfficePreview_UserUploadPDF_FileInputPart(t *testing.T) {
	input, _ := TranslateMessages([]protocoltypes.Message{{Role: "user", Content: "summarise", Media: []string{officePDFDataURL}}})
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type":"input_file"`) || !strings.Contains(s, officePDFDataURL) {
		t.Fatalf("BLOCKED: user PDF dropped — spec §4.2 requires a file input part for the Responses adapter (T-44, B-44), got %s", s)
	}
}

// TestOfficePreview_ToolResultPDF_FileInputReachesWire (§4.2 tool-result
// column, FR-039): the function-call output keeps its text and the PDF
// rides a file input (companion user item until S-2 documents an in-output
// form) — never dropped.
func TestOfficePreview_ToolResultPDF_FileInputReachesWire(t *testing.T) {
	input, _ := TranslateMessages([]protocoltypes.Message{
		{Role: "assistant", ToolCalls: []protocoltypes.ToolCall{{ID: "call-1", Name: "preview_document"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "pages 1-4 of 4", Media: []string{officePDFDataURL}},
	})
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, officePDFDataURL) || !strings.Contains(s, `"type":"input_file"`) {
		t.Fatalf("BLOCKED: tool-result PDF dropped from the Responses request — spec §4.2 requires the file input to reach the wire (T-44, B-44), got %s", s)
	}
	if !strings.Contains(s, "call-1") {
		t.Fatalf("function call output lost its correlation id, got %s", s)
	}
}
