import { describe, expect, it } from 'vitest'
import { decodeBrowserInput, encodeBrowserInput } from './browserInputCodec'

const golden = Uint8Array.from([79,66,73,1,7,0,132,11,0,1,0,97,0,0,0,0,0,0,240,63,0,0,0,0,0,0,0,0,0,0,0,0,0,0,240,63,0,0,0,0,0,0,0,0])
const frame = { type: 'browser_input' as const, kind: 'text' as const, text: 'a', input_epoch: 1, control_epoch: 0, reliable_seq: 1, gesture_barrier: 0 }
describe('input binary v1', () => {
  it('matches the independent documented golden vector in both directions', () => {
    expect(encodeBrowserInput(frame)).toEqual(golden)
    expect(decodeBrowserInput(golden)).toEqual(frame)
  })
  it.each(['', 'Zażółć 世界 👋', '🙂'.repeat(8192)])('preserves UTF-8 text and presence', text => {
    expect(decodeBrowserInput(encodeBrowserInput({ ...frame, text }))).toEqual({ ...frame, text })
  })
  it('preserves every optional field including zero and fractional coordinates', () => {
    const all = { ...frame, kind: 'key_down' as const, x: -0.5, y: 0, capture_width: 640.5, capture_height: 480, button: 'left' as const, delta_x: -1.25, delta_y: 0, key: '@', code: 'KeyL', key_code: 76, modifiers: 1, url: '', capture_generation: Number.MAX_SAFE_INTEGER, capture_id: 'capture', hover_seq: 1 }
    expect(decodeBrowserInput(encodeBrowserInput(all))).toEqual(all)
  })
  it('rejects every truncated prefix and trailing data', () => {
    for (let i = 0; i < golden.length; i++) expect(() => decodeBrowserInput(golden.slice(0, i))).toThrow()
    expect(() => decodeBrowserInput(Uint8Array.from([...golden, 0]))).toThrow()
  })
  it.each([[0,0], [3,2], [4,0], [4,8], [8,128], [9,255], [11,255]])('rejects corrupt header or UTF-8 at %i', (index, value) => {
    const bad = golden.slice(); bad[index] = value
    expect(() => decodeBrowserInput(bad)).toThrow()
  })
  it('rejects nonfinite numbers, oversized packets, and lossy surrogate replacement', () => {
    expect(() => encodeBrowserInput({ ...frame, x: Infinity })).toThrow()
    expect(() => encodeBrowserInput({ ...frame, text: '\ud800' })).toThrow()
    expect(() => decodeBrowserInput(new Uint8Array(65537))).toThrow()
    const bad = golden.slice(); new DataView(bad.buffer).setFloat64(12, NaN, true)
    expect(() => decodeBrowserInput(bad)).toThrow()
  })
})

// Explicit bit assignments are copied from the v1 table, not codec descriptors.
it.each([
 ['x',0,1], ['y',1,1], ['capture_width',2,1], ['capture_height',3,1],
 ['button',4,'a'], ['delta_x',5,1], ['delta_y',6,1], ['key',7,'a'],
 ['code',8,'a'], ['key_code',9,1], ['text',10,'a'], ['modifiers',11,1],
 ['url',12,'a'], ['capture_generation',13,1], ['capture_id',14,'a'],
 ['input_epoch',15,1], ['control_epoch',16,1], ['reliable_seq',17,1],
 ['hover_seq',18,1], ['gesture_barrier',19,1],
] as const)('assigns %s its independent v1 bit', (field, bit, value) => {
 const mask = 2 ** bit
 const bytes = Uint8Array.from([79,66,73,1,7,mask & 255,(mask >>> 8) & 255,(mask >>> 16) & 255,0,...(typeof value === 'string' ? [1,0,97] : [0,0,0,0,0,0,240,63])])
 const expected = { type: 'browser_input' as const, kind: 'text' as const, [field]: value }
 expect(decodeBrowserInput(bytes)).toEqual(expected)
 expect(encodeBrowserInput(expected)).toEqual(bytes)
})

it('preserves leading U+FEFF and an explicitly empty text field', () => {
 for (const [text, payload] of [['\uFEFFa', [4,0,239,187,191,97]], ['', [0,0]]] as const) {
  const expected = { type: 'browser_input' as const, kind: 'text' as const, text }
  const bytes = Uint8Array.from([79,66,73,1,7,0,4,0,0,...payload])
  expect(decodeBrowserInput(bytes)).toEqual(expected)
  expect(encodeBrowserInput(expected)).toEqual(bytes)
 }
})
