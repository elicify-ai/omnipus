// pdfBinaryEncoding.ts — Uint8Array -> standard (RFC 4648 §4) base64, chunked.
//
// `btoa(String.fromCharCode(...bytes))` is the obvious one-liner and the wrong
// one for a saved PDF: spreading a multi-megabyte Uint8Array as call
// arguments to `String.fromCharCode` blows the engine's argument-count limit
// (V8's is 65,536) well before a typical filled PDF's byte length. This
// chunks the conversion so no single call exceeds that limit, matching the
// widely-used browser-side workaround for the same constraint.

const CHUNK_SIZE = 0x8000 // 32,768 — comfortably under the 65,536 argument limit.

/** Converts raw bytes to a standard base64 string, as `LibraryBinaryContentRequest.content_base64`
 * (contracts/components/schemas/LibraryBinaryContentRequest.yaml) requires: "standard (RFC 4648
 * §4) base64 ... no URL-safe alphabet, no line wrapping." */
export function uint8ArrayToBase64(bytes: Uint8Array): string {
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += CHUNK_SIZE) {
    const chunk = bytes.subarray(offset, offset + CHUNK_SIZE)
    binary += String.fromCharCode(...chunk)
  }
  return btoa(binary)
}
