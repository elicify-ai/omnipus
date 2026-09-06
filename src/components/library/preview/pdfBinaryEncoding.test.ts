// pdfBinaryEncoding.test.ts — the base64 encoding LibraryPdfPreview's Save
// path sends as LibraryBinaryContentRequest.content_base64. Expected values
// are hand-computed / derived via `atob` (the platform's own decoder, not
// this module's encoder) — an independent oracle, not an echo of the
// implementation.

import { describe, it, expect } from 'vitest'
import { uint8ArrayToBase64 } from './pdfBinaryEncoding'

describe('uint8ArrayToBase64', () => {
  it('encodes a known byte sequence to its known standard base64 form', () => {
    // "Man" -> "TWFu" is the canonical RFC 4648 worked example.
    const bytes = new Uint8Array([0x4d, 0x61, 0x6e])
    expect(uint8ArrayToBase64(bytes)).toBe('TWFu')
  })

  it('round-trips through the platform\'s own atob decoder', () => {
    const bytes = new Uint8Array(300)
    for (let i = 0; i < bytes.length; i++) bytes[i] = i % 256
    const encoded = uint8ArrayToBase64(bytes)
    const decoded = Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0))
    expect(Array.from(decoded)).toEqual(Array.from(bytes))
  })

  it('handles the empty array', () => {
    expect(uint8ArrayToBase64(new Uint8Array())).toBe('')
  })

  it('round-trips a buffer larger than the internal chunk size (32,768 bytes)', () => {
    // The whole reason this module chunks: String.fromCharCode(...bytes)
    // blows V8's ~65,536 argument limit well before this size. A PDF this
    // preview saves can easily exceed it.
    const bytes = new Uint8Array(100_000)
    for (let i = 0; i < bytes.length; i++) bytes[i] = (i * 7) % 256
    const encoded = uint8ArrayToBase64(bytes)
    const decoded = Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0))
    expect(decoded.length).toBe(bytes.length)
    expect(Array.from(decoded)).toEqual(Array.from(bytes))
  })

  it('never contains URL-safe alphabet characters or line breaks (standard base64, RFC 4648 §4)', () => {
    const bytes = new Uint8Array(64)
    for (let i = 0; i < bytes.length; i++) bytes[i] = 251 + (i % 5) // biases toward '+'/'/' bytes
    const encoded = uint8ArrayToBase64(bytes)
    expect(encoded).not.toContain('-')
    expect(encoded).not.toContain('_')
    expect(encoded).not.toContain('\n')
  })
})
