import type { FrameCropRect } from './browserLiveCoords'
import { mapClientToBrowserCss } from './browserFrameCoords'

/** Local proof for optional annotation enrichment; never sent on the wire. */
export interface BrowserAnnotationFrame { // not-wire-format: local annotation proof retains stream object identity and crop geometry; never serialized
  captureId: string
  generation: number
  marker: number
  cssWidth: number
  cssHeight: number
  videoWidth: number
  videoHeight: number
  source: object
}

export function annotationCssPoint(crop: FrameCropRect, frame: BrowserAnnotationFrame | null): { x: number; y: number } | null {
  if (!frame) return null
  return mapClientToBrowserCss(
    crop.x + crop.width / 2, crop.y + crop.height / 2,
    { left: 0, top: 0, width: frame.videoWidth, height: frame.videoHeight },
    frame.videoWidth, frame.videoHeight, frame.cssWidth, frame.cssHeight,
  )
}

export function sameAnnotationFrame(original: BrowserAnnotationFrame | null, current: BrowserAnnotationFrame | null): boolean {
  return original !== null && current !== null &&
    original.captureId === current.captureId && original.generation === current.generation &&
    original.marker === current.marker && original.source === current.source &&
    original.cssWidth === current.cssWidth && original.cssHeight === current.cssHeight &&
    original.videoWidth === current.videoWidth && original.videoHeight === current.videoHeight
}
