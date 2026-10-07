/**
 * T-04 / T-14 attention projection and the shown-commit acknowledgement.
 * Oracles: FR-012, FR-013, FR-033, BDD-04.1, BDD-04.2, BDD-04.4, BDD-09.1,
 * BDD-E03, BDD-E04, datasets A01–A09, N08. Founder Q-G1 and Q-M6.
 *
 * The seam is the only reader of needs_attention. Titles are temptations:
 * prose such as "question" or "met" must not create a signal.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '@/lib/api'

const SPEC = '@/lib/nav/mainAttention'

type Signal = 'on' | 'off' | 'unknown'

type Projection = {
  byMainId: Record<string, Signal>
  distinctMainCount: number
  needsMeMainIds: string[]
}

type AckInput = {
  attemptKind:
    | 'shown-commit'
    | 'prefetch'
    | 'hidden-reconnect'
    | 'replay'
    | 'failed-attach'
    | 'forbidden'
    | 'overtaken'
    | 'late-success'
    | 'late-failure'
  session: Session
  generation: number
  observedBound: string | null
  newerOutcomeId: string | null
  foreground: { sessionId: string; generation: number; observedBound: string } | null
  viewerId: string
}

type AckResult = {
  acknowledge: boolean
  sessionId?: string
  observedBound?: string
  fields?: Record<string, unknown>
}

const seam = vi.hoisted(() => {
  const attention = new Map<string, Signal>()
  const mains = new Set<string>()
  return {
    attention,
    mains,
    sessionAttention: vi.fn((session: unknown): Signal => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return attention.get(id) ?? 'unknown'
    }),
    isMainSession: vi.fn((session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return mains.has(id)
    }),
    mainSessionIdOfMember: vi.fn((_member: unknown): string | undefined => undefined),
    attachAckFields: vi.fn((bound: unknown) => {
      const observed = bound && typeof bound === 'object' && 'observedBound' in bound
        ? String((bound as { observedBound: unknown }).observedBound)
        : ''
      return { ack_attention: true, observed_bound: observed }
    }),
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: unknown) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: (bound: unknown) => seam.attachAckFields(bound),
}))

function session(id: string, title: string, agentId = 'mia'): Session {
  return {
    id,
    agent_id: agentId,
    title,
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 0,
    workspace_id: 'operations',
  }
}

async function load(): Promise<{
  projectMainAttention: (sessions: Session[]) => Projection
  ackForShownCommit: (input: AckInput) => AckResult
}> {
  return await import(/* @vite-ignore */ SPEC) as {
    projectMainAttention: (sessions: Session[]) => Projection
    ackForShownCommit: (input: AckInput) => AckResult
  }
}

async function expectProjection(sessions: Session[], expected: Projection, specRef: string): Promise<void> {
  let actual: Projection
  try {
    actual = (await load()).projectMainAttention(sessions)
  } catch (err) {
    expect.fail(
      `BLOCKED: ${SPEC} projectMainAttention not implemented — required by ${specRef}. Expected ${JSON.stringify(expected)}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
    )
  }
  expect(actual.byMainId).toEqual(expected.byMainId)
  expect(actual.distinctMainCount).toBe(expected.distinctMainCount)
  expect([...actual.needsMeMainIds].sort()).toEqual([...expected.needsMeMainIds].sort())
  expect(seam.attachAckFields).not.toHaveBeenCalled()
}

beforeEach(() => {
  seam.attention.clear()
  seam.mains.clear()
  seam.sessionAttention.mockClear()
  seam.isMainSession.mockClear()
  seam.attachAckFields.mockClear()
})

describe('projectMainAttention (T-04, A01–A05, A09)', () => {
  it('A01 a main stays on for a question, an approval, and both, until that source resolves', async () => {
    const question = session('main-question', 'pending question card')
    const approval = session('main-approval', 'pending approval')
    const both = session('main-both', 'question and approval')
    for (const id of ['main-question', 'main-approval', 'main-both']) {
      seam.mains.add(id)
      seam.attention.set(id, 'on')
    }
    await expectProjection([question, approval, both], {
      byMainId: { 'main-question': 'on', 'main-approval': 'on', 'main-both': 'on' },
      distinctMainCount: 3,
      needsMeMainIds: ['main-question', 'main-approval', 'main-both'],
    }, 'FR-012, BDD-04.1, dataset A01')

    seam.attention.set('main-question', 'off')
    seam.attachAckFields.mockClear()
    await expectProjection([question, approval, both], {
      byMainId: { 'main-question': 'off', 'main-approval': 'on', 'main-both': 'on' },
      distinctMainCount: 2,
      needsMeMainIds: ['main-approval', 'main-both'],
    }, 'dataset A01 true until the source resolves')
  })

  it('A02 unseen met, rounds_exhausted, and other stay on when the seam says on', async () => {
    const met = session('main-met', 'unseen met')
    const exhausted = session('main-exhausted', 'rounds_exhausted')
    const other = session('main-other', 'other failure')
    for (const id of ['main-met', 'main-exhausted', 'main-other']) {
      seam.mains.add(id)
      seam.attention.set(id, 'on')
    }
    await expectProjection([met, exhausted, other], {
      byMainId: { 'main-met': 'on', 'main-exhausted': 'on', 'main-other': 'on' },
      distinctMainCount: 3,
      needsMeMainIds: ['main-met', 'main-exhausted', 'main-other'],
    }, 'FR-012, BDD-04.1, dataset A02')
  })

  it('A03 stopped, unread text, and a global verdict stay off, and titles do not turn them on', async () => {
    const stopped = session('main-stopped', 'stopped_by_user goal met question')
    const unread = session('main-unread', 'ordinary unread free-text question')
    const global = session('main-global', 'generic task notice global plan verdict')
    for (const id of ['main-stopped', 'main-unread', 'main-global']) {
      seam.mains.add(id)
      seam.attention.set(id, 'off')
    }
    await expectProjection([stopped, unread, global], {
      byMainId: { 'main-stopped': 'off', 'main-unread': 'off', 'main-global': 'off' },
      distinctMainCount: 0,
      needsMeMainIds: [],
    }, 'FR-012, BDD-04.1, dataset A03')
  })

  it('A04 an extra or helper on-signal is not borrowed by Needs me', async () => {
    const main = session('main-quiet', 'Mia main')
    const extra = session('extra-waiting', 'extra has a question')
    const helper = session('helper-waiting', 'helper awaits a decision')
    seam.mains.add('main-quiet')
    seam.attention.set('main-quiet', 'off')
    seam.attention.set('extra-waiting', 'on')
    seam.attention.set('helper-waiting', 'on')
    await expectProjection([main, extra, helper], {
      byMainId: { 'main-quiet': 'off' },
      distinctMainCount: 0,
      needsMeMainIds: [],
    }, 'FR-012, FR-033, BDD-04.1, BDD-09.1, dataset A04')
  })

  it('A05 missing attention stays unknown and is not off, zero, or Needs me', async () => {
    const main = session('main-missing', 'Mia')
    seam.mains.add('main-missing')
    seam.attention.set('main-missing', 'unknown')
    await expectProjection([main], {
      byMainId: { 'main-missing': 'unknown' },
      distinctMainCount: 0,
      needsMeMainIds: [],
    }, 'FR-011, BDD-04.4, dataset A05')
  })

  it('A09 counts two distinct mains, not the extra, the helper, or a duplicate of the same main', async () => {
    const first = session('main-1', 'First')
    const firstAgain = session('main-1', 'First duplicate')
    const second = session('main-2', 'Second')
    const quiet = session('main-quiet', 'Quiet')
    const extra = session('extra-1', 'Extra')
    const helper = session('helper-1', 'Helper')
    for (const id of ['main-1', 'main-2', 'main-quiet']) seam.mains.add(id)
    seam.attention.set('main-1', 'on')
    seam.attention.set('main-2', 'on')
    seam.attention.set('main-quiet', 'off')
    seam.attention.set('extra-1', 'on')
    seam.attention.set('helper-1', 'on')
    await expectProjection([first, firstAgain, second, quiet, extra, helper], {
      byMainId: { 'main-1': 'on', 'main-2': 'on', 'main-quiet': 'off' },
      distinctMainCount: 2,
      needsMeMainIds: ['main-1', 'main-2'],
    }, 'FR-014, BDD-04.3, BDD-09.1, dataset A09')
  })
})

describe('ackForShownCommit (T-14, A06–A08, N08)', () => {
  async function ack(input: AckInput, specRef: string): Promise<AckResult> {
    try {
      return (await load()).ackForShownCommit(input)
    } catch (err) {
      expect.fail(
        `BLOCKED: ${SPEC} ackForShownCommit not implemented — required by ${specRef}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
      )
    }
  }

  function shown(sessionId: string, generation: number, observedBound: string, viewerId: string, newer: string | null): AckInput {
    return {
      attemptKind: 'shown-commit',
      session: session('placeholder', 'placeholder'),
      generation,
      observedBound,
      newerOutcomeId: newer,
      foreground: { sessionId, generation, observedBound },
      viewerId,
    }
  }

  it('A06 / BDD-E04 acknowledges goal1 only, including a delayed retry, and does not expand to goal2', async () => {
    const goal = session('main-a', 'goal')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'on')
    const input = (viewerId: string): AckInput => ({
      ...shown('main-a', 1, 'goal1', viewerId, 'goal2'),
      session: goal,
    })
    const expected = {
      acknowledge: true,
      sessionId: 'main-a',
      observedBound: 'goal1',
      fields: { ack_attention: true, observed_bound: 'goal1' },
    }
    expect(await ack(input('user-a'), 'FR-013, BDD-E04, dataset A06')).toEqual(expected)
    expect(await ack(input('user-a'), 'dataset A06 delayed retry does not recapture')).toEqual(expected)
    expect(seam.attachAckFields).toHaveBeenCalledTimes(2)
    for (const call of seam.attachAckFields.mock.calls) {
      expect(call[0]).toEqual({ sessionId: 'main-a', observedBound: 'goal1' })
    }
  })

  it('A07 the same shown bound is shared by both people and does not clear decisions', async () => {
    const goal = session('main-a', 'goal plus question')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'on')
    const expected = {
      acknowledge: true,
      sessionId: 'main-a',
      observedBound: 'goal1',
      fields: { ack_attention: true, observed_bound: 'goal1' },
    }
    const first = await ack({ ...shown('main-a', 1, 'goal1', 'user-a', null), session: goal }, 'BDD-04.2, dataset A07')
    const second = await ack({ ...shown('main-a', 1, 'goal1', 'user-b', null), session: goal }, 'BDD-04.2, dataset A07')
    expect(first).toEqual(expected)
    expect(second).toEqual(expected)
    expect(JSON.stringify(first.fields)).not.toContain('user-a')
    expect(JSON.stringify(second.fields)).not.toContain('user-b')
    expect(first.fields).not.toHaveProperty('clear_decisions')
  })

  it.each(['prefetch', 'hidden-reconnect', 'replay', 'failed-attach', 'forbidden', 'overtaken'] as const)(
    'A08 a %s of the same main does not acknowledge',
    async (attemptKind) => {
      const goal = session('main-a', 'goal')
      seam.mains.add('main-a')
      seam.attention.set('main-a', 'on')
      const result = await ack({
        attemptKind,
        session: goal,
        generation: 1,
        observedBound: 'goal1',
        newerOutcomeId: null,
        foreground: { sessionId: 'main-a', generation: 1, observedBound: 'goal1' },
        viewerId: 'user-a',
      }, 'FR-013, BDD-04.4, dataset A08')
      expect(result.acknowledge).toBe(false)
      expect(result.fields).toBeUndefined()
      expect(result.observedBound).toBeUndefined()
      expect(seam.attachAckFields).not.toHaveBeenCalled()
    },
  )

  it('A05 / BDD-04.4 a shown main with unknown attention does not acknowledge', async () => {
    const goal = session('main-a', 'goal')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'unknown')
    const result = await ack({ ...shown('main-a', 1, 'goal1', 'user-a', null), session: goal }, 'BDD-04.4, dataset A05')
    expect(result.acknowledge).toBe(false)
    expect(seam.attachAckFields).not.toHaveBeenCalled()
  })

  it('A04 a shown non-main does not acknowledge', async () => {
    const extra = session('extra-1', 'extra goal')
    seam.attention.set('extra-1', 'on')
    const result = await ack({
      ...shown('extra-1', 1, 'goal1', 'user-a', null),
      session: extra,
    }, 'FR-012, dataset A04')
    expect(result.acknowledge).toBe(false)
    expect(seam.attachAckFields).not.toHaveBeenCalled()
  })

  it('N08 / BDD-E03 late A success after B wins does not acknowledge or replace B', async () => {
    const losing = session('session-a', 'losing A')
    seam.mains.add('session-a')
    seam.attention.set('session-a', 'on')
    const result = await ack({
      attemptKind: 'late-success',
      session: losing,
      generation: 1,
      observedBound: 'goal-a',
      newerOutcomeId: null,
      foreground: { sessionId: 'session-b', generation: 2, observedBound: 'goal-b' },
      viewerId: 'user-a',
    }, 'FR-013, BDD-02.4, BDD-E03, dataset N08')
    expect(result.acknowledge).toBe(false)
    expect(result.fields).toBeUndefined()
    expect(result.observedBound).toBeUndefined()
    expect(seam.attachAckFields).not.toHaveBeenCalled()
  })

  it('N08 A→B→A late first-A does not reuse the old bound; the later winning A has its own', async () => {
    const mainA = session('session-a', 'A')
    seam.mains.add('session-a')
    seam.attention.set('session-a', 'on')
    const losing = await ack({
      attemptKind: 'late-success',
      session: mainA,
      generation: 1,
      observedBound: 'goal-a1',
      newerOutcomeId: 'goal-a3',
      foreground: { sessionId: 'session-a', generation: 3, observedBound: 'goal-a3' },
      viewerId: 'user-a',
    }, 'BDD-E03, dataset N08')
    expect(losing.acknowledge).toBe(false)
    expect(seam.attachAckFields).not.toHaveBeenCalled()

    const winning = await ack({
      attemptKind: 'shown-commit',
      session: mainA,
      generation: 3,
      observedBound: 'goal-a3',
      newerOutcomeId: null,
      foreground: { sessionId: 'session-a', generation: 3, observedBound: 'goal-a3' },
      viewerId: 'user-a',
    }, 'BDD-E03 later winning A has its own bound')
    expect(winning).toEqual({
      acknowledge: true,
      sessionId: 'session-a',
      observedBound: 'goal-a3',
      fields: { ack_attention: true, observed_bound: 'goal-a3' },
    })
  })
})
