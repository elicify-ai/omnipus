package agent

// #1968 RR1 (agent half): the provider-recovery note of a demoted PDF must
// keep "this PDF was not read right now" apart from "this PDF has no text".
// Oracle: docs/operations/security-considerations.md and docs/troubleshooting.md
// ("parser busy, try again later" and "timed out" describe the computer, not
// the file; reading it again later can succeed) and spec rev 7 P1/P2 wording.
// The expected strings below are spelled out here, not taken from the code.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	recoveryNoteBusy     = "[native PDF rejected by the provider; it could not be read locally (parser busy, try again later), content unavailable]"
	recoveryNoteTimedOut = "[native PDF rejected by the provider; it could not be read locally (timed out), content unavailable]"
	recoveryNoteNoText   = "[native PDF rejected by the provider; no extractable text, content unavailable]"
	recoveryNoteText     = "[native PDF rejected by the provider; extracted text shown]"
)

// swapChatPDFInspector makes chat PDF inspection return what fn returns and
// restores the real inspector when the test ends.
func swapChatPDFInspector(t *testing.T, fn func([]byte) (docextract.PDFInfo, error)) {
	t.Helper()
	orig := chatPDFInspector
	chatPDFInspector = fn
	t.Cleanup(func() { chatPDFInspector = orig })
}

// inspectedChatCandidate builds a chat PDF candidate whose single inspection
// ran through the (swapped) inspector, with the retry budget spent.
func inspectedChatCandidate(t *testing.T) *pdfCandidate {
	t.Helper()
	dataURL := protocoltypes.PDFDataURLPrefix + "JVBERi0xLjQK" // "%PDF-1.4\n"
	c := &pdfCandidate{
		dataURL:  dataURL,
		chat:     &chatPDF{name: "venue.pdf"},
		decision: pdfBudgetSpent,
	}
	c.chat.inspect(dataURL)
	return c
}

func TestChatPDFNote_BudgetSpent_KeepsTheTemporaryReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
		// reasonInNotice is the N3 wording the persisted injection must keep.
		reasonInNotice string
	}{
		{"parser busy", fmt.Errorf("%w: earlier parses are still stuck", docextract.ErrPDFBusy), recoveryNoteBusy, "parser busy, try again later"},
		{"timed out", fmt.Errorf("%w", docextract.ErrPDFTimedOut), recoveryNoteTimedOut, "timed out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			swapChatPDFInspector(t, func([]byte) (docextract.PDFInfo, error) {
				return docextract.PDFInfo{}, tc.err
			})
			c := inspectedChatCandidate(t)

			got := chatPDFNote(c)
			if got != tc.want {
				t.Fatalf("recovery note = %q, want exactly %q", got, tc.want)
			}
			if strings.Contains(got, "no extractable text") {
				t.Fatalf("a PDF that was not read must not be called text-less: %q", got)
			}
			if !strings.Contains(c.chat.injection, "could not be read locally ("+tc.reasonInNotice+")") {
				t.Fatalf("the persisted N3 notice must keep the reason, got %q", c.chat.injection)
			}
			if c.chat.hasText {
				t.Fatal("hasText must stay false when nothing was read")
			}
		})
	}
}

// Controls: only a temporary failure changes the note.
func TestChatPDFNote_BudgetSpent_Controls(t *testing.T) {
	t.Run("malformed keeps the no-text note and no failure marker", func(t *testing.T) {
		swapChatPDFInspector(t, func([]byte) (docextract.PDFInfo, error) {
			return docextract.PDFInfo{}, fmt.Errorf("%w: bad xref", docextract.ErrPDFMalformed)
		})
		c := inspectedChatCandidate(t)
		if c.chat.localFailure != "" {
			t.Fatalf("a malformed file is a property of the file, not a temporary failure: localFailure=%q", c.chat.localFailure)
		}
		if got := chatPDFNote(c); got != recoveryNoteNoText {
			t.Fatalf("malformed control: note = %q, want %q", got, recoveryNoteNoText)
		}
	})
	t.Run("a scanned file (no text, no error) keeps the no-text note", func(t *testing.T) {
		swapChatPDFInspector(t, func([]byte) (docextract.PDFInfo, error) {
			return docextract.PDFInfo{Pages: 3}, nil
		})
		c := inspectedChatCandidate(t)
		if got := chatPDFNote(c); got != recoveryNoteNoText {
			t.Fatalf("scanned control: note = %q, want %q", got, recoveryNoteNoText)
		}
	})
	t.Run("a file with text keeps the extracted-text note", func(t *testing.T) {
		swapChatPDFInspector(t, func([]byte) (docextract.PDFInfo, error) {
			return docextract.PDFInfo{Pages: 1, Text: "Venue: Grand Hotel Amsterdam, 12 March"}, nil
		})
		c := inspectedChatCandidate(t)
		if got := chatPDFNote(c); got != recoveryNoteText {
			t.Fatalf("text control: note = %q, want %q", got, recoveryNoteText)
		}
	})
}

func TestToolPDFNote_BudgetSpent_KeepsTheTemporaryReason(t *testing.T) {
	tests := []struct {
		name string
		item tools.InspectionPDF
		want string
	}{
		{"busy", tools.InspectionPDF{Name: "venue.pdf", HasText: false, LocalFailure: "parser busy, try again later"}, recoveryNoteBusy},
		{"timed out", tools.InspectionPDF{Name: "venue.pdf", HasText: false, LocalFailure: "timed out"}, recoveryNoteTimedOut},
		{"no failure marker keeps the no-text note", tools.InspectionPDF{Name: "venue.pdf", HasText: false}, recoveryNoteNoText},
		{"text keeps the extracted-text note", tools.InspectionPDF{Name: "venue.pdf", HasText: true}, recoveryNoteText},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &pdfCandidate{tool: true, item: tc.item, decision: pdfBudgetSpent}
			got := toolPDFNote(c)
			if got != tc.want {
				t.Fatalf("tool note = %q, want exactly %q", got, tc.want)
			}
			if tc.item.LocalFailure != "" && strings.Contains(got, "no extractable text") {
				t.Fatalf("a PDF that was not read must not be called text-less: %q", got)
			}
		})
	}
}

// The failure marker must not leak into the other decisions: a model without
// a PDF route (N2) and a denied recheck keep their own notes.
func TestPDFNotes_TemporaryFailureDoesNotChangeOtherDecisions(t *testing.T) {
	item := tools.InspectionPDF{Name: "venue.pdf", LocalFailure: "parser busy, try again later"}
	if got := toolPDFNote(&pdfCandidate{tool: true, item: item, decision: pdfNotCapable}); got != pdfNoteNotCapable {
		t.Fatalf("not-capable note changed: %q", got)
	}
	if got := toolPDFNote(&pdfCandidate{tool: true, item: item, decision: pdfAccessDenied}); got != pdfNoteAccessDenied {
		t.Fatalf("access-denied note changed: %q", got)
	}
}
