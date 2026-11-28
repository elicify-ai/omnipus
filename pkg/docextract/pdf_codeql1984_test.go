package docextract

// PR #1984 CodeQL alert go/incorrect-integer-conversion at inspect_pdf.go
// (info.Pages = int(count.Int64())). PDFInfo.Pages is the page tree's /Count,
// 0 = unknown. A /Count outside the int32 range is malformed like a missing
// or non-positive one: Pages stays 0 and the text is still read.

import (
	"fmt"
	"testing"
)

// codeqlCountPDF is a one-page document whose page tree states /Count count.
func codeqlCountPDF(count string) []byte {
	const content = "BT /F1 12 Tf 72 720 Td (counted) Tj ET"
	return assemblePDF("%PDF-1.4", []pdfObj{
		{num: 1, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`},
		{num: 2, body: fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)},
		{num: 3, body: fmt.Sprintf(`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R >> >> /Contents 2 0 R >>`, fixturePagesNum)},
		{num: fixturePagesNum, body: fmt.Sprintf(`<< /Type /Pages /Count %s /Kids [3 0 R] >>`, count)},
		fixtureCatalogObject(),
	}, "")
}

func TestCodeQL1984_InspectPDFCountRange(t *testing.T) {
	cases := []struct {
		count string
		want  int
	}{
		{"1", 1},
		{"2147483647", 2147483647}, // the largest int32 is still a stated count
		{"2147483648", 0},          // int32 max + 1: malformed, unknown
		{"1099511627776", 0},       // 2^40
		{"4294967297", 0},          // 2^32 + 1: truncated to 32 bits it would read 1
		{"0", 0},
		{"-5", 0},
	}
	for _, tc := range cases {
		t.Run("Count "+tc.count, func(t *testing.T) {
			info, err := InspectPDF(codeqlCountPDF(tc.count))
			if err != nil {
				t.Fatalf("a readable PDF must not error, got: %v", err)
			}
			if info.Pages != tc.want {
				t.Fatalf("Pages = %d, want %d", info.Pages, tc.want)
			}
			if info.Text != "counted" {
				t.Fatalf("Text = %q, want %q", info.Text, "counted")
			}
		})
	}
}
