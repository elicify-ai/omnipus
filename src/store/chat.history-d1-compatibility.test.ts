// #1452 D1 S1a/B86/T68: source identity is not occupation of a display key.
// Real store actions and snapshot reducer; only network/clock/socket edges fake.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { pos } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup } from '../../tests/chat-history-paging/final-gate-harness'
import { A, B, TURN, SID, LIVE_A, LIVE_B, assistantRows, bucket, commitSnapshot, entry, livePair, rest } from '../../tests/chat-history-paging/d1-alias-harness'

beforeEach(reset)
afterEach(cleanup)
const SAVED = 'A distinct saved occurrence in another turn.'

for (const carrier of ['REST', 'snapshot', 'refresh'] as const) {
  it.each(['alias only', 'occupied display key', 'two claimants'] as const)(`T68 B86: ${carrier} does not bind an incompatible or ambiguous source (%s)`, async shape => {
    livePair()
    const sourceId = shape === 'occupied display key' ? A : B
    if (shape === 'two claimants') {
      // Explicit adversarial public input, as in the protected alias-boundaries
      // test: copy the real alias claim, not a fabricated successful merge.
      useChatStore.getState().appendMessage({ ...bucket().messagesById[A], id: 'd1-second-owner', content: 'Other witnessed row.' })
    }
    const prior = assistantRows().map(row => ({ id: row.id, content: row.content }))
    const incoming = entry(9, { id: sourceId, role: 'assistant', agent_id: 'mia',
      turn_id: shape === 'two claimants' ? TURN : 'd1-other-turn', content: SAVED })
    if (carrier === 'snapshot') commitSnapshot([incoming])
    else {
      await rest([incoming])
      if (carrier === 'refresh') {
        useChatStore.getState().enterHistoryReading(SID)
        await rest([incoming], 'refresh')
      }
    }
    const rows = assistantRows()
    expect(rows.filter(row => row.content === SAVED), 'a distinct saved source remains readable exactly once').toHaveLength(1)
    for (const old of prior) {
      // D3's closed historical interval excludes a distant current tail. S1a
      // protects its text/binding, not membership in that historical viewport.
      // Check both owned collections without changing the shared visible-row
      // helper (which the other D1 order/duplicate assertions still require).
      expect(bucket().messagesById[old.id]?.content, 'no incompatible owner loses or absorbs text').toBe(old.content)
      expect([...bucket().messageOrder, ...(bucket().historyLiveOrder ?? [])].filter(id => id === old.id), 'preserved text has exactly one reader/current-tail placement, never orphaned or duplicated').toHaveLength(1)
    }
    const owner = bucket().historySourceAliases?.[pos(9)]
    expect(owner, 'the saved position names its actual displayed row').toBeDefined()
    expect(bucket().messagesById[owner!]).toMatchObject({ content: SAVED, turnId: incoming.turn_id, agentId: 'mia' })
    if (shape === 'occupied display key') {
      expect(owner, 'only an actual display-key collision needs a local key').not.toBe(sourceId)
      expect(owner).not.toBe('d1-second-owner')
      expect(bucket().messagesById[A].content).toBe(LIVE_A + LIVE_B)
    } else {
      expect(owner, 'S1a correction: an alias list does NOT occupy the source id display key').toBe(sourceId)
      expect(bucket().messagesById[sourceId].id).toBe(sourceId)
    }
  })
}

it.each(['different role', 'different agent'] as const)('T68 S1a: an occupied display id never steals a saved source with %s', async mismatch => {
  livePair()
  const source = entry(9, { id: A, role: mismatch === 'different role' ? 'user' : 'assistant',
    turn_id: TURN, agent_id: mismatch === 'different agent' ? 'jim' : 'mia', content: SAVED })
  await rest([source])
  expect(bucket().messagesById[A].content, 'original witnessed text stays on its original display row').toBe(LIVE_A + LIVE_B)
  const owner = bucket().historySourceAliases?.[pos(9)]
  expect(owner).toBeDefined()
  expect(owner).not.toBe(A)
  expect(bucket().messagesById[owner!]).toMatchObject({ content: SAVED, role: source.role, agentId: source.agent_id })
})

it('T68 S1a: a bound position cannot silently move to another source occurrence', async () => {
  livePair()
  const source = entry(9, { id: B, role: 'assistant', turn_id: TURN, agent_id: 'mia', content: LIVE_B })
  await rest([source])
  const before = { ...bucket().historySourceAliases }
  const body = bucket().messagesById[A].content
  const foreign = { ...source, id: 'd1-impossible-rebinding', turn_id: 'another-turn', content: 'Foreign occurrence' }
  // A deliberate protocol negative: the server reuses a position for another
  // identity. This may reject the page; it must never repoint the old binding.
  const selected = (await import('../../tests/chat-history-paging/fixtures')).page([foreign])
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify(selected), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  await useChatStore.getState().loadLatestHistory(SID)
  expect(bucket().historySourceAliases?.[pos(9)], 'S1a: source-occurrence binding is immutable').toBe(before[pos(9)])
  expect(bucket().messagesById[A]?.content).toBe(body)
})
