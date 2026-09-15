/**
 * ws.done-truncation-fields.test.ts — ADR-087 D2, finding #10.
 *
 * `DoneStats.truncated`/`DoneStats.truncation_reason` (new fields, mirrors
 * `Message.truncation_reason`) must survive the SAME production Zod
 * validation gate every other WS frame goes through — `parseFrameSafe`
 * (src/lib/ws.ts), backed by the generated `WsFrame` discriminated-union
 * schema (CLAUDE.md hard-constraint #8: the SPA edge validates every
 * incoming payload before it reaches any component). Pattern mirrored from
 * `ws.new-frames-validation.test.ts` / the existing `done` frame case in
 * `ws.test.ts`.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { parseFrameSafe, resetDroppedFrameCount, getDroppedFrameCount } from './ws'

beforeEach(() => {
  resetDroppedFrameCount()
})

describe('parseFrameSafe — done frame with DoneStats.truncated/truncation_reason (ADR-087 D2, finding #10)', () => {
  it('parses a done frame carrying stats.truncated + max_output_tokens, fields intact', () => {
    const result = parseFrameSafe(
      JSON.stringify({
        type: 'done',
        session_id: 's1',
        stats: { tokens: 512, cost: 0.002, truncated: true, truncation_reason: 'max_output_tokens' },
      }),
    )
    expect(result).not.toBeNull()
    expect(result?.type).toBe('done')
    if (result?.type === 'done') {
      expect(result.stats?.truncated).toBe(true)
      expect(result.stats?.truncation_reason).toBe('max_output_tokens')
    }
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('parses a done frame carrying stats.truncated + cancelled, fields intact', () => {
    const result = parseFrameSafe(
      JSON.stringify({
        type: 'done',
        session_id: 's1',
        stats: { tokens: 12, cost: 0.0001, truncated: true, truncation_reason: 'cancelled' },
      }),
    )
    expect(result).not.toBeNull()
    if (result?.type === 'done') {
      expect(result.stats?.truncated).toBe(true)
      expect(result.stats?.truncation_reason).toBe('cancelled')
    }
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('a plain done frame with no truncation fields still parses (both optional)', () => {
    const result = parseFrameSafe(
      JSON.stringify({ type: 'done', session_id: 's1', stats: { tokens: 100, cost: 0.01 } }),
    )
    expect(result).not.toBeNull()
    if (result?.type === 'done') {
      expect(result.stats?.truncated).toBeUndefined()
      expect(result.stats?.truncation_reason).toBeUndefined()
    }
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('drops a done frame whose truncation_reason is not one of the two enum members', () => {
    const result = parseFrameSafe(
      JSON.stringify({
        type: 'done',
        session_id: 's1',
        stats: { truncated: true, truncation_reason: 'timed_out' },
      }),
    )
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})
