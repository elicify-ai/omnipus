// Squad-lead Q1 A instrument: synthetic replay painting, NOT automatic sizing.
import { describe, expect, it } from 'vitest'
import { parseFrameSafe } from '@/lib/ws'
import { replayPaintFrames } from '../../tests/e2e/fixtures/attach-replay-paint'

const request = { type: 'attach_session', session_id: 'paint-fixture-session', history_limit: 100, history_min_rows: 20 }

describe('synthetic replay-paint fixture preserves the original dataset and wire contract', () => {
  it('has122 deliveries,60 owning groups and90 logical rows, not120 tool/message sources', () => {
    const frames = replayPaintFrames(request)
    expect(frames).toHaveLength(122)
    for (const frame of frames) expect(parseFrameSafe(frame), 'each actual public carrier is generated-valid').toStrictEqual(frame)
    const snapshot = frames[0]
    if (snapshot.type !== 'session_snapshot') throw new Error('Fixture instrument: missing snapshot header')
    expect(snapshot).toMatchObject({ session_id: request.session_id, seq: 100, has_earlier: false, has_later: false,
      page_info: { mode: 'latest', limit: 100, min_rows: 20, source_group_count: 60, row_count: 90, stop_reason: 'end', byte_budget_exceeded: false } })
    const history = frames.slice(1, -1)
    expect(history).toHaveLength(120)
    const positions = history.map(frame => {
      if (!('history_origin' in frame) || !frame.history_origin) throw new Error('Fixture instrument: selected carrier has no occurrence origin')
      return frame.history_origin.position
    })
    expect(new Set(positions).size).toBe(60)
    for (let i = 0; i < 30; i++) {
      const offset = i * 4
      expect(positions[offset]).not.toBe(positions[offset + 1])
      expect(positions.slice(offset + 1, offset + 4)).toStrictEqual([positions[offset + 1], positions[offset + 1], positions[offset + 1]])
    }
    expect(snapshot.page_info.first_position).toBe(positions[0])
    expect(snapshot.page_info.last_position).toBe(positions[positions.length - 1])
    expect(history[history.length - 1]).toMatchObject({ type: 'replay_message', id: 'answer-29', content: 'REPLAY_LAST_ROW' })
    expect(frames[frames.length - 1]).toMatchObject({ type: 'done', stats: { frames_emitted: 120 } })
  })

  it.each([{}, { history_limit: 40, history_min_rows: 20 }, { history_limit: 100, history_min_rows: 40 }])('rejects a different actual wire request rather than inventing100/20: %j', sizing => {
    expect(() => replayPaintFrames({ type: 'attach_session', session_id: request.session_id, ...sizing }))
      .toThrow('Replay-paint fixture requires captured attach history_limit=100 and history_min_rows=20; not an automatic-sizing test')
  })
})
