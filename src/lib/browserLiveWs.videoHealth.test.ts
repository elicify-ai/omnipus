// browserLiveWs.videoHealth.test.ts — issue #674.
//
// Covers the socket edge for the new browser_video_health frame: it must be
// validated by the SAME generated zod schema every other frame goes through
// (hard constraint #8's "SPA edge validates every incoming payload"), routed
// to its own callback, and — when malformed — dropped and counted rather than
// forwarded.
//
// Also pins the copy, because the whole point of the frame is that the panel
// says something SPECIFIC. Copy that silently degraded to a generic sentence
// would pass a "did it render" test and fail the requirement.

import { describe, it, expect } from 'vitest'
import { parseBrowserFrame, describeVideoHealth, getBrowserFrameDropCount } from '@/lib/browserLiveWs'

describe('parseBrowserFrame — browser_video_health', () => {
  it('accepts and narrows a well-formed frame', () => {
    const frame = parseBrowserFrame(
      JSON.stringify({
        type: 'browser_video_health',
        session_id: 's1',
        state: 'recovering',
        attempt: 2,
        max_attempts: 3,
        detail: 'the live browser video feed stopped',
      }),
    )
    expect(frame).not.toBeNull()
    expect(frame?.type).toBe('browser_video_health')
  })

  it('drops a frame whose state is outside the contract enum, and counts the drop', () => {
    const before = getBrowserFrameDropCount()
    const frame = parseBrowserFrame(
      JSON.stringify({ type: 'browser_video_health', state: 'exploded' }),
    )
    expect(frame).toBeNull()
    expect(getBrowserFrameDropCount()).toBe(before + 1)
  })

  it('drops a frame carrying an unknown extra field (the schema is strict)', () => {
    const before = getBrowserFrameDropCount()
    const frame = parseBrowserFrame(
      JSON.stringify({ type: 'browser_video_health', state: 'lost', surprise: true }),
    )
    expect(frame).toBeNull()
    expect(getBrowserFrameDropCount()).toBe(before + 1)
  })
})

describe('describeVideoHealth', () => {
  it('says nothing when there is nothing wrong', () => {
    expect(describeVideoHealth(null)).toBeNull()
    expect(describeVideoHealth({ type: 'browser_video_health', state: 'recovered' })).toBeNull()
  })

  it('names the bounded attempt so a retry cannot read as endless', () => {
    const msg = describeVideoHealth({
      type: 'browser_video_health',
      state: 'recovering',
      attempt: 2,
      max_attempts: 3,
    })
    expect(msg).toMatch(/attempt 2 of 3/i)
  })

  it('appends the gateway cause verbatim rather than paraphrasing it', () => {
    const msg = describeVideoHealth({
      type: 'browser_video_health',
      state: 'unrecoverable',
      attempt: 3,
      max_attempts: 3,
      detail: 'the capture encoder is not producing frames',
    })
    expect(msg).toMatch(/could not restart its video after 3 attempts/i)
    expect(msg).toContain('Reported cause: the capture encoder is not producing frames')
  })

  it('omits the attempt count when the gateway did not send one', () => {
    const msg = describeVideoHealth({ type: 'browser_video_health', state: 'recovering' })
    expect(msg).toMatch(/Reconnecting…/)
    expect(msg).not.toMatch(/attempt/i)
  })
})
