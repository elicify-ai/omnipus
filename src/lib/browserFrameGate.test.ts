import { describe, expect, it } from 'vitest'
import { BrowserFrameGate } from './browserFrameGate'

// FR009 contract: only a presented frame at/after the authoritative RTP marker
// can authorize input. Millisecond values are explicit synthetic compositor times.
function setup(marker = 100) {
  const gate = new BrowserFrameGate()
  const stream = {}
  gate.expectGeneration(1)
  gate.bindStream(stream)
  gate.acceptBoundary({ generation: 1, rtpTimestamp: marker })
  return { gate, stream }
}

describe('BrowserFrameGate', () => {
  it('waits until compositor presentation, including the exact boundary', () => {
    const { gate, stream } = setup()
    expect(gate.observeFrame(stream, { rtpTimestamp: 100, expectedDisplayTime: 20 }, 19)).toEqual({ status: 'presenting', generation: 1, displayAt: 20 })
    expect(gate.read(20)).toEqual({ status: 'ready', generation: 1 })
    expect(gate.read(21)).toEqual({ status: 'ready', generation: 1 })
  })

  it.each([
    [0xffffffff, 0, true], [0, 0xffffffff, false], [0, 0, true],
    [0, 0x7fffffff, true], [0, 0x80000000, false], [0, 0x80000001, false],
    [100, 99, false], [100, 101, true],
  ])('compares marker %s and RTP %s using uint32 serial order', (marker, timestamp, ready) => {
    const { gate, stream } = setup(marker)
    expect(gate.observeFrame(stream, { rtpTimestamp: timestamp, expectedDisplayTime: 20 }, 20)).toEqual(ready ? { status: 'ready', generation: 1 } : { status: 'locked', reason: 'frame-required' })
  })

  it('matches a cached static frame when its boundary arrives later', () => {
    const gate = new BrowserFrameGate()
    const stream = {}
    gate.bindStream(stream)
    gate.expectGeneration(1)
    gate.observeFrame(stream, { rtpTimestamp: 123, expectedDisplayTime: 20 }, 20)
    gate.acceptBoundary({ generation: 1, rtpTimestamp: 123 })
    expect(gate.read(21)).toEqual({ status: 'ready', generation: 1 })
  })

  it('invalidates a scheduled presentation on a new generation and ignores stale boundaries', () => {
    const { gate, stream } = setup()
    gate.observeFrame(stream, { rtpTimestamp: 100, expectedDisplayTime: 20 }, 19)
    gate.expectGeneration(2)
    gate.acceptBoundary({ generation: 1, rtpTimestamp: 100 })
    expect(gate.read(1000)).toEqual({ status: 'locked', reason: 'boundary-required' })
    gate.acceptBoundary({ generation: 2, rtpTimestamp: 200 })
    expect(gate.read(1000)).toEqual({ status: 'locked', reason: 'frame-required' })
  })

  it('invalidates presentation on stream replacement and ignores old callbacks', () => {
    const { gate, stream } = setup()
    gate.observeFrame(stream, { rtpTimestamp: 100, expectedDisplayTime: 20 }, 19)
    gate.bindStream({})
    expect(gate.observeFrame(stream, { rtpTimestamp: 101, expectedDisplayTime: 20 }, 1000)).toEqual({ status: 'locked', reason: 'frame-required' })
    gate.bindStream(null)
    expect(gate.read(1000)).toEqual({ status: 'locked', reason: 'stream-required' })
  })

  it('keeps duplicate boundaries idempotent but requires evidence for a changed marker', () => {
    const { gate, stream } = setup()
    gate.observeFrame(stream, { rtpTimestamp: 100, expectedDisplayTime: 20 }, 20)
    gate.acceptBoundary({ generation: 1, rtpTimestamp: 100 })
    expect(gate.read(20)).toEqual({ status: 'ready', generation: 1 })
    gate.acceptBoundary({ generation: 1, rtpTimestamp: 101 })
    expect(gate.read(1000)).toEqual({ status: 'locked', reason: 'frame-required' })
  })

  it.each([undefined, -1, 0, NaN, Infinity])('never substitutes a clock timeout for unavailable presentation time %s', expectedDisplayTime => {
    const { gate, stream } = setup()
    gate.observeFrame(stream, { rtpTimestamp: 100, expectedDisplayTime }, 20)
    expect(gate.read(1000000)).toEqual({ status: 'locked', reason: 'presentation-time-unavailable' })
  })

  it('requests fresh viewer lineage for missing RTP without timeout unlocking', () => {
    const { gate, stream } = setup()
    gate.observeFrame(stream, { expectedDisplayTime: 20 }, 20)
    expect(gate.read(1000000)).toEqual({ status: 'needs-fresh-viewer', generation: 1 })
  })

  it('allows only a previously unseen server-authorized stream for the committed generation', () => {
    const { gate, stream } = setup()
    expect(gate.bindFreshViewer(stream, 1)).toBe(false)
    expect(gate.bindFreshViewer({}, 2)).toBe(false)
    const fresh = {}
    expect(gate.bindFreshViewer(fresh, 1)).toBe(true)
    expect(gate.observeFrame(fresh, { expectedDisplayTime: 30 }, 29)).toEqual({ status: 'presenting', generation: 1, displayAt: 30 })
    expect(gate.read(30)).toEqual({ status: 'ready', generation: 1 })
    gate.expectGeneration(2)
    expect(gate.read(1000)).toEqual({ status: 'locked', reason: 'boundary-required' })
    expect(gate.bindFreshViewer({}, 2)).toBe(false)
  })

  it('does not allow fallback lineage to override contradictory RTP evidence', () => {
    const { gate } = setup()
    const fresh = {}
    gate.bindFreshViewer(fresh, 1)
    expect(gate.observeFrame(fresh, { rtpTimestamp: 99, expectedDisplayTime: 20 }, 20)).toEqual({ status: 'locked', reason: 'frame-required' })
  })

  it('retains the first matching presentation despite later incomplete metadata', () => {
    const { gate, stream } = setup()
    gate.observeFrame(stream, { rtpTimestamp: 100, expectedDisplayTime: 20 }, 20)
    expect(gate.observeFrame(stream, {}, 21)).toEqual({ status: 'ready', generation: 1 })
  })

  it.each([-1, 0, 0.5, Number.MAX_SAFE_INTEGER + 1, NaN, Infinity])('rejects invalid generation %s', generation => {
    expect(() => new BrowserFrameGate().expectGeneration(generation)).toThrowError(new RangeError('generation must be a positive safe integer'))
  })

  it.each([1, 2, Number.MAX_SAFE_INTEGER - 1, Number.MAX_SAFE_INTEGER])('accepts generation boundary %s', generation => {
    const gate = new BrowserFrameGate()
    gate.expectGeneration(generation)
    expect(gate.read(1)).toEqual({ status: 'locked', reason: 'stream-required' })
  })

  it.each([-1, 0.5, 0x100000000, NaN, Infinity])('rejects invalid boundary RTP %s', rtpTimestamp => {
    expect(() => setup(rtpTimestamp)).toThrowError(new RangeError('rtpTimestamp must be a uint32'))
  })
})
