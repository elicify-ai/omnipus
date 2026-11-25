package tools

// PDFTemporaryFailureReason returns the N3 reason only for the two causes
// that describe this computer's load (docs/troubleshooting.md: "parser busy,
// try again later" and "timed out" do not judge the file damaged), and ""
// for success and for every property of the file.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/docextract"
)

func TestPDFTemporaryFailureReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"busy", docextract.ErrPDFBusy, "parser busy, try again later"},
		{"timed out", docextract.ErrPDFTimedOut, "timed out"},
		{"busy wrapped once", fmt.Errorf("inspect: %w", docextract.ErrPDFBusy), "parser busy, try again later"},
		{"busy wrapped twice", fmt.Errorf("a: %w", fmt.Errorf("b: %w", docextract.ErrPDFBusy)), "parser busy, try again later"},
		{"timed out wrapped", fmt.Errorf("inspect: %w", docextract.ErrPDFTimedOut), "timed out"},
		{"busy in a join", errors.Join(errors.New("other"), docextract.ErrPDFBusy), "parser busy, try again later"},
		{"malformed", docextract.ErrPDFMalformed, ""},
		{"malformed wrapped", fmt.Errorf("%w: bad xref", docextract.ErrPDFMalformed), ""},
		{"encrypted", docextract.ErrPDFEncrypted, ""},
		{"unsupported version", docextract.ErrPDFUnsupportedVersion, ""},
		{"unrelated error", errors.New("disk on fire"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PDFTemporaryFailureReason(tc.err); got != tc.want {
				t.Fatalf("PDFTemporaryFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
