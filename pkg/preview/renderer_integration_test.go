package preview_test

import (
	"bytes"
	"context"
	"image/color"
	"image/png"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// T-02: real registered pdfium program; geometry and pixels derive from
// the vector PDF fixture, not a PNG captured from the same renderer.
func TestPreviewRenderer_RealPagesGeometryAndDistinctPixelControl(t *testing.T) {
	svc := officepreview.Service(t, false, true)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer adm.Release()
	result, err := svc.Preview(context.Background(), adm, preview.Request{Name: "colors.pdf", Ext: ".pdf", Data: officepreview.PDF(2), First: 1, Last: 2})
	require.NoError(t, err)
	require.Equal(t, "rendered", result.Validity)
	require.Empty(t, result.RenderReason)
	require.Equal(t, 2, result.PageCount)
	require.Len(t, result.Pages, 2)
	refs := []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}}
	for i, page := range result.Pages {
		require.Equal(t, i+1, page.Number)
		require.Equal(t, 400, page.Width)
		require.Equal(t, 200, page.Height)
		im, decodeErr := png.Decode(bytes.NewReader(page.PNG))
		require.NoError(t, decodeErr)
		require.Equal(t, 400, im.Bounds().Dx())
		require.Equal(t, 200, im.Bounds().Dy())
		mismatches, wrongPageMatches := 0, 0
		// Text is at the bottom margin; this central reference rectangle is
		// mathematically a uniform fill. A different-page reference must fail.
		for y := 50; y < 150; y++ {
			for x := 100; x < 300; x++ {
				r, g, b, a := im.At(x, y).RGBA()
				rr, rg, rb, ra := refs[i].RGBA()
				wr, wg, wb, wa := refs[1-i].RGBA()
				if r != rr || g != rg || b != rb || a != ra {
					mismatches++
				}
				if r == wr && g == wg && b == wb && a == wa {
					wrongPageMatches++
				}
			}
		}
		require.Zero(t, mismatches, "page %d differs from the independently defined color rectangle", i+1)
		require.Zero(t, wrongPageMatches, "different-page reference control must not match")
	}
}
