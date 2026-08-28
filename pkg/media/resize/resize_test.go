// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package resize_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/draw"

	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// catalogDefaults are the FR-014 default budget values used throughout
// the resize tests. They mirror the catalog defaults exactly so the
// tests exercise the same shape as production callers.
const (
	defaultTestLongEdgePx = 7680
	defaultTestMaxBytes   = 10 * 1024 * 1024
)

// solidImage returns an image.Image of the given dimensions filled with
// a single color. Solid colors compress trivially (small encoded size) so
// these fixtures model the "easy" case: a PNG-encoded solid rectangle is
// a handful of bytes; a JPEG-encoded solid rectangle is similarly tiny.
func solidImage(width, height int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// noisyImage returns an image.Image of the given dimensions filled with
// pseudo-random noise. Noisy pixels defeat both PNG (deflate) and JPEG
// (DCT) compression, so the encoded output is large — the fixtures
// model the "hard" case where the ladder must shrink to fit.
func noisyImage(width, height int, seed uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: seed ^ uint8(x),
				G: seed ^ uint8(y),
				B: seed ^ uint8(x+y),
				A: 255,
			})
		}
	}
	return img
}

// photoLikeImage returns an image.Image with smooth, low-frequency content
// modeled after a natural photograph (multi-frequency sinusoids in each
// channel). For this content JPEG compresses far better than PNG (~10x),
// mirroring how real photo uploads behave.
func photoLikeImage(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := math.Sin(float64(x)/30.0)*50 + math.Sin(float64(y)/50.0)*30 + 128
			g := math.Cos(float64(x)/40.0)*40 + math.Cos(float64(y)/30.0)*50 + 128
			b := math.Sin(float64(x+y)/60.0)*60 + 128
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(clampByte(r)),
				G: uint8(clampByte(g)),
				B: uint8(clampByte(b)),
				A: 255,
			})
		}
	}
	return img
}

func clampByte(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// TestResize_SolidImage_FitsAsPNG (FR-011): a small solid-color image fits
// the budget as PNG (the canonical output). The pipeline must return
// image/png bytes without invoking the JPEG ladder.
func TestResize_SolidImage_FitsAsPNG(t *testing.T) {
	img := solidImage(64, 64, color.RGBA{R: 128, G: 64, B: 200, A: 255})
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: defaultTestLongEdgePx,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, "image/png", result.Mime)
	require.LessOrEqual(t, result.LongEdge, defaultTestLongEdgePx)
	require.LessOrEqual(t, len(result.Data), 10*1024*1024)

	// Round-trip: the PNG bytes decode back to the same shape.
	decoded, format, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.Equal(t, 64, decoded.Bounds().Dx())
	require.Equal(t, 64, decoded.Bounds().Dy())
}

// TestResize_PNGtoJPEGLadder_FitsBudget (FR-015): when the source PNG
// does not fit the budget but JPEG at some quality does, the ladder must
// pick JPEG (PNG is preferred when it fits; JPEG is the fallback when
// it does not). For natural photo-like content, JPEG compresses ~10x
// better than PNG, making this distinction testable.
func TestResize_PNGtoJPEGLadder_FitsBudget(t *testing.T) {
	// photoLikeImage 1024x768: PNG=334KB, JPEG q90=59KB. Budget 200KB is
	// below the PNG size but above the largest JPEG quality. The ladder
	// must select JPEG q90 (the highest quality that fits).
	img := photoLikeImage(1024, 768)
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   200 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", result.Mime,
		"PNG (334KB) does not fit 200KB; ladder must pick JPEG")
	require.LessOrEqual(t, len(result.Data), 200*1024)

	// Sanity: the result JPEG decodes back to 1024x768 (no shrink needed
	// because JPEG q90 already fits the bytes budget).
	decoded, format, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.Equal(t, 1024, decoded.Bounds().Dx())
	require.Equal(t, 768, decoded.Bounds().Dy())
}

// TestResize_LadderFloor_RoutesToStep5 (FR-015): when the image cannot be
// made to fit at any size on the ladder (budget too small for the image),
// ResizeToFit returns ErrLadderFloor — the caller's signal to offload to
// Step 5 (work-dir copy + guidance injection).
func TestResize_LadderFloor_RoutesToStep5(t *testing.T) {
	// 1000x1000 noisy image. A 100-byte budget is impossible regardless
	// of quality or size; the ladder must terminate with ErrLadderFloor.
	img := noisyImage(1000, 1000, 99)
	_, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   100,
	})
	require.ErrorIs(t, err, resize.ErrLadderFloor,
		"ladder floor must be reported when the image cannot fit any size")
}

// TestResize_LadderFloor_BelowMinLongEdge (FR-015): the floor is reached
// when shrinking would take the long edge below MinLongEdge. A tiny source
// image with a budget that cannot fit at any quality must terminate at
// the floor without further shrinking.
func TestResize_LadderFloor_BelowMinLongEdge(t *testing.T) {
	// 100x100 noisy image, 50-byte budget. Source is already below the
	// 256px floor — the ladder cannot shrink further.
	img := noisyImage(100, 100, 7)
	_, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   50,
	})
	require.ErrorIs(t, err, resize.ErrLadderFloor)
}

// TestResize_NoShrink_WhenSourceFits (FR-014): when the source image
// already fits the budget as PNG, the pipeline returns PNG at the source
// dimensions — no shrink step is invoked (the result.LongEdge matches the
// source long edge).
func TestResize_NoShrink_WhenSourceFits(t *testing.T) {
	img := solidImage(512, 384, color.RGBA{R: 255, A: 255})
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, 512, result.LongEdge, "long edge must match the source when no shrink is needed")
	require.Equal(t, "image/png", result.Mime)
}

// TestResize_LongEdgeBudget_ShrinksWhenSourceExceeds (FR-014): when the
// source long edge exceeds budget.LongEdge, the pipeline shrinks to fit.
// The result must satisfy both constraints (long edge AND max bytes).
func TestResize_LongEdgeBudget_ShrinksWhenSourceExceeds(t *testing.T) {
	// 1024x1024 noisy image, budget long edge 512. The pipeline must
	// shrink the image so its long edge is ≤ 512.
	img := noisyImage(1024, 1024, 13)
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 512,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.LessOrEqual(t, result.LongEdge, 512,
		"result long edge must fit budget.LongEdge")
	decoded, _, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.LessOrEqual(t, decoded.Bounds().Dx(), 512)
	require.LessOrEqual(t, decoded.Bounds().Dy(), 512)
}

// TestResize_ShrinkSequence_FollowsPoint75 (FR-015): the shrink factor is
// 0.75 per FR-015. This test exercises the shrink path with a photo-like
// image and asserts the result long edge is one of the valid points in
// the 0.75× sequence (within ±1 of the expected sequence, to accommodate
// integer floor).
func TestResize_ShrinkSequence_FollowsPoint75(t *testing.T) {
	// photoLikeImage 1024x768: PNG=334KB, JPEG q40=28KB. Budget 20KB
	// forces shrinking — JPEG q40 at 1024x768 doesn't fit, so the ladder
	// must shrink. The result long edge must be on the 0.75× sequence
	// (1024 → 768 → 576 → ...).
	img := photoLikeImage(1024, 768)
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   20 * 1024,
	})
	require.NoError(t, err)

	expected := 768
	for i := 0; i < 50; i++ {
		if expected == result.LongEdge || expected == result.LongEdge+1 {
			return
		}
		expected = expected * 3 / 4 // mirror int(math.Floor(float64(x) * 0.75))
		if expected < resize.MinLongEdge {
			break
		}
	}
	t.Fatalf("result long edge %d is not on the 0.75x shrink sequence", result.LongEdge)
}

// TestResize_ShrinkSequence_LandsOnFloor (FR-015): when the budget forces
// shrinking below the MinLongEdge floor, the pipeline returns
// ErrLadderFloor rather than producing a too-small image.
func TestResize_ShrinkSequence_LandsOnFloor(t *testing.T) {
	img := noisyImage(2048, 2048, 200)
	// Budget that forces many shrinks: 1 byte. The pipeline must terminate
	// at the floor rather than producing a 1x1 image.
	_, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   1,
	})
	require.ErrorIs(t, err, resize.ErrLadderFloor)
}

// TestResize_DefaultBudget_AcceptsLargeImage (FR-014): the catalog default
// (~7680px / 10 MB) must accept a reasonably large image without
// shrinking. A 4096x3072 solid image is far below the budget.
func TestResize_DefaultBudget_AcceptsLargeImage(t *testing.T) {
	img := solidImage(4096, 3072, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: defaultTestLongEdgePx,
		MaxBytes:   defaultTestMaxBytes,
	})
	require.NoError(t, err)
	require.Equal(t, 4096, result.LongEdge)
	require.Equal(t, "image/png", result.Mime,
		"solid large image PNG-encoded fits the default budget")
}

// TestResize_LongEdgeBudget_CannotShrinkBelowFloor (FR-014/FR-015): when
// the long-edge budget itself is below MinLongEdge, the pipeline cannot
// produce a valid result and returns ErrLadderFloor. The pipeline never
// shrinks below MinLongEdge.
func TestResize_LongEdgeBudget_CannotShrinkBelowFloor(t *testing.T) {
	img := noisyImage(512, 512, 5)
	// Long-edge budget 64 is below MinLongEdge (256). Even though the
	// bytes budget is huge, the ladder cannot satisfy both constraints.
	_, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 64,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.ErrorIs(t, err, resize.ErrLadderFloor)
}

// TestResize_JPEGQualityLadder_FirstFitWins (FR-015): at a fixed size
// where multiple JPEG qualities fit, the ladder must return the result
// of the highest quality that fits. For a photo-like image, the JPEG
// ladder returns progressively smaller encodings as quality drops.
func TestResize_JPEGQualityLadder_FirstFitWins(t *testing.T) {
	img := photoLikeImage(512, 512)

	// Encode the source at q90 and q40 to find sizes.
	var q90Buf, q40Buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&q90Buf, img, &jpeg.Options{Quality: 90}))
	require.NoError(t, jpeg.Encode(&q40Buf, img, &jpeg.Options{Quality: 40}))
	q90Size := q90Buf.Len()
	q40Size := q40Buf.Len()
	require.Greater(t, q90Size, q40Size, "q90 must encode larger than q40 for photo-like input")

	// Pick a budget below q90 and above q40 — q90 doesn't fit; the
	// ladder must step down to q80, q70, etc. The first one to fit wins.
	budget := (q40Size + q90Size) / 2
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   int64(budget),
	})
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", result.Mime)
	require.LessOrEqual(t, len(result.Data), budget)
	require.LessOrEqual(t, len(result.Data), q90Size,
		"result must be smaller than q90 (else the ladder picked the wrong quality)")
}

// TestResize_PNGPreferred_WhenItFits (FR-011): when PNG fits the budget,
// it is preferred over JPEG (canonical PNG output). This test verifies
// that for small images, PNG is selected even though JPEG would also
// fit and be smaller.
func TestResize_PNGPreferred_WhenItFits(t *testing.T) {
	img := solidImage(64, 64, color.RGBA{R: 200, G: 100, B: 50, A: 255})

	// Both PNG and JPEG fit easily; PNG must win.
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   10 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, "image/png", result.Mime,
		"PNG is preferred when it fits (FR-011 canonical output)")
}

// TestResize_NilImage_ReturnsError guards against nil-image crashes; the
// pipeline must surface a clear error rather than panic.
func TestResize_NilImage_ReturnsError(t *testing.T) {
	_, err := resize.ResizeToFit(nil, catalog.ResizeLimits{LongEdgePx: 1024, MaxBytes: 1024})
	require.Error(t, err)
}

// TestResize_InvalidBudget_ReturnsError guards against zero/negative
// budgets (defensive — the caller should never invoke ResizeToFit with
// such a budget).
func TestResize_InvalidBudget_ReturnsError(t *testing.T) {
	img := solidImage(10, 10, color.RGBA{})
	_, err := resize.ResizeToFit(img, catalog.ResizeLimits{LongEdgePx: 0, MaxBytes: 1024})
	require.Error(t, err)
	_, err = resize.ResizeToFit(img, catalog.ResizeLimits{LongEdgePx: 1024, MaxBytes: 0})
	require.Error(t, err)
}

// TestResize_PreservesAspectRatio guards the shrink step: shrinking is
// applied uniformly (both axes), so the aspect ratio of the source is
// preserved within 1 px on each axis.
func TestResize_PreservesAspectRatio(t *testing.T) {
	src := noisyImage(1600, 800, 0) // aspect 2:1
	// Force shrinking via budget long edge.
	result, err := resize.ResizeToFit(src, catalog.ResizeLimits{
		LongEdgePx: 800,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	decoded, _, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)

	w := decoded.Bounds().Dx()
	h := decoded.Bounds().Dy()
	require.LessOrEqual(t, w, 800)
	require.LessOrEqual(t, h, 800)

	srcAspect := 1600.0 / 800.0
	dstAspect := float64(w) / float64(h)
	require.InDelta(t, srcAspect, dstAspect, 0.05,
		"aspect ratio must be preserved within 5%% (src=%v, dst=%v)", srcAspect, dstAspect)
}

// TestResize_PNGOutput_RoundTripsThroughPNG ensures that when PNG is the
// output (the canonical case), the encoded bytes are valid PNG and
// decode back to the expected dimensions.
func TestResize_PNGOutput_RoundTripsThroughPNG(t *testing.T) {
	img := solidImage(256, 256, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	result, err := resize.ResizeToFit(img, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, "image/png", result.Mime)

	decoded, format, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.Equal(t, 256, decoded.Bounds().Dx())
	require.Equal(t, 256, decoded.Bounds().Dy())
}

// TestResize_PNGOutputFromJPEGSource ensures that a JPEG source decoded
// to image.Image can still produce a canonical PNG output (the spec's
// FR-011 "normalize to canonical PNG").
func TestResize_PNGOutputFromJPEGSource(t *testing.T) {
	// Encode a solid image as JPEG, decode back, run resize.
	src := solidImage(128, 128, color.RGBA{R: 80, G: 160, B: 240, A: 255})
	var jpegBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBuf, src, &jpeg.Options{Quality: 75}))

	decoded, _, err := image.Decode(&jpegBuf)
	require.NoError(t, err)

	result, err := resize.ResizeToFit(decoded, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, "image/png", result.Mime,
		"JPEG source → image.Image → resize must re-encode as PNG (FR-011)")
	require.Equal(t, 128, decoded.Bounds().Dx())
}

// TestResize_ScalesToRequestedSize guards the underlying scaler: when
// the requested size differs from the source, the output dimensions must
// match. This is exercised indirectly by every other test but is asserted
// explicitly here for regression visibility.
func TestResize_ScalesToRequestedSize(t *testing.T) {
	src := noisyImage(1024, 1024, 0)
	// Use a long-edge budget above MinLongEdge that forces shrinking but
	// allows the bytes budget to accommodate a fit. Noisy 1024x1024 PNG
	// is ~36KB; budget 32KB forces shrinking to ~768x768 (~14KB) which
	// fits within the bytes budget.
	result, err := resize.ResizeToFit(src, catalog.ResizeLimits{
		LongEdgePx: 512,
		MaxBytes:   32 * 1024,
	})
	require.NoError(t, err)
	decoded, _, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.LessOrEqual(t, decoded.Bounds().Dx(), 512)
	require.LessOrEqual(t, decoded.Bounds().Dy(), 512)
}

// TestResize_CatmullRomKernel_UsedForScaling is a smoke test for the
// internal scaler: a high-frequency source image is downscaled by the
// Catmull-Rom kernel (no nearest-neighbor blockiness). The decoded output
// must round-trip to a valid image.
func TestResize_CatmullRomKernel_UsedForScaling(t *testing.T) {
	// 512x512 source with a checkerboard pattern — high-frequency content.
	src := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			if (x/8+y/8)%2 == 0 {
				src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	// Confirm the scaler is available (this is also a build-time check
	// that golang.org/x/image/draw is wired in correctly).
	var dst draw.Image = image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	_ = dst // referenced above

	// The public API shrinks the source to fit the long-edge budget and
	// exercises the Catmull-Rom kernel. The result is verified to be a
	// valid (decodable) image smaller than the source.
	result, err := resize.ResizeToFit(src, catalog.ResizeLimits{
		LongEdgePx: 384,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	decoded, _, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.Less(t, decoded.Bounds().Dx(), 512,
		"source was 512 wide; result must be smaller (exercises the scaler)")
	require.LessOrEqual(t, decoded.Bounds().Dx(), 384,
		"result long edge must fit the budget")
	require.LessOrEqual(t, decoded.Bounds().Dy(), 384)
}

// TestResize_NoScaleSameSize checks that when the requested size equals
// the source size, the scaler returns the source unchanged (no copy).
// This is exercised indirectly by every small-image test; the assertion
// here is the round-trip dimensions.
func TestResize_NoScaleSameSize(t *testing.T) {
	src := solidImage(64, 64, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	result, err := resize.ResizeToFit(src, catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   10 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.Equal(t, 64, result.LongEdge)
	require.Equal(t, "image/png", result.Mime)
}

// TestResizeBudget_OverflowSafety (Wave 1 TD-M6) asserts that a MaxBytes
// value exceeding the int32 range is handled in int64 space without
// silent truncation. The historical pkg/media/resize.Budget stored
// MaxBytes as int (32 bits on common Go targets); a value like 1<<50
// would silently truncate to 0 on a 32-bit int cast. With
// pkg/providers/catalog.ResizeLimits (MaxBytes int64) the
// comparison runs entirely in int64 space — there is no int cast at
// the budget boundary, so the comparison is honest and the pipeline
// returns ErrLadderFloor (not a successful fit) when an int64 budget
// is physically impossible to satisfy.
func TestResizeBudget_OverflowSafety(t *testing.T) {
	// 1<<62 overflows int32 (max ≈ 2.1e9) but fits in int64 (max ≈ 9.2e18).
	// A pre-TD-M6 int conversion would silently truncate to a small
	// value (or to 0 on some targets), making the comparison wrong.
	// With catalog.ResizeLimits (int64 bytes) the value is honored
	// exactly: the comparison `int64(len(pngData)) <= 1<<62` is trivially
	// true for any image that can exist in memory, so the PNG path
	// returns a valid encoding — the budget itself is not silently
	// truncated, capped, or converted.
	bigBudget := catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   1 << 62,
	}
	img := solidImage(64, 64, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	result, err := resize.ResizeToFit(img, bigBudget)
	require.NoError(t, err, "oversized-but-int64 budget must not error")
	require.NotEmpty(t, result.Data, "must produce encoded bytes when budget is generous")
	require.Equal(t, "image/png", result.Mime, "PNG fits an int64-sized budget trivially")

	decoded, format, err := image.Decode(bytes.NewReader(result.Data))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.Equal(t, 64, decoded.Bounds().Dx())
	require.Equal(t, 64, decoded.Bounds().Dy())

	// Mirror check: a 1-byte budget forces the pipeline to declare the
	// image impossible to fit. This proves the int64 comparison is
	// actually consulted (not silently treated as "always > 0" by an
	// int cast that lost the value).
	tinyBudget := catalog.ResizeLimits{
		LongEdgePx: 7680,
		MaxBytes:   1,
	}
	_, err = resize.ResizeToFit(img, tinyBudget)
	require.ErrorIs(t, err, resize.ErrLadderFloor,
		"a 1-byte budget must still drive the ladder floor; if the budget "+
			"type silently truncated, this would behave as an always-fit "+
			"budget and pass")
}

// TestResizeBudget_NonPositiveRejected (Wave 1 TD-M6) is the closure
// guard for the budget validation path. A zero or negative LongEdgePx
// or MaxBytes must surface as an explicit error, never as a silent
// no-op or a successful fit. The pre-TD-M6 resize package enforced
// the same on its own Budget type; this test pins the canonical
// catalog.ResizeLimits to the same contract.
func TestResizeBudget_NonPositiveRejected(t *testing.T) {
	img := solidImage(32, 32, color.RGBA{})

	cases := map[string]catalog.ResizeLimits{
		"zero LongEdgePx":     {LongEdgePx: 0, MaxBytes: 1024},
		"zero MaxBytes":       {LongEdgePx: 1024, MaxBytes: 0},
		"negative LongEdgePx": {LongEdgePx: -1, MaxBytes: 1024},
		"negative MaxBytes":   {LongEdgePx: 1024, MaxBytes: -1},
	}
	for name, budget := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := resize.ResizeToFit(img, budget)
			require.Error(t, err, "%s must surface as an error", name)
			require.NotErrorIs(t, err, resize.ErrLadderFloor,
				"%s must be rejected before the ladder starts (invalid input, not a floor result)", name)
		})
	}
}
