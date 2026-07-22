// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

const webpFixtureBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

func TestEncodeImageToDataURL_NormalizesJPEGToPNG(t *testing.T) {
	img := testImage()
	var source bytes.Buffer
	require.NoError(t, jpeg.Encode(&source, img, &jpeg.Options{Quality: 90}))

	dataURL := encodeImageFixture(t, "image.jpg", "image/jpeg", source.Bytes(), 1<<20)
	decoded := decodePNGDataURL(t, dataURL)
	require.Equal(t, 4, decoded.Bounds().Dx())
	require.Equal(t, 3, decoded.Bounds().Dy())
}

func TestEncodeImageToDataURL_NormalizesGIFToPNG(t *testing.T) {
	img := testImage()
	var source bytes.Buffer
	require.NoError(t, gif.Encode(&source, img, nil))

	dataURL := encodeImageFixture(t, "image.gif", "image/gif", source.Bytes(), 1<<20)
	decoded := decodePNGDataURL(t, dataURL)
	require.Equal(t, 4, decoded.Bounds().Dx())
	require.Equal(t, 3, decoded.Bounds().Dy())
}

func TestEncodeImageToDataURL_AnimatedGIFToStaticPNG(t *testing.T) {
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}})
	second := image.NewPaletted(first.Rect, first.Palette)
	for i := range first.Pix {
		first.Pix[i] = 0
		second.Pix[i] = 1
	}
	var source bytes.Buffer
	require.NoError(t, gif.EncodeAll(&source, &gif.GIF{
		Image:     []*image.Paletted{first, second},
		Delay:     []int{10, 10},
		LoopCount: 0,
	}))

	dataURL := encodeImageFixture(t, "animated.gif", "image/gif", source.Bytes(), 1<<20)
	decoded := decodePNGDataURL(t, dataURL)
	got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
	require.Equal(t, uint8(255), got.R)
	require.Equal(t, uint8(0), got.B)
}

func TestEncodeImageToDataURL_NormalizesWebPToPNG(t *testing.T) {
	source, err := base64.StdEncoding.DecodeString(webpFixtureBase64)
	require.NoError(t, err)

	dataURL := encodeImageFixture(t, "image.webp", "image/webp", source, 1<<20)
	decoded := decodePNGDataURL(t, dataURL)
	require.NotEmpty(t, decoded.Bounds())
}

func TestEncodeImageToDataURL_NormalizesBMPToPNG(t *testing.T) {
	img := testImage()
	var source bytes.Buffer
	require.NoError(t, bmp.Encode(&source, img))

	dataURL := encodeImageFixture(t, "image.bmp", "image/bmp", source.Bytes(), 1<<20)
	decoded := decodePNGDataURL(t, dataURL)
	require.Equal(t, 4, decoded.Bounds().Dx())
	require.Equal(t, 3, decoded.Bounds().Dy())
}

func TestEncodeImageToDataURL_NormalizesTIFFToPNG(t *testing.T) {
	img := testImage()
	var source bytes.Buffer
	require.NoError(t, tiff.Encode(&source, img, nil))

	dataURL := encodeImageFixture(t, "image.tiff", "image/tiff", source.Bytes(), 1<<20)
	decoded := decodePNGDataURL(t, dataURL)
	require.Equal(t, 4, decoded.Bounds().Dx())
	require.Equal(t, 3, decoded.Bounds().Dy())
}

func TestEncodeImageToDataURL_NonDecodableReturnsEmpty(t *testing.T) {
	// ADR-051 FR-001 (D2 path): AVIF/HEIC are NOT decoded by Go's stdlib
	// + x/image and (unlike SVG) have no pure-Go rasterizer. They must NOT
	// be dropped before the LLM call — they pass through as-is so the
	// provider's rejection triggers the media-retry strip on the next
	// attempt. The prior behavior returned "" here, which made
	// resolveMediaRefs drop the attachment entirely — exactly the bug the
	// spec says to avoid. Assert the passthrough: the original bytes come
	// back as a data URL carrying the ORIGINAL mime (not re-encoded as PNG).
	// (SVG was removed from this path when the oksvg/rasterx rasterizer
	// landed — see TestEncodeImageToDataURL_SVGRasterizesToPNG.)
	path := filepath.Join(t.TempDir(), "image.avif")
	require.NoError(t, os.WriteFile(path, []byte("fake-avif-bytes"), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/avif", info, 1<<20)
	require.NotEmpty(t, dataURL, "AVIF must pass through to the LLM (D2 path), not be dropped")
	require.True(t, strings.HasPrefix(dataURL, "data:image/avif;base64,"),
		"passthrough must carry the original avif mime, got %q", dataURL)
}

func TestEncodeImageToDataURL_PixelBomb_Rejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixel-bomb.png")
	require.NoError(t, os.WriteFile(path, pixelBombPNG(t, 4097, 4097), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	require.Empty(t, encodeImageToDataURL(path, "image/png", info, 1<<20))
}

func TestEncodeImageToDataURL_OutputOversize_Fallback(t *testing.T) {
	source, err := base64.StdEncoding.DecodeString(webpFixtureBase64)
	require.NoError(t, err)
	decoded, format, err := image.Decode(bytes.NewReader(source))
	require.NoError(t, err)
	require.Equal(t, "webp", format)

	var expectedPNG bytes.Buffer
	require.NoError(t, png.Encode(&expectedPNG, decoded))
	require.Less(t, len(source), expectedPNG.Len(), "fixture must exercise the post-normalization size limit")

	path := filepath.Join(t.TempDir(), "expanded.webp")
	require.NoError(t, os.WriteFile(path, source, 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/webp", info, len(source))
	require.Empty(t, dataURL)
}

func encodeImageFixture(t *testing.T, name, mime string, data []byte, maxSize int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, mime, info, maxSize)
	require.NotEmpty(t, dataURL)
	require.True(t, strings.HasPrefix(dataURL, "data:image/png;base64,"))
	return dataURL
}

func decodePNGDataURL(t *testing.T, dataURL string) image.Image {
	t.Helper()
	const prefix = "data:image/png;base64,"
	require.True(t, strings.HasPrefix(dataURL, prefix))
	encoded := strings.TrimPrefix(dataURL, prefix)
	data, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	decoded, format, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, "png", format)
	return decoded
}

func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(40 * x),
				G: uint8(60 * y),
				B: uint8(20 * (x + y)),
				A: 255,
			})
		}
	}
	return img
}

func pixelBombPNG(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	data := source.Bytes()
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}
