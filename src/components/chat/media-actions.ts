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

/** Produce a safe download name: take the basename (drop any path components) and
 * strip control + bidi-override characters so the suggested name can't spoof an
 * extension (e.g. via U+202E RTL-override) or look like a path. Legitimate spaces
 * are kept. The browser already ignores path components, so this is anti-spoofing,
 * not anti-traversal. Falls back to "download" if nothing usable remains. */
function sanitizeFilename(name: string): string {
  const base = name.split(/[\\/]/).pop() ?? ''
  // eslint-disable-next-line no-control-regex
  const cleaned = base.replace(/[\u0000-\u001F\u202A-\u202E]/g, '').trim().slice(0, 200)
  return cleaned || 'download'
}

/**
 * Trigger a browser download for a Blob. The anchor is appended to the document
 * (Firefox requires it for a synthetic click) and the object URL is revoked on a
 * later tick (revoking synchronously after .click() can abort the download).
 */
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = sanitizeFilename(filename)
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

// ── Fetch ─────────────────────────────────────────────────────────────────────

/**
 * Fetch a chat image as a Blob for copy/share/download.
 *
 * SECURITY: the `src` is agent-influenceable (a media frame / markdown image URL),
 * and this result lands in the clipboard / a download / a share sheet. Two gates
 * keep it from becoming a credentialed-read-into-exfil-sink primitive:
 *   1. Only http(s) and data: URLs are fetched (no file:/blob:/other schemes).
 *   2. The response must be ok AND have an image/* content-type — so an agent that
 *      points `src` at an internal/authenticated endpoint (which returns JSON/HTML)
 *      gets rejected instead of having that body copied/downloaded.
 */
export async function fetchImageBlob(src: string): Promise<Blob> {
  let resolved: URL
  try {
    resolved = new URL(src, typeof location !== 'undefined' ? location.href : 'http://localhost')
  } catch {
    throw new Error('Invalid image URL')
  }
  if (!['http:', 'https:', 'data:'].includes(resolved.protocol)) {
    throw new Error(`Unsupported image URL scheme: ${resolved.protocol}`)
  }
  const response = await fetch(resolved.href)
  if (!response.ok) {
    throw new Error(`Image fetch failed (${response.status})`)
  }
  const blob = await response.blob()
  if (!blob.type.startsWith('image/')) {
    throw new Error('That URL did not return an image')
  }
  return blob
}

/**
 * Fetch a raster image and guarantee a PNG Blob. Chromium's clipboard image
 * write only accepts image/png, so non-PNG sources (jpeg/webp/…) are re-encoded
 * through a canvas. Inherits fetchImageBlob's scheme + content-type gates.
 */
export async function fetchImagePng(src: string): Promise<Blob> {
  const blob = await fetchImageBlob(src)
  if (blob.type === 'image/png') return blob
  const bitmap = await createImageBitmap(blob)
  const canvas = document.createElement('canvas')
  canvas.width = bitmap.width
  canvas.height = bitmap.height
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('Could not get 2D canvas context')
  ctx.drawImage(bitmap, 0, 0)
  return new Promise<Blob>((resolve, reject) =>
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('PNG conversion failed'))), 'image/png'),
  )
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
