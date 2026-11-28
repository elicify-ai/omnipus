package docextract_test

// PORT-SEC-1 regression, tool half: when the shared PDF parser is fully
// occupied, read_file must not call the file malformed or scanned, must still
// send the exact bytes natively, and must refuse a paged read with a busy
// error. The slots are held through docextract's in-package test helper
// (export_test.go); the tool is the real ReadFileTool.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestReadFile_PDFParserBusyIsNotMalformedNorScanned(t *testing.T) {
	venue := docextract.MakeVenuePDFForTest(t)
	docextract.HoldAllPDFParseSlotsForTest(t, venue)

	ws := t.TempDir()
	path := filepath.Join(ws, "venue.pdf")
	if err := os.WriteFile(path, venue, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := tools.NewReadFileTool(ws, false, tools.MaxReadFileSize)

	res := tool.Execute(context.Background(), map[string]any{"path": path})
	if res.IsError {
		t.Fatalf("a busy parser must not make an unpaged PDF read an error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "could not be read locally (parser busy, try again later)") {
		t.Fatalf("expected the N3 notice with the busy reason, got:\n%s", res.ForLLM)
	}
	for _, banned := range []string{"malformed", "likely scanned"} {
		if strings.Contains(res.ForLLM, banned) {
			t.Fatalf("a busy parser must not be reported as %q:\n%s", banned, res.ForLLM)
		}
	}
	if len(res.InspectionPDFs) != 1 {
		t.Fatalf("expected exactly one native PDF item, got %d", len(res.InspectionPDFs))
	}
	// R3-TEST-1: the item must say it was not inspected (wording from
	// docs/troubleshooting.md), so the recovery note cannot call it text-less.
	if got := res.InspectionPDFs[0].LocalFailure; got != "parser busy, try again later" {
		t.Fatalf("native item LocalFailure = %q, want %q", got, "parser busy, try again later")
	}
	if res.InspectionPDFs[0].HasText {
		t.Fatalf("a PDF that was not inspected must not claim HasText")
	}
	if !bytes.Equal(res.InspectionPDFs[0].Bytes, venue) {
		t.Fatalf("native item must carry the exact file bytes")
	}

	paged := tool.Execute(context.Background(), map[string]any{"path": path, "offset": float64(0), "length": float64(10)})
	if !paged.IsError || !strings.Contains(paged.ForLLM, "(parser busy, try again later)") {
		t.Fatalf("a paged read must fail with the busy reason, got IsError=%v: %s", paged.IsError, paged.ForLLM)
	}
	if strings.Contains(paged.ForLLM, "malformed") {
		t.Fatalf("a busy parser must not be reported as malformed:\n%s", paged.ForLLM)
	}
	if len(paged.InspectionPDFs) != 0 {
		t.Fatalf("a paged read attaches no native item, got %d", len(paged.InspectionPDFs))
	}
}

// RR1 (#1968 re-review): a PDF above the media size setting, read without
// offset/length while the parser is busy, is a TEMPORARY failure of this
// computer, not a property of the file. Oracle: troubleshooting.md and
// security-considerations.md ("parser busy, try again later ... reading the
// file again later can succeed; ... was not judged damaged"). The result must
// stay an error (the file is over the media cap, so no native delivery), carry
// the busy reason with its retry advice, and must not claim the file has no
// extractable text or is damaged/scanned: the venue PDF does have text.
func assertOverCapBusyError(t *testing.T, res *tools.ToolResult) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("an over-media-setting read while the parser is busy must be an error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "parser busy, try again later") {
		t.Fatalf("the busy reason with its retry advice must survive the over-setting path, got:\n%s", res.ForLLM)
	}
	for _, banned := range []string{"no extractable text", "malformed", "likely scanned", "convert its pages"} {
		if strings.Contains(res.ForLLM, banned) {
			t.Fatalf("a busy parser must not be reported with %q (the file has text and is not damaged):\n%s", banned, res.ForLLM)
		}
	}
	if len(res.InspectionPDFs) != 0 {
		t.Fatalf("a PDF over the media setting must never be sent natively, got %d items", len(res.InspectionPDFs))
	}
}

func TestReadFile_PDFOverMediaSettingWhileBusyKeepsTheBusyReason(t *testing.T) {
	venue := docextract.MakeVenuePDFForTest(t)
	docextract.HoldAllPDFParseSlotsForTest(t, venue)

	ws := t.TempDir()
	path := filepath.Join(ws, "venue.pdf")
	if err := os.WriteFile(path, venue, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := tools.NewReadFileTool(ws, false, tools.MaxReadFileSize)
	tool.SetMaxInspectionImageBytes(len(venue) - 1)

	assertOverCapBusyError(t, tool.Execute(context.Background(), map[string]any{"path": path}))
}

func TestLibraryRead_PDFOverMediaSettingWhileBusyKeepsTheBusyReason(t *testing.T) {
	venue := docextract.MakeVenuePDFForTest(t)
	docextract.HoldAllPDFParseSlotsForTest(t, venue)

	ws := t.TempDir()
	lib := filepath.Join(ws, ".library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "venue.pdf"), venue, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := tools.NewLibraryReadTool(ws, true, tools.MaxReadFileSize)
	tool.SetMaxInspectionImageBytes(len(venue) - 1)

	assertOverCapBusyError(t, tool.Execute(context.Background(), map[string]any{"path": "venue.pdf"}))
}
