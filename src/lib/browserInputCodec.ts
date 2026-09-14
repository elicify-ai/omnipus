import type { BrowserInputFrame } from './api/generated/asyncapi-types'

export const browserInputProtocol = 'omnipus.input.v1'
const kinds = ['mouse_move', 'mouse_down', 'mouse_up', 'wheel', 'key_down', 'key_up', 'text'] as const
const fields = ['x', 'y', 'capture_width', 'capture_height', 'button', 'delta_x', 'delta_y', 'key', 'code', 'key_code', 'text', 'modifiers', 'url', 'capture_generation', 'capture_id', 'input_epoch', 'control_epoch', 'reliable_seq', 'hover_seq', 'gesture_barrier'] as const
const strings = new Set<string>(['button', 'key', 'code', 'text', 'url', 'capture_id'])
const encoder = new TextEncoder()
const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })
const invalid = () => new Error('Invalid browser input binary packet')

/** v1 preserves optional fields; the server's existing schema remains authoritative. */
export function encodeBrowserInput(frame: BrowserInputFrame): Uint8Array<ArrayBuffer> {
  const kind = kinds.indexOf(frame.kind as typeof kinds[number]) + 1
  if (frame.type !== 'browser_input' || !kind || Object.keys(frame).some(key => key !== 'type' && key !== 'kind' && !fields.includes(key as typeof fields[number]))) throw invalid()
  const values: (Uint8Array | number | undefined)[] = []
  let mask = 0, size = 9
  fields.forEach((key, bit) => {
    const value = frame[key]
    if (value === undefined) return
    mask |= 1 << bit
    if (strings.has(key)) {
      if (typeof value !== 'string') throw invalid()
      const bytes = encoder.encode(value)
      // TextEncoder replaces lone UTF-16 surrogates; never silently change input.
      if (bytes.length > 65535 || decoder.decode(bytes) !== value) throw invalid()
      values[bit] = bytes; size += 2 + bytes.length
    } else {
      if (typeof value !== 'number' || !Number.isFinite(value)) throw invalid()
      values[bit] = value; size += 8
    }
  })
  if (size > 65536) throw invalid()
  const bytes = new Uint8Array(size), view = new DataView(bytes.buffer)
  bytes.set([79, 66, 73, 1, kind]); view.setUint32(5, mask, true)
  let offset = 9
  values.forEach(value => {
    if (value !== undefined && typeof value !== 'number') {
      view.setUint16(offset, value.length, true); offset += 2
      bytes.set(value, offset); offset += value.length
    } else if (value !== undefined) { view.setFloat64(offset, value, true); offset += 8 }
  })
  return bytes
}

export function decodeBrowserInput(data: ArrayBuffer | ArrayBufferView): BrowserInputFrame {
  const bytes = ArrayBuffer.isView(data) ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength) : new Uint8Array(data)
  if (bytes.length < 9 || bytes.length > 65536 || bytes[0] !== 79 || bytes[1] !== 66 || bytes[2] !== 73 || bytes[3] !== 1 || !kinds[bytes[4] - 1]) throw invalid()
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength), mask = view.getUint32(5, true)
  if (mask >>> fields.length) throw invalid()
  const frame: Record<string, unknown> = { type: 'browser_input', kind: kinds[bytes[4] - 1] }
  let offset = 9
  fields.forEach((key, bit) => {
    if (!(mask & (1 << bit))) return
    if (strings.has(key)) {
      if (offset + 2 > bytes.length) throw invalid()
      const length = view.getUint16(offset, true); offset += 2
      if (offset + length > bytes.length) throw invalid()
      frame[key] = decoder.decode(bytes.subarray(offset, offset + length)); offset += length
    } else {
      if (offset + 8 > bytes.length) throw invalid()
      const value = view.getFloat64(offset, true); offset += 8
      if (!Number.isFinite(value)) throw invalid()
      frame[key] = value
    }
  })
  if (offset !== bytes.length) throw invalid()
  return frame as unknown as BrowserInputFrame
}
