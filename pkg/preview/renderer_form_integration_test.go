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

// T-49 / B-32. A form field whose value is FORM-42 is visible: its appearance
// paints green across the widget, and draws that same value. The negative
// control is the same widget with an empty value and a white appearance.
// The two renders must differ in the field, and only there.
//
// Geometry is not taken from a rendered PNG. The page is 200 x 100 pt, the
// same box as officepreview.PDF. omnipus_pdfium.c::emit_page starts at scale
// 2 and shrinks only when the long edge would pass MaxLongEdge (2000).
// 200*2 = 400, so the PNG is 400 x 200.
//
// The sampled window is inset from the widget and sits above the 10 pt text
// drawn at appearance y 4, so glyph antialiasing is not the oracle. The value
// is still in /V and in the appearance's text operator.

const (
	formPageW = 200
	formPageH = 100
	formScale = 2
	// Widget /Rect [20 40 120 80], appearance BBox [0 0 100 40].
	formWidgetX = 20
	formWidgetY = 40
	// Appearance-space sample: x [10, 90), y [18, 36).
	formSampleX0 = 10
	formSampleX1 = 90
	formSampleY0 = 18
	formSampleY1 = 36
	formValue    = "FORM-42"
)

const formFilledAppearance = "0 1 0 rg\n0 0 100 40 re f\nBT /Helv 10 Tf 0 0 0 rg 4 4 Td (FORM-42) Tj ET\n"
const formEmptyAppearance = "1 1 1 rg\n0 0 100 40 re f\n"

func TestPreviewRenderer_FilledFormValueIsVisibleAgainstEmptyField(t *testing.T) {
	filled := acroFormPDF(t, formValue, formFilledAppearance)
	empty := acroFormPDF(t, "", formEmptyAppearance)
	require.Equal(t, 2, bytes.Count(filled, []byte("("+formValue+")")), "the value must be both /V and the appearance text")
	require.False(t, bytes.Contains(empty, []byte(formValue)), "the empty control must not carry the value")
	require.True(t, bytes.Contains(filled, []byte("0 1 0 rg")), "the filled appearance must paint green")
	require.False(t, bytes.Contains(empty, []byte("0 1 0 rg")), "the empty appearance must not paint green")
	require.True(t, bytes.Contains(filled, []byte("/Rect [20 40 120 80]")))

	svc := officepreview.Service(t, false, true)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer adm.Release()

	filledImg := renderFormPage(t, svc, adm, "filled-form.pdf", filled)
	emptyImg := renderFormPage(t, svc, adm, "empty-form.pdf", empty)

	// PDF y is bottom-up; PNG y is top-down. These four numbers are the
	// sample window in PNG pixels: x [60, 220), y [48, 84).
	pngX0 := (formWidgetX + formSampleX0) * formScale
	pngX1 := (formWidgetX + formSampleX1) * formScale
	pngY0 := (formPageH - (formWidgetY + formSampleY1)) * formScale
	pngY1 := (formPageH - (formWidgetY + formSampleY0)) * formScale
	require.Equal(t, 60, pngX0)
	require.Equal(t, 220, pngX1)
	require.Equal(t, 48, pngY0)
	require.Equal(t, 84, pngY1)

	green := color.RGBA{G: 255, A: 255}                 // appearance "0 1 0 rg"
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255} // page and empty appearance "1 1 1 rg"
	assertRegion(t, filledImg, pngX0, pngY0, pngX1, pngY1, green, "filled field shows the value appearance")
	assertRegion(t, emptyImg, pngX0, pngY0, pngX1, pngY1, white, "empty field shows no value")
	require.NotEqual(t, filledImg.At(pngX0, pngY0), emptyImg.At(pngX0, pngY0), "T-49: negative control must differ in the field region")

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
	mismatches := 0
	var sample color.Color
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := im.At(x, y).RGBA()
			wr, wg, wb, wa := want.RGBA()
			if r != wr || g != wg || b != wb || a != wa {
				mismatches++
				if sample == nil {
					sample = im.At(x, y)
				}
			}
		}
	}
	require.Zero(t, mismatches, "%s: %d pixels differ from %#v, example %v", label, mismatches, want, sample)
}

func acroFormPDF(t *testing.T, value, appearance string) []byte {
	t.Helper()
	if strings.ContainsAny(value, "()\\") {
		t.Fatalf("fixture value %q is not a plain PDF literal", value)
	}
	content := "1 1 1 rg 0 0 200 100 re f\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm 8 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [4 0 R] >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R /Annots [6 0 R] >>",
		pdfStream(content),
		fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Tx /T (amount) /V (%s) /Rect [20 40 120 80] /F 4 /BS << /W 0 >> /DA (/Helv 10 Tf 0 0 0 rg) /AP << /N 7 0 R >> /P 4 0 R >>", value),
		fmt.Sprintf("<< /Type /XObject /Subtype /Form /FormType 1 /BBox [0 0 100 40] /Resources << /Font << /Helv 3 0 R >> >> /Length %d >>\nstream\n%sendstream", len(appearance), appearance),
		"<< /Fields [6 0 R] /NeedAppearances false /DA (/Helv 10 Tf 0 0 0 rg) /DR << /Font << /Helv 3 0 R >> >> >>",
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
