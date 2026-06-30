// media-actions.ts — pure helpers + feature detection for chat media actions.
// No React in this file. Each function is small and typed.

// ── Feature detection ─────────────────────────────────────────────────────────

/** True iff the browser supports writing image blobs to the clipboard. */
export function canCopyImage(): boolean {
  return typeof ClipboardItem !== 'undefined' && !!navigator.clipboard?.write
}

/** True iff the browser supports the Web Share API with file support. */
export function canShareFiles(): boolean {
  return typeof navigator.share === 'function'
}

// ── Text ──────────────────────────────────────────────────────────────────────

/** Copy a plain text string to the clipboard. */
export async function copyText(text: string): Promise<void> {
  await navigator.clipboard.writeText(text)
}

// ── Image blob ────────────────────────────────────────────────────────────────

/**
 * Copy a Blob to the clipboard as an image.
 * NOTE: Chromium only accepts image/png — callers must pass a PNG blob.
 */
export async function copyImageBlob(blob: Blob): Promise<void> {
  await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })])
}

/**
 * Share a Blob via the Web Share API.
 * Guards with canShareFiles() — throws if sharing is unavailable.
 */
export async function shareBlob(blob: Blob, filename: string, title?: string): Promise<void> {
  if (!canShareFiles()) {
    throw new Error('Web Share API is not available in this browser')
  }
  const file = new File([blob], filename, { type: blob.type })
  await navigator.share({ files: [file], title })
}

// ── Download ──────────────────────────────────────────────────────────────────

/**
 * Trigger a browser download for a Blob.
 * Creates a temporary object URL, simulates a click, then revokes the URL.
 */
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

// ── Fetch ─────────────────────────────────────────────────────────────────────

/**
 * Fetch a same-origin image (http/https or data: URL) as a Blob.
 * Chat images are same-origin so no CORS headers are needed.
 */
export async function fetchImageBlob(src: string): Promise<Blob> {
  const response = await fetch(src)
  return response.blob()
}

// ── SVG → PNG conversion ─────────────────────────────────────────────────────

/**
 * Rasterise an SVG string or SVGElement to a PNG Blob at the given pixel density scale.
 * Reads intrinsic size from viewBox or bounding box; falls back to 800×600 if unknown.
 * Intended for Mermaid copy/download as PNG.
 */
export async function svgToPngBlob(svg: string | SVGElement, scale = 2): Promise<Blob> {
  // Normalise to a serialized SVG string
  let svgString: string
  if (typeof svg === 'string') {
    svgString = svg
  } else {
    svgString = new XMLSerializer().serializeToString(svg)
  }

  // Parse to extract intrinsic dimensions from viewBox or width/height attributes
  const parser = new DOMParser()
  const doc = parser.parseFromString(svgString, 'image/svg+xml')
  const svgEl = doc.querySelector('svg')

  let width = 800
  let height = 600

  if (svgEl) {
    const viewBox = svgEl.getAttribute('viewBox')
    if (viewBox) {
      const parts = viewBox.trim().split(/[\s,]+/)
      if (parts.length >= 4) {
        const w = parseFloat(parts[2])
        const h = parseFloat(parts[3])
        if (w > 0 && h > 0) {
          width = w
          height = h
        }
      }
    } else {
      const w = parseFloat(svgEl.getAttribute('width') ?? '0')
      const h = parseFloat(svgEl.getAttribute('height') ?? '0')
      if (w > 0 && h > 0) {
        width = w
        height = h
      }
    }
  }

  // Build a data URL for the SVG
  const encoded = btoa(unescape(encodeURIComponent(svgString)))
  const dataUrl = `data:image/svg+xml;base64,${encoded}`

  return new Promise<Blob>((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      const canvas = document.createElement('canvas')
      canvas.width = Math.round(width * scale)
      canvas.height = Math.round(height * scale)
      const ctx = canvas.getContext('2d')
      if (!ctx) {
        reject(new Error('Could not get 2D canvas context'))
        return
      }
      ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
      canvas.toBlob((blob) => {
        if (!blob) {
          reject(new Error('canvas.toBlob returned null'))
          return
        }
        resolve(blob)
      }, 'image/png')
    }
    img.onerror = () => reject(new Error('Failed to load SVG as image'))
    img.src = dataUrl
  })
}
