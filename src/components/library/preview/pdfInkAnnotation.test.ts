// pdfInkAnnotation.test.ts — the exact shape PDF.js's worker bundle
// (pdf.worker.mjs's `getNewAnnotationsMap` / `InkAnnotation.createNewDict` /
// `createNewAppearanceStream`) must see to emit a real `/Subtype /Ink`
// annotation on `saveDocument()`. Every expected value here is derived from
// reading that worker bundle directly (see pdfInkAnnotation.ts's header for
// the exact functions and reasoning), not from this module's own
// implementation — an independent oracle, not an echo of the code under test.

import { describe, it, expect, beforeEach } from 'vitest'
import {
  buildInkAnnotationEntry,
  PDFJS_NEW_ANNOTATION_PREFIX,
  __resetInkKeyCounterForTests,
} from './pdfInkAnnotation'

const INK = 15 // arbitrary stand-in for pdfjs.AnnotationEditorType.INK — this
// module accepts it as a parameter precisely so it never has to match a real
// pdfjs-dist constant to be tested.

beforeEach(() => {
  __resetInkKeyCounterForTests()
})

describe('buildInkAnnotationEntry — key', () => {
  it('starts every key with PDF.js\'s own "new annotation" prefix', () => {
    const { key } = buildInkAnnotationEntry({
      strokesPdfSpace: [[{ x: 0, y: 0 }, { x: 1, y: 1 }]],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
    })
    // MUTATION THIS DIES ON: change the prefix, or key a new ink annotation
    // by anything other than PDFJS_NEW_ANNOTATION_PREFIX. pdf.worker.mjs's
    // getNewAnnotationsMap skips any storage key that does not start with
    // exactly this string — a differently-keyed entry is silently dropped
    // from saveDocument(), never an error.
    expect(key.startsWith(PDFJS_NEW_ANNOTATION_PREFIX)).toBe(true)
  })

  it('produces a distinct key per call, so two signatures never collide in annotationStorage', () => {
    const strokes = [[{ x: 0, y: 0 }, { x: 1, y: 1 }]]
    const first = buildInkAnnotationEntry({ strokesPdfSpace: strokes, pageIndex: 0, rotation: 0, annotationEditorTypeInk: INK })
    const second = buildInkAnnotationEntry({ strokesPdfSpace: strokes, pageIndex: 0, rotation: 0, annotationEditorTypeInk: INK })
    expect(first.key).not.toBe(second.key)
  })
})

describe('buildInkAnnotationEntry — required fields for saveNewAnnotations', () => {
  it('passes annotationType, pageIndex, rotation and deleted:false straight through', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [[{ x: 10, y: 20 }, { x: 30, y: 40 }]],
      pageIndex: 2,
      rotation: 90,
      annotationEditorTypeInk: INK,
    })
    // MUTATION THIS DIES ON: dropping or hardcoding any of these four —
    // pdf.worker.mjs's getNewAnnotationsMap groups by `value.pageIndex`, its
    // save loop skips any entry with `deleted: true`, and its switch
    // statement dispatches strictly on `annotationType`.
    expect(value.annotationType).toBe(INK)
    expect(value.pageIndex).toBe(2)
    expect(value.rotation).toBe(90)
    expect(value.deleted).toBe(false)
  })

  it('defaults color to black, opacity to 1, and a positive thickness', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [[{ x: 0, y: 0 }, { x: 1, y: 0 }]],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
    })
    expect(value.color).toEqual([0, 0, 0])
    expect(value.opacity).toBe(1)
    expect(value.thickness).toBeGreaterThan(0)
  })
})

describe('buildInkAnnotationEntry — paths.points (/InkList)', () => {
  it('emits one flat [x0,y0,x1,y1,...] array per stroke, values unchanged', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [
        [{ x: 1, y: 2 }, { x: 3, y: 4 }, { x: 5, y: 6 }],
        [{ x: 100, y: 200 }],
      ],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
    })
    // MUTATION THIS DIES ON: transposing x/y, dropping a point, or merging
    // strokes into one array — InkAnnotation.createNewDict writes
    // `paths.points` verbatim as /InkList, one array per stroke.
    expect(value.paths.points).toEqual([
      [1, 2, 3, 4, 5, 6],
      [100, 200],
    ])
  })

  it('drops empty strokes entirely rather than emitting a degenerate entry', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [[], [{ x: 0, y: 0 }, { x: 1, y: 1 }], []],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
    })
    expect(value.paths.points).toHaveLength(1)
  })
})

describe('buildInkAnnotationEntry — paths.lines (appearance stream outline)', () => {
  it('encodes a straight multi-point stroke as one moveto plus NaN-marked linetos', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [[{ x: 1, y: 2 }, { x: 3, y: 4 }, { x: 5, y: 6 }]],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
    })
    const [outline] = value.paths.lines
    // MUTATION THIS DIES ON: changing the moveto position (must be [4],[5])
    // or emitting a non-NaN group (createNewAppearanceStream's straight-line
    // branch fires exactly when `outline[i]` is NaN; the other branch draws
    // a cubic Bezier through 3 point pairs, which this module never emits).
    expect(outline.slice(0, 6)).toEqual([NaN, NaN, NaN, NaN, 1, 2])
    expect(outline.slice(6, 12)).toEqual([NaN, NaN, NaN, NaN, 3, 4])
    expect(outline.slice(12, 18)).toEqual([NaN, NaN, NaN, NaN, 5, 6])
    expect(outline).toHaveLength(18)
  })

  it('encodes a single-point stroke (a tap/dot) as a bare 6-number moveto group', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [[{ x: 7, y: 8 }]],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
    })
    // createNewAppearanceStream special-cases `outline.length === 6`: it
    // re-emits the same point as a zero-length "l" so a dot still paints
    // (round line-cap) instead of a lone unterminated "m". This module's
    // job is only to produce that 6-length array — the re-emit is the
    // worker's, not this module's.
    expect(value.paths.lines[0]).toEqual([NaN, NaN, NaN, NaN, 7, 8])
  })
})

describe('buildInkAnnotationEntry — rect (/Rect, and the appearance stream BBox)', () => {
  it('bounds the rect to the min/max of every point across every stroke, padded by half the thickness plus one unit', () => {
    const { value } = buildInkAnnotationEntry({
      strokesPdfSpace: [
        [{ x: 10, y: 10 }, { x: 20, y: 5 }],
        [{ x: 0, y: 30 }, { x: 15, y: 15 }],
      ],
      pageIndex: 0,
      rotation: 0,
      annotationEditorTypeInk: INK,
      thicknessPdfUnits: 2,
    })
    // min x=0, min y=5, max x=20, max y=30; pad = 2/2 + 1 = 2.
    expect(value.rect).toEqual([0 - 2, 5 - 2, 20 + 2, 30 + 2])
  })
})

describe('buildInkAnnotationEntry — invalid input', () => {
  it('throws rather than silently writing an empty annotation when every stroke is empty', () => {
    expect(() =>
      buildInkAnnotationEntry({ strokesPdfSpace: [[], []], pageIndex: 0, rotation: 0, annotationEditorTypeInk: INK }),
    ).toThrow()
    expect(() =>
      buildInkAnnotationEntry({ strokesPdfSpace: [], pageIndex: 0, rotation: 0, annotationEditorTypeInk: INK }),
    ).toThrow()
  })
})
