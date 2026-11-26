package preview_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// T-49 / B-32. A form field whose value is FORM-42 is visible against the same
// widget with an empty value. The two PDFs share the widget (same rectangle,
// same flags, no appearance stream). They differ only in /V.
//
// A static appearance does not prove the value. omnipus_pdfium.c::emit_page
// always paints annotations (FPDF_ANNOT) and, when the render flag bit 0 is
// set, also calls FPDF_FFLDraw. A shared /AP is painted for any /V, so the
// pixels would match. These fixtures have no /AP and NeedAppearances true, so
// the ink in the field is the value pdfium generates. The empty system-font
// provider still draws the standard Helvetica face named in /DA.
//
// Geometry is not taken from a rendered PNG. The page is 200 x 100 pt.
// omnipus_pdfium.c::emit_page starts at scale 2 and shrinks only when the long
// edge would pass MaxLongEdge (2000). 200*2 = 400, so the PNG is 400 x 200.
// The field window is the whole widget, because the value is drawn as glyphs
// inside it rather than as a solid fill.

const (
	formPageW = 200
	formPageH = 100
	formScale = 2
	// Widget /Rect [20 40 120 80].
	formWidgetX = 20
	formWidgetY = 40
	formWidgetW = 100
	formWidgetH = 40
	formValue   = "FORM-42"
)

func TestPreviewRenderer_FilledFormValueIsVisibleAgainstEmptyField(t *testing.T) {
	filled := acroFormPDF(t, formValue)
	empty := acroFormPDF(t, "")
	require.Equal(t, 1, bytes.Count(filled, []byte("("+formValue+")")), "the value is the field's /V and nothing else")
	require.False(t, bytes.Contains(empty, []byte(formValue)), "the empty control must not carry the value")
	require.False(t, bytes.Contains(filled, []byte("/AP")), "a static appearance would paint the same pixels for any /V")
	require.False(t, bytes.Contains(empty, []byte("/AP")))
	require.True(t, bytes.Contains(filled, []byte("/Rect [20 40 120 80]")))
	require.True(t, bytes.Contains(empty, []byte("/Rect [20 40 120 80]")))

	svc := officepreview.Service(t, false, true)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer adm.Release()

	filledImg := renderFormPage(t, svc, adm, "filled-form.pdf", filled)
	emptyImg := renderFormPage(t, svc, adm, "empty-form.pdf", empty)

	// PDF y is bottom-up; PNG y is top-down. Widget [20 40 120 80] on a
	// 100 pt page at scale 2 is PNG x [40, 240), y [40, 120).
	pngX0 := formWidgetX * formScale
	pngX1 := (formWidgetX + formWidgetW) * formScale
	pngY0 := (formPageH - (formWidgetY + formWidgetH)) * formScale
	pngY1 := (formPageH - formWidgetY) * formScale
	require.Equal(t, 40, pngX0)
	require.Equal(t, 240, pngX1)
	require.Equal(t, 40, pngY0)
	require.Equal(t, 120, pngY1)

	white := color.RGBA{R: 255, G: 255, B: 255, A: 255} // page fill "1 1 1 rg"
	assertRegion(t, emptyImg, pngX0, pngY0, pngX1, pngY1, white, "empty field shows no value")
	filledInk := countOther(filledImg, pngX0, pngY0, pngX1, pngY1, white)
	require.Positive(t, filledInk, "filled field shows the value FORM-42")
	require.NotEqual(t, filledInk, 0, "T-49: the value and the empty field must differ in the field region")

	// A corner outside the widget stays the page white on both, so the
	// difference is the field and not a full-page tint. pdf (10, 10).
	cornerX := 10 * formScale
	cornerY := (formPageH - 10) * formScale
	require.Equal(t, 20, cornerX)
	require.Equal(t, 180, cornerY)
	assertRegion(t, filledImg, cornerX, cornerY, cornerX+1, cornerY+1, white, "filled page outside the field")
	assertRegion(t, emptyImg, cornerX, cornerY, cornerX+1, cornerY+1, white, "empty page outside the field")
}

func renderFormPage(t *testing.T, svc *preview.Service, adm *preview.Admission, name string, pdf []byte) image.Image {
	t.Helper()
	result, err := svc.Preview(context.Background(), adm, preview.Request{
		Name: name, Ext: ".pdf", Data: pdf, First: 1, Last: 1,
	})
	require.NoError(t, err)
	require.Equal(t, "rendered", result.Validity)
	require.Empty(t, result.RenderReason)
	require.Equal(t, 1, result.PageCount)
	require.Len(t, result.Pages, 1)
	page := result.Pages[0]
	require.Equal(t, 1, page.Number)
	require.Equal(t, formPageW*formScale, page.Width)
	require.Equal(t, formPageH*formScale, page.Height)
	im, decErr := png.Decode(bytes.NewReader(page.PNG))
	require.NoError(t, decErr)
	require.Equal(t, formPageW*formScale, im.Bounds().Dx())
	require.Equal(t, formPageH*formScale, im.Bounds().Dy())
	return im
}

func assertRegion(t *testing.T, im image.Image, x0, y0, x1, y1 int, want color.RGBA, label string) {
	t.Helper()
	mismatches := countOther(im, x0, y0, x1, y1, want)
	require.Zero(t, mismatches, "%s: %d pixels differ from %#v", label, mismatches, want)
}

func countOther(im image.Image, x0, y0, x1, y1 int, want color.RGBA) int {
	mismatches := 0
	wr, wg, wb, wa := want.RGBA()
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := im.At(x, y).RGBA()
			if r != wr || g != wg || b != wb || a != wa {
				mismatches++
			}
		}
	}
	return mismatches
}

func acroFormPDF(t *testing.T, value string) []byte {
	t.Helper()
	if strings.ContainsAny(value, "()\\") {
		t.Fatalf("fixture value %q is not a plain PDF literal", value)
	}
	content := "1 1 1 rg 0 0 200 100 re f\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm 7 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [4 0 R] >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R /Annots [6 0 R] >>",
		pdfStream(content),
		fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Tx /T (amount) /V (%s) /Rect [20 40 120 80] /F 4 /DA (/Helv 18 Tf 0 0 0 rg) /P 4 0 R >>", value),
		"<< /Fields [6 0 R] /NeedAppearances true /DA (/Helv 18 Tf 0 0 0 rg) /DR << /Font << /Helv 3 0 R >> >> >>",
	}
	return pdfDocument(objects)
}

func pdfStream(body string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(body), body)
}

func pdfDocument(objects []string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
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
