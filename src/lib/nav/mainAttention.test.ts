/**
 * T-04 attention projection. Oracles: FR-012, FR-014, FR-033, BDD-04.1,
 * BDD-04.3, BDD-04.4, BDD-09.1, datasets A01–A05, A09.
 *
 * The seam is the only reader of needs_attention. Titles are temptations:
 * prose such as "question" or "met" must not create a signal.
 *
 * M6: the shown-commit acknowledgement pins that used to live here (T-14,
 * A06–A08, N08) asserted a second copy of the live rules; they are asserted
 * against the live foregroundAck path in
 * src/store/session/foregroundAck.shown-commit.test.ts instead.
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
    // The forwarding mock retains the seam argument; this unavailable
    // default must not manufacture a main ID from any member value.
    mainSessionIdOfMember: vi.fn((_member: unknown): string | undefined => { void _member; return undefined }),
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

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  // Real seam for everything the stubs don't override. (WU-11 GREEN removed the
  // Admin-main seam reader with the retired Admin row — FR-008.)
  ...await importOriginal<typeof import('@/lib/nav/sessionCoreSeam')>(),
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
}> {
  return await import(/* @vite-ignore */ SPEC) as {
    projectMainAttention: (sessions: Session[]) => Projection
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
