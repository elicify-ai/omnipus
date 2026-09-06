// pdfInkAnnotation.ts — builds a PDF.js AnnotationStorage entry for a
// hand-drawn signature, saved as a real /Ink annotation (library-b-c-design-
// 2026-09-07.md "B — PDF form-fill and drawn signature"; ADR-067 D15.3:
// "Drawn signature produced /Subtype /Ink with /InkList ... plus an
// appearance stream", measured against pdfjs-dist 6.2.108's saveDocument()
// and re-verified by an engine unrelated to PDF.js).
//
// Why this is hand-built rather than going through PDF.js's InkEditor /
// AnnotationEditorUIManager: that machinery (`display/editor/tools.js`'s
// `AnnotationEditorUIManager`, 15-argument constructor) is designed to run
// inside the full `pdfjs-dist/web/pdf_viewer` component — an EventBus, an
// l10n provider, comment/signature/alt-text managers, an undo bar — which
// this preview deliberately does not pull in (see the header of
// LibraryPdfPreview.tsx: it already builds its OWN Worker and its OWN
// TextLayer rather than adopting the full viewer, to keep the bundle and the
// trust surface small). Pulling in the editor UI manager just to get one
// annotation type would mean adopting all of that for a feature that needs
// none of its interactivity (no undo stack, no alt text, no multi-select).
//
// The save path itself is small and directly inspectable: PDF.js's own
// worker bundle (`pdf.worker.mjs`) reads `AnnotationStorage` as a plain
// key/value map. `saveDocument()` collects every entry whose key starts with
// PDF.js's internal "this is a brand-new annotation, not an edit to an
// existing one" prefix (`getNewAnnotationsMap`), and for `annotationType ===
// AnnotationEditorType.INK` hands the stored value STRAIGHT to
// `InkAnnotation.createNewDict` / `createNewAppearanceStream` with no
// normalisation in between. Those two functions are what this module's
// output shape is reverse-engineered against — verified by reading
// `pdf.worker.mjs` directly, not assumed from the public API surface, which
// does not document this shape at all (annotationStorage.setValue's own
// JSDoc types the value as a bare `Object`).

/** A point in some 2D pixel space — either a signature pad's own drawing
 * surface (device-independent CSS pixels) or, after conversion via
 * `PageViewport.convertToPdfPoint`, PDF user-space units. Callers keep these
 * two spaces straight by type name only; this module never mixes them. */
export interface SignaturePoint {
  x: number
  y: number
}

/** One continuous pen-down-to-pen-up stroke, as a polyline of points. */
export type SignatureStroke = SignaturePoint[]

/**
 * PDF.js's own prefix for annotationStorage keys that represent a brand-new
 * annotation absent from the original document (as opposed to a value keyed
 * by an EXISTING annotation's `id`, e.g. a filled-in AcroForm field, which
 * PDF.js recognises by simply not matching this prefix). Verified directly
 * against pdfjs-dist 6.2.108's shipped worker bundle
 * (`build/pdf.worker.mjs`'s `getNewAnnotationsMap`, and `build/pdf.mjs`'s own
 * copy of the same constant) — an ink entry keyed any other way is silently
 * dropped from `saveDocument()`, never an error, which is exactly the kind
 * of silent failure this project does not ship.
 */
export const PDFJS_NEW_ANNOTATION_PREFIX = 'pdfjs_internal_editor_'

// Offset well clear of PDF.js's own internal per-document editor-id counter
// (which also starts at 0 and increments by 1 per new editor). That counter
// only exists inside a real AnnotationEditorUIManager, which this preview
// never constructs alongside this hand-built path (see the file header), so
// there is no actual collision risk — the offset is a zero-cost extra margin,
// not a load-bearing fix for a real collision.
const INK_KEY_OFFSET = 100_000

let inkKeyCounter = 0

/** Resets the per-module key counter. Tests only, so key assertions are
 * deterministic across test files that both import this module. */
export function __resetInkKeyCounterForTests(): void {
  inkKeyCounter = 0
}

function nextInkKey(): string {
  inkKeyCounter += 1
  return `${PDFJS_NEW_ANNOTATION_PREFIX}${INK_KEY_OFFSET + inkKeyCounter}`
}

/** The exact shape `InkAnnotation.createNewDict`/`createNewAppearanceStream`
 * (pdf.worker.mjs) read off a `saveNewAnnotations` entry for
 * `AnnotationEditorType.INK`. Fields not read by either function (e.g. a
 * `date`/`user` author string) are intentionally omitted — PDF.js defaults
 * `date` to "now" itself when absent. */
export interface InkAnnotationStorageValue {
  annotationType: number
  pageIndex: number
  rotation: number
  rect: [number, number, number, number]
  color: [number, number, number]
  opacity: number
  thickness: number
  paths: { points: number[][]; lines: number[][] }
  deleted: false
}

/** Half a point of stroke-width padding plus one full PDF unit, so the
 * annotation's /Rect never clips the stroke it bounds. */
const RECT_PADDING_PDF_UNITS = 1

/**
 * Converts already-PDF-space strokes into the annotationStorage entry
 * `saveDocument()` needs to emit a real `/Subtype /Ink` annotation.
 *
 *   - `paths.points` — one flat `[x0,y0,x1,y1,...]` array per stroke. This
 *     becomes `/InkList` verbatim: the semantic entry another PDF engine
 *     reads (the half of ADR-067's measurement that isn't the appearance
 *     stream).
 *   - `paths.lines` — one "outline" array per stroke, in the NaN-marker
 *     straight-line format `createNewAppearanceStream` understands: the
 *     first six-number group's positions [4] and [5] are the initial
 *     `moveto`; every following six-number group draws a straight `lineto`
 *     to its own [4]/[5] because its position [0] is `NaN` (the alternative,
 *     non-NaN branch draws a cubic Bezier through all six numbers and is
 *     deliberately unused here — a straight segment between consecutive
 *     pointer-move samples is a faithful rendering of a freehand stroke at
 *     the density a mouse/touch/pen actually reports, and this module does
 *     not reimplement PDF.js's own bezier-fit outliner to get there).
 *
 * Throws if every stroke is empty — the caller (LibrarySignaturePad's
 * "Place signature" action) is expected to disable itself before that can
 * happen; this is a programmer-error guard, not a user-facing validation.
 */
export function buildInkAnnotationEntry(params: {
  strokesPdfSpace: SignatureStroke[]
  pageIndex: number
  rotation: number
  /** `pdfjs.AnnotationEditorType.INK` — passed in rather than imported here
   * so this module stays pdfjs-free and independently unit-testable. */
  annotationEditorTypeInk: number
  color?: [number, number, number]
  opacity?: number
  thicknessPdfUnits?: number
}): { key: string; value: InkAnnotationStorageValue } {
  const {
    strokesPdfSpace,
    pageIndex,
    rotation,
    annotationEditorTypeInk,
    color = [0, 0, 0],
    opacity = 1,
    thicknessPdfUnits = 1.5,
  } = params

  const nonEmptyStrokes = strokesPdfSpace.filter((stroke) => stroke.length > 0)
  if (nonEmptyStrokes.length === 0) {
    throw new Error('buildInkAnnotationEntry requires at least one non-empty stroke')
  }

  const points: number[][] = []
  const lines: number[][] = []
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity

  for (const stroke of nonEmptyStrokes) {
    const flat: number[] = []
    const first = stroke[0]
    const outline: number[] = [NaN, NaN, NaN, NaN, first.x, first.y]
    for (const { x, y } of stroke) {
      flat.push(x, y)
      if (x < minX) minX = x
      if (y < minY) minY = y
      if (x > maxX) maxX = x
      if (y > maxY) maxY = y
    }
    for (let i = 1; i < stroke.length; i++) {
      const p = stroke[i]
      outline.push(NaN, NaN, NaN, NaN, p.x, p.y)
    }
    points.push(flat)
    lines.push(outline)
  }

  const pad = thicknessPdfUnits / 2 + RECT_PADDING_PDF_UNITS
  const rect: [number, number, number, number] = [minX - pad, minY - pad, maxX + pad, maxY + pad]

  return {
    key: nextInkKey(),
    value: {
      annotationType: annotationEditorTypeInk,
      pageIndex,
      rotation,
      rect,
      color,
      opacity,
      thickness: thicknessPdfUnits,
      paths: { points, lines },
      deleted: false,
    },
  }
}
