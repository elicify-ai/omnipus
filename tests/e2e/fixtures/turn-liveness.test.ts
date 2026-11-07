// turn-liveness.test.ts — vitest coverage for attachWhileTurnActive, the
// active-at-attach bracket used by replay-fidelity (c) (CHECK 3 finding F1).
//
// Faked edge: the Playwright Locator's isVisible() (browser). The helper's
// sampling order and its failure/propagation behaviour run for REAL. The
// expectations are the contract in sprint-i-historical-replay-fidelity-spec.md
// Scenario 9 (attach while the turn is live), not helper output: marker
// visible at both ends passes; missing at either end fails with a message
// naming that end; the attach is never started when the turn is already
// over; an attach failure propagates unchanged.

// @vitest-environment node
import { describe, expect, it } from 'vitest'
import {
  attachWhileTurnActive,
  TURN_ENDED_BEFORE_ATTACH,
  TURN_ENDED_DURING_ATTACH,
} from './turn-liveness'

/** A marker replaying a scripted visibility sequence, logging into `events`. */
function scriptedMarker(visibility: boolean[], events: string[]) {
  let i = 0
  return {
    isVisible: async () => {
      const v = visibility[i++]
      events.push(`sample:${v}`)
      return v
    },
  }
}

describe('attachWhileTurnActive', () => {
  it('returns the attach result when the marker is visible before AND after, sampling around the attach', async () => {
    const events: string[] = []
    const marker = scriptedMarker([true, true], events)
    const out = await attachWhileTurnActive(marker, async () => {
      events.push('attach')
      return 'proof'
    })
    expect(out).toBe('proof')
    expect(events).toEqual(['sample:true', 'attach', 'sample:true'])
  })

  it('fails on the BEFORE message and never starts the attach when the turn already ended', async () => {
    const events: string[] = []
    const marker = scriptedMarker([false], events)
    await expect(
      attachWhileTurnActive(marker, async () => {
        events.push('attach')
      }),
    ).rejects.toThrow(TURN_ENDED_BEFORE_ATTACH)
    expect(events).toEqual(['sample:false'])
  })

  it('fails on the DURING message when the turn ended before the attach completed', async () => {
    const events: string[] = []
    const marker = scriptedMarker([true, false], events)
    await expect(
      attachWhileTurnActive(marker, async () => {
        events.push('attach')
      }),
    ).rejects.toThrow(TURN_ENDED_DURING_ATTACH)
    expect(events).toEqual(['sample:true', 'attach', 'sample:false'])
  })

  it('names the two failure ends distinctly', () => {
    expect(TURN_ENDED_BEFORE_ATTACH).not.toBe(TURN_ENDED_DURING_ATTACH)
    expect(TURN_ENDED_BEFORE_ATTACH).toContain('before the second viewer started attaching')
    expect(TURN_ENDED_DURING_ATTACH).toContain("before the second viewer's attach completed")
  })

  it('propagates an attach failure unchanged without sampling afterwards', async () => {
    const events: string[] = []
    const marker = scriptedMarker([true, true], events)
    await expect(
      attachWhileTurnActive(marker, async () => {
        throw new Error('attach exploded')
      }),
    ).rejects.toThrow('attach exploded')
    expect(events).toEqual(['sample:true'])
  })
})
