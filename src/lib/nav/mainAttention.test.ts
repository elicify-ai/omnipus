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
  frame: Record<string, unknown> | null
  newerOutcomeId: number | null
  foreground: { sessionId: string; generation: number; frame: Record<string, unknown> | null } | null
  viewerId: string
}

type AckResult = {
  acknowledge: boolean
  sessionId?: string
  attentionBound?: number
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
    attentionBoundOfFrame: vi.fn((frame: unknown): number | undefined => {
      if (!frame || typeof frame !== 'object' || !('attention_bound' in frame)) return undefined
      const value = (frame as { attention_bound: unknown }).attention_bound
      return typeof value === 'number' && Number.isInteger(value) ? value : undefined
    }),
    attachAckFields: vi.fn((bound: unknown) => {
      if (typeof bound !== 'number' || !Number.isInteger(bound)) return {}
      return { ack_attention: true, attention_bound: bound }
    }),
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: unknown) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: (bound: unknown) => seam.attachAckFields(bound),
  attentionBoundOfFrame: (frame: unknown) => seam.attentionBoundOfFrame(frame),
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
  seam.attentionBoundOfFrame.mockClear()
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

  // PLAN 5.2: attention_bound is an integer on the server snapshot. The old
  // goal-id strings were not a wire field. 1 is A06's shown bound, 2 is the
  // newer outcome that must not be acked, 3 is the later winning A.
  function serverFrame(sessionId: string, attentionBound?: number): Record<string, unknown> {
    const frame: Record<string, unknown> = {
      type: 'session_snapshot',
      session_id: sessionId,
      seq: 4,
    }
    if (attentionBound !== undefined) frame.attention_bound = attentionBound
    return frame
  }

  function shown(sessionId: string, generation: number, attentionBound: number, viewerId: string, newer: number | null): AckInput {
    const frame = serverFrame(sessionId, attentionBound)
    return {
      attemptKind: 'shown-commit',
      session: session('placeholder', 'placeholder'),
      generation,
      frame,
      newerOutcomeId: newer,
      foreground: { sessionId, generation, frame },
      viewerId,
    }
  }

  it('A06 / BDD-E04 acknowledges bound 1 only, including a delayed retry, and does not expand to bound 2', async () => {
    const goal = session('main-a', 'goal')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'on')
    const input = (viewerId: string): AckInput => ({
      ...shown('main-a', 1, 1, viewerId, 2),
      session: goal,
    })
    const expected = {
      acknowledge: true,
      sessionId: 'main-a',
      attentionBound: 1,
      fields: { ack_attention: true, attention_bound: 1 },
    }
    expect(await ack(input('user-a'), 'FR-013, BDD-E04, dataset A06')).toEqual(expected)
    expect(await ack(input('user-a'), 'dataset A06 delayed retry does not recapture')).toEqual(expected)
    expect(seam.attentionBoundOfFrame).toHaveBeenCalledWith(expect.objectContaining({
      type: 'session_snapshot',
      session_id: 'main-a',
      attention_bound: 1,
    }))
    expect(seam.attachAckFields).toHaveBeenCalledTimes(2)
    for (const call of seam.attachAckFields.mock.calls) {
      expect(call[0]).toBe(1)
    }
  })

  it('A07 the same shown bound is shared by both people and does not clear decisions', async () => {
    const goal = session('main-a', 'goal plus question')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'on')
    const expected = {
      acknowledge: true,
      sessionId: 'main-a',
      attentionBound: 1,
      fields: { ack_attention: true, attention_bound: 1 },
    }
    const first = await ack({ ...shown('main-a', 1, 1, 'user-a', null), session: goal }, 'BDD-04.2, dataset A07')
    const second = await ack({ ...shown('main-a', 1, 1, 'user-b', null), session: goal }, 'BDD-04.2, dataset A07')
    expect(first).toEqual(expected)
    expect(second).toEqual(expected)
    expect(JSON.stringify(first.fields)).not.toContain('user-a')
    expect(JSON.stringify(second.fields)).not.toContain('user-b')
    expect(first.fields).not.toHaveProperty('clear_decisions')
    expect(first.fields).not.toHaveProperty('observed_bound')
  })

  it('a shown commit does not acknowledge when the server frame has no integer attention_bound', async () => {
    const goal = session('main-a', 'goal')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'on')
    const frame = serverFrame('main-a')
    const result = await ack({
      attemptKind: 'shown-commit',
      session: goal,
      generation: 1,
      frame,
      newerOutcomeId: null,
      foreground: { sessionId: 'main-a', generation: 1, frame, observedBound: 'goal1' },
      viewerId: 'user-a',
      observedBound: 'goal1',
    } as AckInput, 'PLAN 5.2: no ack without a server attention_bound')
    expect(result.acknowledge).toBe(false)
    expect(result.fields).toBeUndefined()
    expect(result.attentionBound).toBeUndefined()
    expect(seam.attachAckFields).not.toHaveBeenCalled()
  })

  it.each(['prefetch', 'hidden-reconnect', 'replay', 'failed-attach', 'forbidden', 'overtaken'] as const)(
    'A08 a %s of the same main does not acknowledge',
    async (attemptKind) => {
      const goal = session('main-a', 'goal')
      seam.mains.add('main-a')
      seam.attention.set('main-a', 'on')
      const frame = serverFrame('main-a', 1)
      const result = await ack({
        attemptKind,
        session: goal,
        generation: 1,
        frame,
        newerOutcomeId: null,
        foreground: { sessionId: 'main-a', generation: 1, frame },
        viewerId: 'user-a',
      }, 'FR-013, BDD-04.4, dataset A08')
      expect(result.acknowledge).toBe(false)
      expect(result.fields).toBeUndefined()
      expect(result.attentionBound).toBeUndefined()
      expect(seam.attachAckFields).not.toHaveBeenCalled()
    },
  )

  it('A05 / BDD-04.4 a shown main with unknown attention does not acknowledge', async () => {
    const goal = session('main-a', 'goal')
    seam.mains.add('main-a')
    seam.attention.set('main-a', 'unknown')
    const result = await ack({ ...shown('main-a', 1, 1, 'user-a', null), session: goal }, 'BDD-04.4, dataset A05')
    expect(result.acknowledge).toBe(false)
    expect(seam.attachAckFields).not.toHaveBeenCalled()
  })

  it('A04 a shown non-main does not acknowledge', async () => {
    const extra = session('extra-1', 'extra goal')
    seam.attention.set('extra-1', 'on')
    const result = await ack({
      ...shown('extra-1', 1, 1, 'user-a', null),
      session: extra,
    }, 'FR-012, dataset A04')
    expect(result.acknowledge).toBe(false)
    expect(seam.attachAckFields).not.toHaveBeenCalled()
  })

  it('N08 / BDD-E03 a shown commit with the same session and bound but a losing generation does not acknowledge', async () => {
    const mainA = session('session-a', 'losing open')
    seam.mains.add('session-a')
    seam.attention.set('session-a', 'on')
    const frame = serverFrame('session-a', 3)
    const result = await ack({
      attemptKind: 'shown-commit',
      session: mainA,
      generation: 1,
      frame,
      newerOutcomeId: null,
      foreground: { sessionId: 'session-a', generation: 3, frame },
      viewerId: 'user-a',
    }, 'FR-013, BDD-E03, dataset N08')
    expect(result.acknowledge, 'a losing generation is not the winning shown commit').toBe(false)
    expect(result.fields).toBeUndefined()
    expect(result.attentionBound).toBeUndefined()
    expect(result.sessionId).toBeUndefined()
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
      frame: serverFrame('session-a', 1),
      newerOutcomeId: null,
      foreground: { sessionId: 'session-b', generation: 2, frame: serverFrame('session-b', 2) },
      viewerId: 'user-a',
    }, 'FR-013, BDD-02.4, BDD-E03, dataset N08')
    expect(result.acknowledge).toBe(false)
    expect(result.fields).toBeUndefined()
    expect(result.attentionBound).toBeUndefined()
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
      frame: serverFrame('session-a', 1),
      newerOutcomeId: 3,
      foreground: { sessionId: 'session-a', generation: 3, frame: serverFrame('session-a', 3) },
      viewerId: 'user-a',
    }, 'BDD-E03, dataset N08')
    expect(losing.acknowledge).toBe(false)
    expect(seam.attachAckFields).not.toHaveBeenCalled()

    const winning = await ack({
      attemptKind: 'shown-commit',
      session: mainA,
      generation: 3,
      frame: serverFrame('session-a', 3),
      newerOutcomeId: null,
      foreground: { sessionId: 'session-a', generation: 3, frame: serverFrame('session-a', 3) },
      viewerId: 'user-a',
    }, 'BDD-E03 later winning A has its own bound')
    expect(winning).toEqual({
      acknowledge: true,
      sessionId: 'session-a',
      attentionBound: 3,
      fields: { ack_attention: true, attention_bound: 3 },
    })
    expect(winning.fields).not.toHaveProperty('observed_bound')
  })
})
