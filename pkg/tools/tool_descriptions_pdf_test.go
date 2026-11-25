package tools

// Spec #1968 (read-file native PDF) — T28, docs level. Labelled A: FAILS BY
// ASSERTION on today's code, where read_file's description still claims PDFs
// are "automatically decoded to plain text" (the per-model native behaviour
// supersedes that) and library_read's description does not mention PDFs at
// all.
//
// Oracle: §5.2 FR-9 — "Both tool descriptions and shared offset/length help
// describe native viewing per model, limits, no-text outcomes, re-read".
// Wording is the implementer's, so the assertions pin the spec's own
// vocabulary, not sentences.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// descriptionStatesNativePerModel checks the FR-9 native-viewing-per-model
// concept via the spec's vocabulary ("PDF-capable models", "models that
// support PDFs").
func descriptionStatesNativePerModel(desc string) bool {
	return strings.Contains(desc, "PDF-capable") || strings.Contains(desc, "support PDFs")
}

// descriptionStatesNoTextOutcome checks the FR-9 no-text concept ("likely
// scanned", "no extractable text").
func descriptionStatesNoTextOutcome(desc string) bool {
	return strings.Contains(desc, "scanned") || strings.Contains(desc, "no extractable text")
}

func TestToolDescriptions_PDF(t *testing.T) {
	t.Run("read_file describes native viewing, limits, no-text and re-read", func(t *testing.T) {
		desc := NewReadFileTool(t.TempDir(), false, MaxReadFileSize).Description()

		if !strings.Contains(desc, "PDF") {
			t.Fatal("read_file description does not mention PDFs at all")
		}
		// FR-9: native viewing per model — the old blanket "automatically
		// decoded to plain text" is exactly what the per-model decision
		// replaces; the description must name the model-dependent native
		// path and must not keep the blanket claim.
		if !descriptionStatesNativePerModel(desc) {
			t.Fatalf("description must state that PDFs are viewed natively by models that support them:\n%s", desc)
		}
		if strings.Contains(desc, "automatically decoded to plain text") {
			t.Fatalf("description still claims PDFs are always decoded to plain text (superseded by the per-model native path):\n%s", desc)
		}
		// FR-9: limits (the media size setting caps what is sent natively).
		if !strings.Contains(desc, "limit") {
			t.Fatalf("description must state the size limit that governs native PDF viewing:\n%s", desc)
		}
		// FR-9: no-text outcomes.
		if !descriptionStatesNoTextOutcome(desc) {
			t.Fatalf("description must state what a PDF without extractable text (scanned) looks like:\n%s", desc)
		}
		// FR-9: re-read (the model can page the extracted text afterwards).
		if !strings.Contains(desc, "re-read") {
			t.Fatalf("description must tell the model it can re-read the PDF for its text:\n%s", desc)
		}
	})

	t.Run("library_read describes the PDF behaviour too", func(t *testing.T) {
		desc := NewLibraryReadTool(t.TempDir(), false, MaxReadFileSize).Description()

		if !strings.Contains(desc, "PDF") {
			t.Fatalf("library_read description does not mention PDFs at all:\n%s", desc)
		}
		if !descriptionStatesNativePerModel(desc) {
			t.Fatalf("library_read description must state per-model native PDF viewing:\n%s", desc)
		}
		if !descriptionStatesNoTextOutcome(desc) {
			t.Fatalf("library_read description must state the no-text outcome:\n%s", desc)
		}
		// FR-9 applies to BOTH tools: limits and re-read must be described
		// here too (F14: the library_read check omitted them).
		if !strings.Contains(desc, "limit") {
			t.Fatalf("library_read description must state the size limit that governs native PDF viewing:\n%s", desc)
		}
		if !strings.Contains(desc, "re-read") {
			t.Fatalf("library_read description must tell the model it can re-read the PDF for its text:\n%s", desc)
		}
	})

	t.Run("shared offset/length help describe PDF paging and its no-text outcome", func(t *testing.T) {
		offsetDesc := readerOffsetParamDesc
		lengthDesc := readerLengthParamDesc

		// The paging contract for PDFs (characters of extracted text) is
		// already described today and must survive.
		for _, d := range []string{offsetDesc, lengthDesc} {
			if !strings.Contains(d, "PDF") {
				t.Fatalf("shared param help must keep naming PDFs:\n%s", d)
			}
			if !strings.Contains(d, "character") {
				t.Fatalf("shared param help must keep stating character units for PDFs:\n%s", d)
			}
		}
		// FR-9: the help must also state the no-text outcome for PDFs
		// (offset/length on a PDF without text errors instead of paging).
		if !descriptionStatesNoTextOutcome(offsetDesc) {
			t.Fatalf("offset help must state the PDF no-text outcome:\n%s", offsetDesc)
		}
		if !descriptionStatesNoTextOutcome(lengthDesc) {
			t.Fatalf("length help must state the PDF no-text outcome:\n%s", lengthDesc)
		}
	})
}

// #1968 fix round 8: read_file's description names the incomplete-sheet
// notice the extractor appends, and its row in the reference page carries the
// same sentence (the cmd/docsref gate holds the whole row in step).
func TestToolDescriptions_ReadFileNamesIncompleteNotice(t *testing.T) {
	const notice = "[incomplete: <count> sheet(s) could not be read and are missing; first:"
	desc := NewReadFileTool(t.TempDir(), false, MaxReadFileSize).Description()
	if !strings.Contains(desc, notice) {
		t.Fatalf("read_file description must contain %q:\n%s", notice, desc)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "built-in-tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, line := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(line, "| `read_file` |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("docs/reference/built-in-tools.md has no read_file row")
	}
	if !strings.Contains(row, notice) {
		t.Fatalf("the read_file row of built-in-tools.md must contain %q", notice)
	}
}
