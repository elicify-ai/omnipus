import { describe, expect, it } from 'vitest'
import { annotationCssPoint, sameAnnotationFrame, type BrowserAnnotationFrame } from './browserAnnotationFrame'

const frame: BrowserAnnotationFrame = {
  captureId: 'capture-original', generation: 7, marker: 100,
  cssWidth: 1280, cssHeight: 720, videoWidth: 640, videoHeight: 360, source: {},
}

describe('annotation frame coordinates and lifetime', () => {
  it('maps encoded crop center into confirmed CSS pixels without changing the crop', () => {
    const crop = { x: 300, y: 160, width: 40, height: 40 }
    expect(annotationCssPoint(crop, frame)).toEqual({ x: 640, y: 360 })
    expect(crop).toEqual({ x: 300, y: 160, width: 40, height: 40 })
  })

  it('removes encoder padding and refuses an enrichment point inside padding', () => {
    const padded = { ...frame, cssWidth: 800, cssHeight: 400, videoWidth: 400, videoHeight: 240 }
    // Page occupies y=20..220 in the encoded image; encoded (100,70) is CSS (200,100).
    expect(annotationCssPoint({ x: 90, y: 60, width: 20, height: 20 }, padded)).toEqual({ x: 200, y: 100 })
    expect(annotationCssPoint({ x: 90, y: 0, width: 20, height: 20 }, padded)).toBeNull()
    expect(annotationCssPoint({ x: 0, y: 0, width: 20, height: 20 }, null)).toBeNull()
  })

  it('accepts a copied proof from the same presented source', () => {
    expect(sameAnnotationFrame(frame, { ...frame })).toBe(true)
    expect(sameAnnotationFrame(frame, null)).toBe(false)
  })

  it.each([
    { captureId: 'replacement' }, { generation: 8 }, { marker: 200 },
    { cssWidth: 1279 }, { cssHeight: 719 }, { videoWidth: 639 }, { videoHeight: 359 }, { source: {} },
  ])('rejects retired identity or geometry %j', change => {
    expect(sameAnnotationFrame(frame, { ...frame, ...change })).toBe(false)
  })
})
