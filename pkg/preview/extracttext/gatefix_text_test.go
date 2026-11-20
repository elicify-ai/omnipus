package extracttext

import (
	"bytes"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/require"
)

// G3 / SF-02. A PDF that opens, but whose first page's text extraction fails,
// must be a typed failure. office-preview spec AD-15: the scanned-document
// sentence is only for a page that really has no text. An extractor error is
// not that page.
func TestExtractText_PageFailureIsNotEmptySuccess(t *testing.T) {
	pdf := gatefixTwoPagePDF("Tf\n", "BT /F1 12 Tf 10 10 Td (GATEFIX_PAGE2_SENTINEL) Tj ET\n")
	var out bytes.Buffer
	var parentGone atomic.Bool
	req := protocol.Request{Op: protocol.OpText, FirstPage: 1, LastPage: 2}
	code := serve(req, pdf, &out, &parentGone)
	resp, readErr := protocol.ReadResponse(bytes.NewReader(out.Bytes()), req, nil)
	if code == 0 && readErr == nil {
		page1 := ""
		if len(resp.Text) > 0 {
			page1 = resp.Text[0].Text
		}
		t.Fatalf("extract-text exited 0 with a successful text response after page 1 failed (page 1 text %q); SF-02: a page-text error must be a typed failure, not an empty successful page", page1)
	}
	msg := ""
	if readErr != nil {
		msg = readErr.Error()
	}
	require.Contains(t, msg, "page 1", "the typed failure must name the page whose text could not be extracted")
}

// gatefixTwoPagePDF is an independently built PDF: page 1 carries the given
// content stream (a bare Tf, which the extractor rejects), page 2 carries
// extractable sentinel text. The xref is computed from the bytes written.
func gatefixTwoPagePDF(page1, page2 string) []byte {
	pages := []string{page1, page2}
	// Catalog, pages node, font, then one page object and one content stream
	// per page. The capacity is that count so the later appends do not grow.
	objects := make([]string, 0, 3+2*len(pages))
	objects = append(objects,
		"<< /Type /Catalog /Pages 2 0 R >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	)
	kids := ""
	for i, stream := range pages {
		pageObj := 4 + 2*i
		kids += fmt.Sprintf("%d 0 R ", pageObj)
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", pageObj+1))
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count 2 /Kids [%s] >>", kids)
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
