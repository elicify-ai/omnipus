// Package resize implements pure-Go image resize-to-fit for the Omnipus
// workspace media library (ADR-051 Rev 4, FR-011/012/013/014/015).
//
// The pipeline shrinks a decoded raster image until it fits a per-provider
// ResizeLimits budget. The order of attempts at each candidate size is:
//
//  1. PNG at the candidate size (FR-011: canonical PNG output when it fits).
//  2. JPEG at the candidate size, quality ladder 90 → 40 (FR-015).
//
// If neither fits the byte budget, the pipeline shrinks 0.75× and repeats.
// The ladder floor (FR-015) is reached when the next 0.75× shrink would
// take the long edge below MinLongEdge — ResizeToFit returns ErrLadderFloor
// and the presentation orchestrator (Step 5) offloads the file into the
// workspace work/ dir.
//
// # Budget shape (Wave 1 TD-M6)
//
// ResizeToFit accepts the canonical pkg/providers/catalog.ResizeLimits
// directly. There is no resize.Budget type. Byte counts are int64
// end-to-end so a 32-bit target cannot truncate the comparison; the
// catalog allows MaxBytes up to math.MaxInt64 and the resize pipeline
// compares in int64 space (no int cast at the budget boundary).
package resize

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ErrLadderFloor is returned by ResizeToFit when the image still does not
// fit the budget after exhausting the ladder (JPEG quality 40 reached AND
// the next 0.75× shrink would take the long edge below MinLongEdge).
// The caller treats this as the routing signal for Step 5 offload.
var ErrLadderFloor = errors.New("resize: ladder floor reached, image too large for budget")

// MinLongEdge is the resize-ladder floor (FR-015). Below this long edge the
// image is too small to be useful; the ladder terminates and the image is
// offloaded per the spec.
const MinLongEdge = 256

// patchEdgePx is the tile size of vision-patch accounting (issue #1435): a
// provider counts ceil(width/32) * ceil(height/32) patches in an image, so a
// ResizeLimits.MaxPatches cap is evaluated on this grid.
const patchEdgePx = 32

// PatchCount reports how many 32x32-pixel patches a width x height image
// occupies under the OpenAI vision-patch formula ceil(w/32) * ceil(h/32)
// (issue #1435; the incident image's 2560x15424 -> 80*482 = 38560 matched
// the provider's rejection verbatim).
func PatchCount(width, height int) int {
	if width <= 0 || height <= 0 {
		return 0
	}
	return ((width + patchEdgePx - 1) / patchEdgePx) * ((height + patchEdgePx - 1) / patchEdgePx)
}

// shrinkFactor is the FR-015 ladder step: 0.75× per shrink.
const shrinkFactor = 0.75

// qualityLevels is the FR-015 JPEG ladder: 90 → 80 → 70 → 60 → 50 → 40.
// At each shrink step the ladder cycles through these in order; the first
// encoding that fits the budget wins.
var qualityLevels = []int{90, 80, 70, 60, 50, 40}

// Result is the output of a successful ResizeToFit.
type Result struct {
	Data     []byte // encoded image bytes (PNG or JPEG, see Mime)
	Mime     string // "image/png" or "image/jpeg"
	LongEdge int    // final long edge of the encoded image
}

// ResizeToFit shrinks img to fit budget and returns the encoded bytes,
// preferring PNG when it fits (FR-011 canonical output) and falling through
// to the JPEG quality ladder (FR-015) when PNG does not fit. Returns
// ErrLadderFloor if the image cannot be made to fit at any size on the
// ladder (the caller routes to Step 5 offload).
//
// The DecodeConfig pre-flight pixel guard is the caller's responsibility
// (see encodeImageToDataURL, FR-013): ResizeToFit does NOT enforce
// maxImagePixels because it operates on a fully decoded image.Image.
//
// The budget shape is pkg/providers/catalog.ResizeLimits — the
// canonical per-model resize budget. LongEdgePx is the long-edge
// ceiling; MaxBytes (int64) is the byte ceiling after the PNG→JPEG
// quality ladder; MaxPatches (issue #1435) is the ceiling on 32px
// vision patches, ceil(w/32)*ceil(h/32). LongEdgePx and MaxBytes must
// be positive; non-positive values surface as an error (no silent
// truncation, no implicit cap). MaxPatches is optional: zero (or
// negative) means the provider's patch accounting is unknown and only
// the edge/byte ceilings apply — exactly the pre-#1435 behavior.
//
// Pure-Go: uses stdlib image/jpeg, image/png, and golang.org/x/image/draw.
// No CGo.
func ResizeToFit(img image.Image, budget catalog.ResizeLimits) (Result, error) {
	if budget.LongEdgePx <= 0 || budget.MaxBytes <= 0 {
		return Result{}, errors.New("resize: invalid budget (LongEdgePx and MaxBytes must be > 0)")
	}
	if img == nil {
		return Result{}, errors.New("resize: nil image")
	}

	bounds := img.Bounds()
	curW, curH := bounds.Dx(), bounds.Dy()
	if curW <= 0 || curH <= 0 {
		return Result{}, errors.New("resize: image has zero dimensions")
	}

	for {
		longEdge := maxInt(curW, curH)

		// If the long edge exceeds budget.LongEdgePx, shrink unconditionally
		// (FR-014). The ladder cannot return an image whose long edge
		// exceeds the budget — the budget is a hard ceiling on dimensions.
		if longEdge > budget.LongEdgePx {
			var floor bool
			curW, curH, floor = shrinkOrFloor(curW, curH)
			if floor {
				return Result{}, ErrLadderFloor
			}
			continue
		}

		// Issue #1435: the same hard-ceiling treatment for the patch budget.
		// A candidate whose 32px patch grid exceeds budget.MaxPatches is
		// never encoded — the provider would reject it — so the ladder
		// shrinks first, exactly as it does for the long edge. Zero
		// MaxPatches keeps the unlimited (pre-#1435) behavior.
		if budget.MaxPatches > 0 && PatchCount(curW, curH) > budget.MaxPatches {
			var floor bool
			curW, curH, floor = shrinkOrFloor(curW, curH)
			if floor {
				return Result{}, ErrLadderFloor
			}
			continue
		}

		// 1. Try PNG at the current size (FR-011 canonical output).
		if pngData, err := encodePNG(img, curW, curH); err == nil {
			if int64(len(pngData)) <= budget.MaxBytes {
				return Result{Data: pngData, Mime: "image/png", LongEdge: longEdge}, nil
			}
		}

		// 2. Try JPEG ladder at the current size (FR-015).
		for _, q := range qualityLevels {
			jpegData, err := encodeJPEG(img, curW, curH, q)
			if err != nil {
				return Result{}, err
			}
			if int64(len(jpegData)) <= budget.MaxBytes {
				return Result{Data: jpegData, Mime: "image/jpeg", LongEdge: longEdge}, nil
			}
		}

		// 3. Neither PNG nor any JPEG quality fit the bytes budget at
		// this size. Shrink 0.75× (FR-015). The ladder floor (FR-015) is
		// reached when the new long edge would fall below MinLongEdge —
		// the image is too small to be useful after further shrinking,
		// and is offloaded (Step 5) per the spec.
		var floor bool
		curW, curH, floor = shrinkOrFloor(curW, curH)
		if floor {
			return Result{}, ErrLadderFloor
		}
	}
}

// encodePNG scales src to width×height using the Catmull-Rom kernel and
// encodes the result as PNG. The scale step is skipped when the requested
// size matches the source exactly, to avoid the cost of a full re-render.
func encodePNG(src image.Image, w, h int) ([]byte, error) {
	dst := scaleImage(src, w, h)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeJPEG scales src to width×height using the Catmull-Rom kernel and
// encodes the result as JPEG at the given quality (1-100, stdlib semantics).
func encodeJPEG(src image.Image, w, h int, quality int) ([]byte, error) {
	dst := scaleImage(src, w, h)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scaleImage returns an image.Image of size width×height containing src
// resampled with the Catmull-Rom kernel (high quality, pure-Go). When the
// requested size matches src.Bounds() the source is returned unchanged to
// avoid an unnecessary re-render.
func scaleImage(src image.Image, width, height int) image.Image {
	srcBounds := src.Bounds()
	if srcBounds.Dx() == width && srcBounds.Dy() == height {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, srcBounds, draw.Over, nil)
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// shrinkOrFloor applies the FR-015 0.75× shrink. The bool return is true
// when the ladder floor has been reached (new long edge < MinLongEdge) —
// the caller MUST return ErrLadderFloor.
func shrinkOrFloor(curW, curH int) (int, int, bool) {
	newW := int(math.Floor(float64(curW) * shrinkFactor))
	newH := int(math.Floor(float64(curH) * shrinkFactor))
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	newLong := maxInt(newW, newH)
	if newLong < MinLongEdge {
		return curW, curH, true
	}
	return newW, newH, false
}
