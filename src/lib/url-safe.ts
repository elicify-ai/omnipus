/**
 * URL scheme safety helpers — shared across chat and iframe preview components.
 *
 * Allow-list: http, https, mailto, tel.
 * Everything else (javascript:, data:, vbscript:, file:, ftp:, schemeless
 * absolute URLs that browser engines resolve to javascript:, etc.) is rejected.
 */

const SAFE_PROTOCOLS = new Set(['http:', 'https:', 'mailto:', 'tel:'])

/**
 * Returns true only when `href` is parseable by the URL constructor and its
 * scheme is in the allow-list. Relative URLs (which throw in `new URL`) and
 * any disallowed scheme both return false.
 */
export function isSafeHref(href: string): boolean {
  try {
    const parsed = new URL(href)
    return SAFE_PROTOCOLS.has(parsed.protocol)
  } catch {
    return false
  }
}

/**
 * Safety check for an <img> src that will ALSO be fetched into the clipboard / a
 * download. Unlike isSafeHref it RESOLVES relative URLs against the current origin —
 * chat uploads are same-origin relative paths (e.g. `/api/v1/uploads/…`) that
 * isSafeHref would reject — and permits only http(s) and self-contained data: images,
 * rejecting javascript:/blob:/file:. Pair with fetchImageBlob's content-type gate.
 */
export function isDisplayableImageSrc(src: string): boolean {
  if (!src) return false
  try {
    const parsed = new URL(src, typeof location !== 'undefined' ? location.href : 'http://localhost')
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' || parsed.protocol === 'data:'
  } catch {
    return false
  }
}
