//go:build darwin || linux

package preview

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/require"
)

// G3 / SF-02, through the real Preview path (extract-text hosted by this
// test binary). AD-15's scanned-document sentence is only for a page that
// really has no text. A page whose extraction fails must be named as a
// failure, not delivered as a blank successful page.
func TestOfficePreview_TextFailureIsNotABlankPage(t *testing.T) {
	svc := previewUnitService(t)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer adm.Release()
	pdf := gatefixStreamsPDF([]string{
		"Tf\n",
		"BT /F1 12 Tf 10 10 Td (GATEFIX_PAGE2_SENTINEL) Tj ET\n",
	})
	res, previewErr := svc.Preview(context.Background(), adm, Request{
		Name: "broken-text.pdf", Ext: ".pdf", Data: pdf, First: 1, Last: 2,
	})
	// R3-F3: this fixture has a failed page followed by an extractable page.
	// It must return a partial result; a top-level typed error cannot pass
	// before the structural page-number assertions below have been reached.
	require.NoError(t, previewErr, "SF-02/T5: the two-page fixture must return partial text, not bypass its page-number checks through a typed error")
	require.NotNil(t, res, "SF-02/T5: both requested pages need a result to number")
	var body strings.Builder
	for _, page := range res.Text {
		fmt.Fprintf(&body, "page %d: %s\n", page.Number, page.Text)
	}
	text := body.String()
	require.Contains(t, text, "text extraction failed", "SF-02: the model text must say extraction failed, not present a blank page")
	require.Contains(t, text, "page 1")
	require.NotContains(t, text, "may be scanned", "a failed extraction is not a scanned page (AD-15)")
	// T5 (gate round 2, CHECK): the text above can carry its own page
	// literal, so a mutated entry number would hide behind it. The failed
	// entry must STRUCTURALLY carry the failed page's number, and the
	// successful one the other requested page — the spec's page numbering is
	// 1-based and the request asked for pages 1-2 (expected values from the
	// request, not from the code).
	var failedNumber, sentinelNumber uint32
	for _, page := range res.Text {
		if strings.Contains(page.Text, "text extraction failed") {
			failedNumber = page.Number
			continue
		}
		if strings.Contains(page.Text, "GATEFIX_PAGE2_SENTINEL") {
			sentinelNumber = page.Number
		}
	}
	require.Equal(t, uint32(1), failedNumber, "SF-02/T5: the extraction-failed entry is page 1, the first requested page — not a shifted number")
	require.Equal(t, uint32(2), sentinelNumber, "SF-02/T5: the sentinel entry is page 2, the second requested page")
}

// G4 / CR F6, SF-03. AD-6 discloses a cut at the 4 MiB text budget. A single
// page that exceeds the ~1 MiB frame must not drop later pages, and the cut
// must be disclosed even when the bytes that arrived are under 4 MiB.
func TestOfficePreview_PageFrameCutKeepsLaterPagesAndSaysSo(t *testing.T) {
	svc := previewUnitService(t)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer adm.Release()
	// One byte over the frame room (frame ceiling minus the 4-byte page header).
	huge := strings.Repeat("A", protocol.MaxTextFrameBytes)
	pdf := gatefixStreamsPDF([]string{
		"BT /F1 12 Tf (" + huge + ") Tj ET\n",
		"BT /F1 12 Tf (GATEFIX_PAGE2_AFTER_CUT) Tj ET\n",
	})
	res, previewErr := svc.Preview(context.Background(), adm, Request{
		Name: "long-text.pdf", Ext: ".pdf", Data: pdf, First: 1, Last: 2,
	})
	require.NoError(t, previewErr)
	var body strings.Builder
	for _, page := range res.Text {
		fmt.Fprintf(&body, "page %d: %s\n", page.Number, page.Text)
	}
	text := body.String()
	require.Contains(t, text, "GATEFIX_PAGE2_AFTER_CUT", "a per-page frame cut must not drop later requested pages")
	require.True(t, res.TextTruncated, "AD-6: a cut must be disclosed; inferring it only at 4 MiB hides a 1 MiB frame cut")
}

// gatefixStreamsPDF builds a PDF whose page content streams are the given
// strings, in order. The xref is computed from the bytes written.
func gatefixStreamsPDF(streams []string) []byte {
	// Catalog, pages node, font, then one page object and one content stream
	// per page. The capacity is that count so the later appends do not grow.
	objects := make([]string, 0, 3+2*len(streams))
	objects = append(objects,
		"<< /Type /Catalog /Pages 2 0 R >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	)
	var kids strings.Builder
	for i, stream := range streams {
		pageObj := 4 + 2*i
		fmt.Fprintf(&kids, "%d 0 R ", pageObj)
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", pageObj+1))
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(streams), kids.String())
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
