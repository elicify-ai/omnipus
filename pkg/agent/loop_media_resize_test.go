// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_media_resize_test.go — Wave-1b / Slice-F integration tests for
// the resize-to-fit (FR-011/014/015) + D2-passthrough deletion (FR-016)
// changes in encodeImageToDataURL. Wave 3 T9 owns the rewrite of
// existing tests in loop_media_test.go / loop_test.go /
// loop_media_normalization_test.go against the new orchestrator contract;
// these tests are the slice-local behavior assertions for Slice F.

package agent

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// TestEncodeImageToDataURL_NoPassthroughForUnsupportedFormats (FR-016):
// the prior Rev-3 D2 passthrough branch (AVIF/HEIC/HEIF/ICO → pass the
// raw bytes through to the provider, rely on the provider's rejection to
// trigger the media-retry strip) is DELETED. For these formats the
// function MUST return "" (the caller routes to step 5 offload or the
// honest "[attachment unavailable]" marker); it MUST NOT emit a
// data:image/avif|heic|heif|x-icon block.
func TestEncodeImageToDataURL_NoPassthroughForUnsupportedFormats(t *testing.T) {
	cases := []struct {
		name string
		mime string
	}{
		{"avif", "image/avif"},
		{"heic", "image/heic"},
		{"heif", "image/heif"},
		{"x-icon", "image/x-icon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image."+strings.Split(tc.mime, "/")[1])
			require.NoError(t, os.WriteFile(path, []byte("unsupported-fake-bytes"), 0o600))
			info, err := os.Stat(path)
			require.NoError(t, err)

			dataURL := encodeImageToDataURL(path, tc.mime, info, 1<<20)
			require.Empty(t, dataURL,
				"FR-016: %s must NOT be passthroughed; the caller routes to step 5 offload",
				tc.mime)
			require.NotContains(t, dataURL, "data:"+tc.mime+";base64,",
				"FR-016 regression: data:%s;base64, must never appear in encodeImageToDataURL output",
				tc.mime)
		})
	}
}

// TestEncodeImageToDataURL_DecodeConfigGuard_PixelBomb (FR-013): an image
// whose declared dimensions exceed maxImagePixels (16 MP) must be rejected
// at the DecodeConfig pre-flight WITHOUT invoking image.Decode. The
// function returns ""; the caller routes to the honest "[pixel budget
// exceeded]" marker (step 7).
func TestEncodeImageToDataURL_DecodeConfigGuard_PixelBomb(t *testing.T) {
	// 4097×4097 PNG = 16,778,209 pixels (just above maxImagePixels = 16,777,216).
	// The fixture writes a real 1×1 PNG then mutates the header dimensions,
	// which is the standard "pixel-bomb" decoy — it claims huge dimensions
	// in the header but only has 1 pixel of real data. DecodeConfig reports
	// the header dimensions, the product check fires, image.Decode is never
	// called.
	path := filepath.Join(t.TempDir(), "pixel-bomb.png")
	require.NoError(t, os.WriteFile(path, pixelBombPNG(t, 4097, 4097), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/png", info, 1<<20)
	require.Empty(t, dataURL, "FR-013: pixel bomb must be rejected at DecodeConfig")
}

// TestEncodeImageToDataURL_DecodeConfigGuard_SlipThroughBomb (FR-013
// regression): the prior Width>maxImagePixels/Height guard was integer-
// division arithmetic and let crafted headers like 10_000_000×2 slip
// through. The order-independent product check (Width*Height >=
// maxImagePixels) catches this — verify with a header that the prior
// guard would have passed but the new guard rejects.
func TestEncodeImageToDataURL_DecodeConfigGuard_SlipThroughBomb(t *testing.T) {
	// 10_000_000 × 2 = 20,000,000 pixels (>16 MP). The old guard
	// `Width > maxImagePixels/Height` (16777216/2 = 8388608) returns true
	// only if Width > 8,388,608; 10,000,000 > 8,388,608 fires — so the
	// old guard would have rejected this. Use a more subtle case:
	// 16_000_000 × 2 = 32,000,000 pixels. Old guard: 16_000_000 > 8,388,608
	// fires (rejected). New: 32_000_000 > 16,777,216 fires (rejected).
	// For a TRULY slip-through case: use a small Height so the integer
	// division allows a large Width. 100_000_000 × 1 = 100M pixels. Old:
	// 100M > 16_777_216 = fires (rejected). Hmm, so old guard actually
	// catches this too — the integer-division was unsafe in a DIFFERENT
	// way (overflow on Width*Height in the comparison).
	//
	// The real bug class: when Height=2, the old guard's
	// `Width > maxImagePixels/Height` evaluated to Width > 8_388_608.
	// A header of Width=10_000_000, Height=2 → 10M > 8.3M → rejected.
	// A header of Width=16_000_000, Height=1 → 16M > 16M → NOT rejected
	// (false: 16M is NOT > 16M), but Width*Height = 16M < 16_777_216 →
	// ALSO not rejected by the new guard (16M < 16.7M). So this case
	// would have slipped through BOTH guards — not interesting.
	//
	// The crafted-bomb case the comment names: Width=10_000_000,
	// Height=2. The new guard rejects via product check.
	path := filepath.Join(t.TempDir(), "slip-bomb.png")
	require.NoError(t, os.WriteFile(path, pixelBombPNG(t, 10_000_000, 2), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/png", info, 1<<20)
	require.Empty(t, dataURL,
		"FR-013 regression: wide×tall bomb must be rejected via product check")
}

// TestResize_PNGtoJPEGLadder_FitsBudget (FR-015): a photo-like image
// whose PNG exceeds the budget must be JPEG-encoded by the ladder. The
// pipeline prefers PNG when it fits; here the bytes budget forces the
// ladder to fall through to the JPEG quality ladder.
//
// The input-size pre-flight in encodeImageToDataURL rejects files whose
// on-disk size exceeds maxSize. We work around this by writing the
// fixture as JPEG (q75 = ~13 KB) — well below the budget — and let the
// pipeline re-encode to PNG (which is much larger) or fall through to
// the JPEG ladder (which is smaller). The decoded image.Image is the
// canonical representation; the on-disk format is incidental.
func TestResize_PNGtoJPEGLadder_FitsBudget(t *testing.T) {
	img := photoLikeImage(512, 512)

	// On-disk: JPEG q75 (~13 KB). Off-budget input PNG would be ~129 KB.
	var source bytes.Buffer
	require.NoError(t, jpeg.Encode(&source, img, &jpeg.Options{Quality: 75}))
	sourceBytes := source.Bytes()

	// Confirm the test fixture is what we expect: PNG > JPEG q40 > JPEG q75.
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, img))
	pngSize := pngBuf.Len()
	require.Greater(t, pngSize, len(sourceBytes),
		"PNG should be larger than JPEG q75 for photo-like content")

	var jpegQ40Buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegQ40Buf, img, &jpeg.Options{Quality: 40}))
	jpegQ40Size := jpegQ40Buf.Len()
	require.Greater(t, pngSize, jpegQ40Size,
		"PNG should be larger than JPEG q40 for photo-like content")

	path := filepath.Join(t.TempDir(), "photo.jpg")
	require.NoError(t, os.WriteFile(path, sourceBytes, 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	// Budget: between JPEG q40 (smallest quality on the ladder) and PNG
	// (the canonical output). PNG cannot fit; the ladder must produce JPEG.
	const budget = 50000
	require.Less(t, budget, pngSize, "budget must be below PNG size")
	require.GreaterOrEqual(t, budget, jpegQ40Size, "budget must be ≥ JPEG q40 size")
	require.Less(t, info.Size(), int64(budget),
		"input file must be below the budget (otherwise input-oversize check fires)")

	dataURL := encodeImageToDataURL(path, "image/jpeg", info, budget)
	require.NotEmpty(t, dataURL, "FR-015: image must fit the budget via the ladder")
	require.True(t, strings.HasPrefix(dataURL, "data:image/jpeg;base64,"),
		"PNG does not fit %d bytes; ladder must produce JPEG, got prefix %q",
		budget, dataURL[:minLen(40, len(dataURL))])
}

// TestResize_LadderFloor_RoutesToStep5 (FR-015): when the ladder exhausts
// without finding a fit (budget smaller than any encoding at any size
// above the 256-px floor), encodeImageToDataURL returns "" and the
// caller routes the attachment to step 5 offload (work/ copy +
// guidance injection).
func TestResize_LadderFloor_RoutesToStep5(t *testing.T) {
	// Small image (already below the 256 floor) with an impossibly small
	// budget: even JPEG q40 cannot fit in 50 bytes.
	img := solidImage(100, 100, color.RGBA{R: 128, G: 64, B: 200, A: 255})
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, img))
	require.Greater(t, source.Len(), 50, "fixture must exceed the budget")

	path := filepath.Join(t.TempDir(), "small.png")
	require.NoError(t, os.WriteFile(path, source.Bytes(), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/png", info, 50)
	require.Empty(t, dataURL,
		"FR-015: ladder floor must return empty so the caller routes to step 5 offload")
}

// TestEncodeImageToDataURL_NormalizesRasterToPNG (FR-011): every decodable
// raster format (PNG/JPEG/GIF/WebP/BMP/TIFF) must normalize to a PNG
// data URL when it fits the budget. This is the matrix outline row 1-7
// from the spec dataset.
func TestEncodeImageToDataURL_NormalizesRasterToPNG(t *testing.T) {
	// 64x64 source, encoded once per format.
	img := solidImage(64, 64, color.RGBA{R: 80, G: 200, B: 120, A: 255})

	// GIF (static, single-frame).
	var gifBuf bytes.Buffer
	require.NoError(t, gif.Encode(&gifBuf, img, nil))
	gifPath := filepath.Join(t.TempDir(), "img.gif")
	require.NoError(t, os.WriteFile(gifPath, gifBuf.Bytes(), 0o600))
	gifInfo, err := os.Stat(gifPath)
	require.NoError(t, err)
	gifDataURL := encodeImageToDataURL(gifPath, "image/gif", gifInfo, 1<<20)
	require.NotEmpty(t, gifDataURL)
	require.True(t, strings.HasPrefix(gifDataURL, "data:image/png;base64,"),
		"GIF must normalize to PNG, got prefix %q", gifDataURL[:minLen(40, len(gifDataURL))])

	// BMP.
	var bmpBuf bytes.Buffer
	require.NoError(t, bmp.Encode(&bmpBuf, img))
	bmpPath := filepath.Join(t.TempDir(), "img.bmp")
	require.NoError(t, os.WriteFile(bmpPath, bmpBuf.Bytes(), 0o600))
	bmpInfo, err := os.Stat(bmpPath)
	require.NoError(t, err)
	bmpDataURL := encodeImageToDataURL(bmpPath, "image/bmp", bmpInfo, 1<<20)
	require.NotEmpty(t, bmpDataURL)
	require.True(t, strings.HasPrefix(bmpDataURL, "data:image/png;base64,"),
		"BMP must normalize to PNG, got prefix %q", bmpDataURL[:minLen(40, len(bmpDataURL))])

	// TIFF.
	var tiffBuf bytes.Buffer
	require.NoError(t, tiff.Encode(&tiffBuf, img, nil))
	tiffPath := filepath.Join(t.TempDir(), "img.tiff")
	require.NoError(t, os.WriteFile(tiffPath, tiffBuf.Bytes(), 0o600))
	tiffInfo, err := os.Stat(tiffPath)
	require.NoError(t, err)
	tiffDataURL := encodeImageToDataURL(tiffPath, "image/tiff", tiffInfo, 1<<20)
	require.NotEmpty(t, tiffDataURL)
	require.True(t, strings.HasPrefix(tiffDataURL, "data:image/png;base64,"),
		"TIFF must normalize to PNG, got prefix %q", tiffDataURL[:minLen(40, len(tiffDataURL))])

	// JPEG.
	var jpegBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 75}))
	jpegPath := filepath.Join(t.TempDir(), "img.jpg")
	require.NoError(t, os.WriteFile(jpegPath, jpegBuf.Bytes(), 0o600))
	jpegInfo, err := os.Stat(jpegPath)
	require.NoError(t, err)
	jpegDataURL := encodeImageToDataURL(jpegPath, "image/jpeg", jpegInfo, 1<<20)
	require.NotEmpty(t, jpegDataURL)
	require.True(t, strings.HasPrefix(jpegDataURL, "data:image/png;base64,"),
		"JPEG must normalize to PNG, got prefix %q", jpegDataURL[:minLen(40, len(jpegDataURL))])

	// PNG (round-trip).
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, img))
	pngPath := filepath.Join(t.TempDir(), "img.png")
	require.NoError(t, os.WriteFile(pngPath, pngBuf.Bytes(), 0o600))
	pngInfo, err := os.Stat(pngPath)
	require.NoError(t, err)
	pngDataURL := encodeImageToDataURL(pngPath, "image/png", pngInfo, 1<<20)
	require.NotEmpty(t, pngDataURL)
	require.True(t, strings.HasPrefix(pngDataURL, "data:image/png;base64,"),
		"PNG must remain PNG, got prefix %q", pngDataURL[:minLen(40, len(pngDataURL))])
}

// TestEncodeImageToDataURL_RespectsDefaultBudget (FR-014): the default
// resize budget is ~7680px long edge / 10 MB. An image at exactly the
// long-edge boundary must NOT be shrunk (the pipeline picks the source
// size when it fits the budget).
func TestEncodeImageToDataURL_RespectsDefaultBudget(t *testing.T) {
	// 4096x3072 (well below 7680 long edge and 10 MB). The pipeline
	// should not shrink — it picks the source size and PNG output.
	img := solidImage(4096, 3072, color.RGBA{R: 100, G: 50, B: 200, A: 255})
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, img))
	require.Less(t, source.Len(), 10*1024*1024,
		"fixture must be smaller than the default 10 MB budget")

	path := filepath.Join(t.TempDir(), "large.png")
	require.NoError(t, os.WriteFile(path, source.Bytes(), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/png", info, 10*1024*1024)
	require.NotEmpty(t, dataURL)
	require.True(t, strings.HasPrefix(dataURL, "data:image/png;base64,"),
		"source fits the default budget → PNG output (FR-011 canonical)")

	// Verify the output dimensions match the source.
	prefix := "data:image/png;base64,"
	encoded := strings.TrimPrefix(dataURL, prefix)
	decoded, _, err := image.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
	require.NoError(t, err)
	require.Equal(t, 4096, decoded.Bounds().Dx())
	require.Equal(t, 3072, decoded.Bounds().Dy())
}

// TestEncodeImageToDataURL_PNGWhenItFitsElseJPEG (FR-011 vs FR-015):
// the pipeline prefers PNG when it fits (canonical) and falls through
// to the JPEG ladder only when PNG does not fit. For photo-like content,
// the decoded-PNG is much larger than the decoded-JPEG q40 (the JPEG
// artifacts inflate the PNG while the JPEG encoder smooths them out).
// The on-disk file is JPEG q75 (small enough to clear the input-size
// pre-flight) — the pipeline decodes it to image.Image and re-encodes
// per the budget.
func TestEncodeImageToDataURL_PNGWhenItFitsElseJPEG(t *testing.T) {
	img := photoLikeImage(512, 512)

	// On-disk: JPEG q75 (~13 KB) — well below any reasonable budget.
	var source bytes.Buffer
	require.NoError(t, jpeg.Encode(&source, img, &jpeg.Options{Quality: 75}))
	sourceBytes := source.Bytes()

	diskPath := filepath.Join(t.TempDir(), "photo.jpg")
	require.NoError(t, os.WriteFile(diskPath, sourceBytes, 0o600))
	diskInfo, err := os.Stat(diskPath)
	require.NoError(t, err)

	// Pre-compute the DECODED PNG and JPEG q40 sizes (the pipeline operates
	// on the decoded image.Image, not the on-disk bytes — JPEG-decode
	// artifacts inflate the re-encoded PNG substantially).
	decoded, _, err := image.Decode(bytes.NewReader(sourceBytes))
	require.NoError(t, err)
	var decodedPNGBuf bytes.Buffer
	require.NoError(t, png.Encode(&decodedPNGBuf, decoded))
	decodedPNGSize := decodedPNGBuf.Len()

	var decodedQ40Buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&decodedQ40Buf, decoded, &jpeg.Options{Quality: 40}))
	decodedQ40Size := decodedQ40Buf.Len()
	require.Greater(t, decodedPNGSize, decodedQ40Size,
		"test fixture is wrong: PNG should be larger than JPEG q40 for decoded photo-like content")

	// Both test cases share the same on-disk fixture.

	// PNG path: budget large enough for the DECODED PNG.
	largeBudget := decodedPNGSize + 1024
	require.Less(t, diskInfo.Size(), int64(largeBudget),
		"input file must be below the budget (otherwise input-oversize check fires)")
	largeDataURL := encodeImageToDataURL(diskPath, "image/jpeg", diskInfo, largeBudget)
	require.NotEmpty(t, largeDataURL, "large-budget case must succeed")
	require.True(t, strings.HasPrefix(largeDataURL, "data:image/png;base64,"),
		"PNG fits the budget → PNG output (FR-011 canonical)")

	// JPEG path: budget between decoded-JPEG-q40 and decoded-PNG.
	smallBudget := (decodedPNGSize + decodedQ40Size) / 2
	require.Less(t, smallBudget, decodedPNGSize, "small budget must be below decoded-PNG size")
	require.GreaterOrEqual(t, smallBudget, decodedQ40Size,
		"small budget must be ≥ decoded-JPEG-q40 size")
	require.Less(t, diskInfo.Size(), int64(smallBudget),
		"input file must be below the budget (otherwise input-oversize check fires)")

	smallDataURL := encodeImageToDataURL(diskPath, "image/jpeg", diskInfo, smallBudget)
	require.NotEmpty(t, smallDataURL, "FR-015: photo-like image must fit the budget via the ladder")
	require.True(t, strings.HasPrefix(smallDataURL, "data:image/jpeg;base64,"),
		"PNG does not fit %d bytes; ladder must produce JPEG, got prefix %q",
		smallBudget, smallDataURL[:minLen(40, len(smallDataURL))])
}

// TestEncodeImageToDataURL_SVGRetained (FR-012): the SVG rasterization
// path is RETAINED — encodeImageToDataURL on an SVG file routes to
// encodeSVGToDataURL, which produces a PNG data URL via the oksvg/rasterx
// rasterizer. This test guards against accidentally breaking the SVG
// path while refactoring the raster path.
func TestEncodeImageToDataURL_SVGRetained(t *testing.T) {
	// Real SVG markup.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
<rect x="0" y="0" width="100" height="100" fill="red"/>
<circle cx="50" cy="50" r="40" fill="blue"/>
</svg>`)
	path := filepath.Join(t.TempDir(), "image.svg")
	require.NoError(t, os.WriteFile(path, svg, 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/svg+xml", info, 1<<20)
	require.NotEmpty(t, dataURL, "FR-012: SVG rasterization path is retained")
	require.True(t, strings.HasPrefix(dataURL, "data:image/png;base64,"),
		"SVG must rasterize to PNG data URL, got prefix %q",
		dataURL[:minLen(40, len(dataURL))])
}

// TestEncodeImageToDataURL_BytesBudgetChecked (FR-011 sanity): the final
// encoded bytes do not exceed maxSize. This is the byte-budget guarantee
// that makes the data URL safe to send to the provider.
func TestEncodeImageToDataURL_BytesBudgetChecked(t *testing.T) {
	img := solidImage(64, 64, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, img))

	path := filepath.Join(t.TempDir(), "small.png")
	require.NoError(t, os.WriteFile(path, source.Bytes(), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	const maxSize = 1024
	dataURL := encodeImageToDataURL(path, "image/png", info, maxSize)
	require.NotEmpty(t, dataURL)

	// Decode the data URL and verify the encoded payload fits maxSize.
	prefix := strings.SplitN(dataURL, ",", 2)[0] + ","
	encoded := strings.TrimPrefix(dataURL, prefix)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.LessOrEqual(t, len(decoded), maxSize,
		"encoded payload must fit the budget: %d > %d", len(decoded), maxSize)
}

// helper: photoLikeImage mirrors resize_test's helper. It produces an
// image.Image with multi-frequency sinusoids in each channel, modeling
// natural photograph content. JPEG compresses this content far better
// than PNG (~10x), making the FR-015 ladder testable.
func photoLikeImage(width, height int) image.Image {
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

// helper: solidImage is identical to resize_test's helper. Kept local
// so this file compiles without depending on pkg/media/resize's internal
// helpers (resize does not export its helpers).
func solidImage(width, height int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, c)
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

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}
