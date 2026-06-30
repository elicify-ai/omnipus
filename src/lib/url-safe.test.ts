/**
 * url-safe.test.ts — scheme allow-list helpers.
 *
 * isSafeHref: strict, rejects relative + non-http(s)/mailto/tel.
 * isDisplayableImageSrc: image-oriented, RESOLVES relative URLs against the origin
 *   (chat uploads are same-origin relative paths) and permits http(s) + data:,
 *   rejecting javascript:/blob:/file:. This is the gate ChatImage uses before
 *   rendering + fetching an <img> into the clipboard/download.
 */

import { describe, it, expect } from 'vitest'
import { isSafeHref, isDisplayableImageSrc } from './url-safe'

describe('isSafeHref', () => {
  it('allows http/https/mailto/tel absolute URLs', () => {
    expect(isSafeHref('https://omnipus.ai/x.png')).toBe(true)
    expect(isSafeHref('http://example.com')).toBe(true)
    expect(isSafeHref('mailto:a@b.com')).toBe(true)
  })

  it('rejects javascript:, data:, blob:, file: and relative URLs', () => {
    expect(isSafeHref('javascript:alert(1)')).toBe(false)
    expect(isSafeHref('data:image/png;base64,AAAA')).toBe(false)
    expect(isSafeHref('blob:https://x/y')).toBe(false)
    expect(isSafeHref('file:///etc/passwd')).toBe(false)
    expect(isSafeHref('/api/v1/uploads/x')).toBe(false) // relative throws in new URL
  })
})

describe('isDisplayableImageSrc', () => {
  it('allows same-origin RELATIVE upload paths (the common chat case)', () => {
    expect(isDisplayableImageSrc('/api/v1/uploads/s/upload')).toBe(true)
    expect(isDisplayableImageSrc('./img.png')).toBe(true)
    expect(isDisplayableImageSrc('uploads/x.jpg')).toBe(true)
  })

  it('allows absolute http(s) and self-contained data: images', () => {
    expect(isDisplayableImageSrc('https://cdn.example.com/a.png')).toBe(true)
    expect(isDisplayableImageSrc('http://example.com/a.png')).toBe(true)
    expect(isDisplayableImageSrc('data:image/png;base64,AAAA')).toBe(true)
  })

  it('rejects javascript:, blob:, file: and empty', () => {
    expect(isDisplayableImageSrc('javascript:alert(1)')).toBe(false)
    expect(isDisplayableImageSrc('blob:https://x/y')).toBe(false)
    expect(isDisplayableImageSrc('file:///etc/passwd')).toBe(false)
    expect(isDisplayableImageSrc('')).toBe(false)
  })
})
