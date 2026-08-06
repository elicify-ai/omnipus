// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// ADR-051 RD1 extension (Option A): SVG is normalized to PNG like every
// other image, via the pure-Go oksvg/rasterx rasterizer (no CGo, Hard
// Constraint #2). The provider then receives a canonical PNG it can
// actually see, instead of an image/svg+xml block every major vision
// provider rejects with HTTP 400.

const (
	// maxSVGRasterDimension caps one edge of the raster canvas. Combined
	// with maxImagePixels it bounds decode memory exactly like the
	// DecodeConfig pre-flight for raster formats.
	maxSVGRasterDimension = 4096
	// defaultSVGRasterSize is the canvas edge used when the SVG declares
	// neither a viewBox nor width/height (icon.ViewBox is zero).
	defaultSVGRasterSize = 512
)

// rasterizeSVGToPNG renders SVG markup to PNG bytes. Returns an error when
// the markup is unparseable or contains no drawable content — the caller
// falls back to injecting the SVG source as text (Option B), which lets any
// model reason about the image from its code.
//
// oksvg.WarnErrorMode tolerates unsupported elements (filters, etc.) and
// renders what it can; a partial render beats none. The pixel budget is
// enforced by scaling the canvas down BEFORE rasterization, so a crafted
// giant viewBox cannot allocate a giant bitmap.
func rasterizeSVGToPNG(data []byte) ([]byte, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.WarnErrorMode)
	if icon == nil {
		return nil, fmt.Errorf("svg parse failed: %w", err)
	}
	if len(icon.SVGPaths) == 0 {
		return nil, fmt.Errorf("svg contains no drawable content")
	}

	w, h := icon.ViewBox.W, icon.ViewBox.H
	if w <= 0 || h <= 0 {
		w, h = defaultSVGRasterSize, defaultSVGRasterSize
	}

	// Scale the canvas down to fit both the per-edge cap and the shared
	// pixel budget, preserving aspect ratio. scale <= 1: we never upscale,
	// the SVG's own density is the best fidelity available.
	scale := 1.0
	if w > maxSVGRasterDimension {
		scale = math.Min(scale, maxSVGRasterDimension/w)
	}
	if h > maxSVGRasterDimension {
		scale = math.Min(scale, maxSVGRasterDimension/h)
	}
	if pixels := w * h; pixels > maxImagePixels {
		scale = math.Min(scale, math.Sqrt(maxImagePixels/pixels))
	}
	rw := max(1, int(w*scale))
	rh := max(1, int(h*scale))

	img := image.NewRGBA(image.Rect(0, 0, rw, rh))
	scanner := rasterx.NewScannerGV(rw, rh, img, img.Bounds())
	raster := rasterx.NewDasher(rw, rh, scanner)
	icon.SetTarget(0, 0, float64(rw), float64(rh))
	icon.Draw(raster, 1.0)

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("png encode failed: %w", err)
	}
	return out.Bytes(), nil
}

// encodeSVGToDataURL rasterizes an SVG file to PNG and returns it as a data
// URL, mirroring encodeImageToDataURL's contract: empty string on any
// failure (logged via logImageNormalizationFailure), which routes the caller
// to the Option-B text-injection fallback.
func encodeSVGToDataURL(localPath string, info os.FileInfo, maxSize int) string {
	const mime = "image/svg+xml"
	if info.Size() > int64(maxSize) {
		logImageNormalizationFailure(localPath, mime, "input-oversize", nil, map[string]any{
			"size":     info.Size(),
			"max_size": maxSize,
		})
		return ""
	}

	// Bounded by the size check above; ReadFile is safe here.
	data, err := os.ReadFile(localPath)
	if err != nil {
		logImageNormalizationFailure(localPath, mime, "read-failed", err, nil)
		return ""
	}

	pngBytes, err := rasterizeSVGToPNG(data)
	if err != nil {
		logImageNormalizationFailure(localPath, mime, "svg-rasterize-failed", err, nil)
		return ""
	}
	if len(pngBytes) > maxSize {
		logImageNormalizationFailure(localPath, mime, "encoded-oversize", nil, map[string]any{
			"encoded_size": len(pngBytes),
			"max_size":     maxSize,
		})
		return ""
	}

	const prefix = "data:image/png;base64,"
	return prefix + base64.StdEncoding.EncodeToString(pngBytes)
}
