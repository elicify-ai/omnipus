// Semantic-fixture pack, D4/D10 and T33 (B23/B51). Structural Zod parsing
// alone accepted source_group_count=0 in the earlier CHECK mutant.
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SessionDetail } from '@/lib/api/generated/openapi-types'
import { makeSnapshotHistory } from '@/test/factories'
import { Stage1Network } from '../l1-room-ui/stage1-harness'
import { MAIN_ID } from '../l1-room-ui/harness'
import { expectPageAccounting, expectValidDetail, entry, page, pos, snapshot, expectValidFrame } from './fixtures'

afterEach(() => vi.unstubAllGlobals())

describe('T33 semantic fixture accounting — B23/B51', () => {
  it('available Stage1 detail is a complete zero-group page, not only an empty array', async () => {
    const network = new Stage1Network()
    const response = await network.fetch(`/api/v1/sessions/${MAIN_ID}`)
    expect(response.status).toBe(200)
    const detail = await response.json() as SessionDetail
    expectValidDetail(detail, [], 0)
    expect(detail.messages).toStrictEqual([])
    expect(network.unexpected).toStrictEqual([])
  })

  it.each([0, 1, 2] as const)('narration snapshot with %i sources has exact source and row counts', (count) => {
    const fixture = makeSnapshotHistory(count)
    // Independent frozen selectors used by the existing small replay factory.
    const first = `hp1_${'A'.repeat(43)}`
    const second = `hp1_${'A'.repeat(42)}E`
    const positions = count === 0 ? [] : count === 1 ? [first] : [first, second]
    expectPageAccounting(fixture, positions, count)
    expect({ groups: fixture.page_info.source_group_count, rows: fixture.page_info.row_count })
      .toStrictEqual({ groups: count, rows: count })
  })

  it('a snapshot with two source entries has two groups and narration rows despite control/done frames', () => {
    const sources = [entry(1), entry(2, { type: 'assistant', role: 'assistant', content: 'Saved answer' })]
    const history = page(sources)
    const frame = snapshot(history)
    expectValidFrame(frame)
    expectPageAccounting(frame, [pos(1), pos(2)], 2)
    // The real snapshot consumer must compare selected sources, not a count of
    // session_state / done / completion frames. This pins both lie-prone fields.
    expect(frame.page_info.source_group_count).toBe(2)
    expect(frame.page_info.row_count).toBe(2)
  })
})
