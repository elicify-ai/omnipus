// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const circleSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="50" cy="50" r="40" fill="blue"/></svg>`

func decodeRasterizedPNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err, "rasterized output must be a decodable PNG")
	return img
}

func TestRasterizeSVGToPNG_RendersCircle(t *testing.T) {
	pngBytes, err := rasterizeSVGToPNG([]byte(circleSVG))
	require.NoError(t, err)

	img := decodeRasterizedPNG(t, pngBytes)
	require.Equal(t, 100, img.Bounds().Dx())
	require.Equal(t, 100, img.Bounds().Dy())

	// The canvas center sits inside the blue circle: it must be blue-ish,
	// not the transparent/zero background.
	r, g, b, a := img.At(50, 50).RGBA()
	require.Positive(t, a, "center pixel must be painted")
	require.Positive(t, b, "center pixel must carry blue")
	require.Zero(t, r, "center pixel must not carry red")
	require.Zero(t, g, "center pixel must not carry green")
}

func TestRasterizeSVGToPNG_MalformedReturnsError(t *testing.T) {
	_, err := rasterizeSVGToPNG([]byte("<svg><unclosed"))
	require.Error(t, err, "unparseable SVG must error so the caller can fall back to text injection")
}

func TestRasterizeSVGToPNG_NoDrawableContentReturnsError(t *testing.T) {
	_, err := rasterizeSVGToPNG([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	require.Error(t, err, "an empty SVG renders a blank canvas — the text fallback is strictly better")
}

func TestRasterizeSVGToPNG_MissingViewBoxUsesDefaultCanvas(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40" fill="blue"/></svg>`
	pngBytes, err := rasterizeSVGToPNG([]byte(svg))
	require.NoError(t, err)

	img := decodeRasterizedPNG(t, pngBytes)
	require.Equal(t, defaultSVGRasterSize, img.Bounds().Dx())
	require.Equal(t, defaultSVGRasterSize, img.Bounds().Dy())
}

func TestRasterizeSVGToPNG_HugeViewBoxIsScaledDown(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100000 100000"><rect width="100000" height="100000" fill="red"/></svg>`
	pngBytes, err := rasterizeSVGToPNG([]byte(svg))
	require.NoError(t, err)

	img := decodeRasterizedPNG(t, pngBytes)
	require.LessOrEqual(t, img.Bounds().Dx(), maxSVGRasterDimension)
	require.LessOrEqual(t, img.Bounds().Dy(), maxSVGRasterDimension)
	require.LessOrEqual(t, uint64(img.Bounds().Dx())*uint64(img.Bounds().Dy()), uint64(maxImagePixels))
}

func TestRasterizeSVGToPNG_UnsupportedElementsTolerated(t *testing.T) {
	// WarnErrorMode must skip elements oksvg does not implement (filters)
	// and still render the rest — a partial render beats no render.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
		<filter id="f"><feGaussianBlur stdDeviation="2"/></filter>
		<rect width="10" height="10" fill="green"/>
	</svg>`
	pngBytes, err := rasterizeSVGToPNG([]byte(svg))
	require.NoError(t, err)
	decodeRasterizedPNG(t, pngBytes)
}

func TestEncodeImageToDataURL_SVGRasterizesToPNG(t *testing.T) {
	// ADR-051 RD1 extension (Option A): SVG no longer passes through as
	// image/svg+xml (every major vision provider 400s on that MIME); it is
	// rasterized to canonical PNG like every other image format.
	path := filepath.Join(t.TempDir(), "image.svg")
	require.NoError(t, os.WriteFile(path, []byte(circleSVG), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	dataURL := encodeImageToDataURL(path, "image/svg+xml", info, 1<<20)
	require.True(t, strings.HasPrefix(dataURL, "data:image/png;base64,"),
		"SVG must be rasterized to PNG, got prefix of %q", dataURL[:48])
}

func TestEncodeImageToDataURL_SVGOversizeReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.svg")
	require.NoError(t, os.WriteFile(path, []byte(circleSVG), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	require.Empty(t, encodeImageToDataURL(path, "image/svg+xml", info, 8),
		"input over maxSize must be rejected before rasterization")
}
