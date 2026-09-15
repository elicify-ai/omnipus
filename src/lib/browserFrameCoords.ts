import { computeObjectContainRect, type DeviceCoords, type RectLike } from './browserLiveCoords'

/**
 * Remove container letterboxing, then the encoder's aspect-preserving padding.
 * Outside coordinates are retained only for an already-held pointer's release;
 * ordinary input must never turn a click in padding into a click at a page edge.
 */
export function mapClientToBrowserCss(
  clientX: number, clientY: number, box: RectLike,
  videoWidth: number, videoHeight: number, cssWidth: number, cssHeight: number, allowOutside = false,
): DeviceCoords | null {
  if (![clientX, clientY, box.left, box.top].every(Number.isFinite)) return null
  if (![box.width, box.height, videoWidth, videoHeight, cssWidth, cssHeight].every(value => Number.isFinite(value) && value > 0)) return null
  const videoRect = computeObjectContainRect(box, videoWidth, videoHeight)
  const pageRect = computeObjectContainRect(videoRect, cssWidth, cssHeight)
  if (!allowOutside && (clientX < pageRect.left || clientX >= pageRect.left + pageRect.width || clientY < pageRect.top || clientY >= pageRect.top + pageRect.height)) return null
  const x = cssWidth / 2 + (clientX - (pageRect.left + pageRect.width / 2)) * cssWidth / pageRect.width
  const y = cssHeight / 2 + (clientY - (pageRect.top + pageRect.height / 2)) * cssHeight / pageRect.height
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null
  return allowOutside ? { x, y } : { x: Math.max(0, x), y: Math.max(0, y) }
}
