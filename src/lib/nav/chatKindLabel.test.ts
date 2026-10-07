/**
 * Above-feed kind label. Oracles: the labels table (`Main chat` /
 * `Extra chat — title`, em dash) and BDD-07.1. Task and helper kinds pass
 * through. A main does not grow a title suffix. An extra with no title does
 * not keep a dangling dash.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '@/lib/api'

const SPEC = '@/lib/nav/chatKindLabel'
const EM = '—'

type KindModule = {
  feedKindLabel: (session: Session) => string
}

const seam = vi.hoisted(() => ({
  mains: new Set<string>(),
  isMainSession: vi.fn((session: unknown) => {
    const id = session && typeof session === 'object' && 'id' in session
      ? String((session as { id: unknown }).id)
      : ''
    return seam.mains.has(id)
  }),
  mainSessionIdOfMember: vi.fn((_member: unknown): string | undefined => undefined),
  sessionAttention: vi.fn((_session: unknown): 'unknown' => 'unknown'),
  attachAckFields: vi.fn((_bound: unknown) => ({})),
}))

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: unknown) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: (bound: unknown) => seam.attachAckFields(bound),
}))

function session(partial: Partial<Session> & Pick<Session, 'id' | 'title'>): Session {
  return {
    agent_id: 'mia',
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    message_count: 1,
    ...partial,
  }
}

async function expectLabel(value: Session, expected: string, specRef: string): Promise<void> {
  let actual: string
  try {
    const mod = await import(/* @vite-ignore */ SPEC) as KindModule
    actual = mod.feedKindLabel(value)
  } catch (err) {
    expect.fail(
      `BLOCKED: ${SPEC} feedKindLabel not implemented — required by ${specRef}. Expected ${JSON.stringify(expected)}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
    )
  }
  expect(actual).toBe(expected)
}

beforeEach(() => {
  seam.mains.clear()
  seam.isMainSession.mockClear()
})

describe('feedKindLabel (BDD-07.1)', () => {
  it('a main is exactly "Main chat", with no title suffix', async () => {
    seam.mains.add('sid-main')
    await expectLabel(
      session({ id: 'sid-main', title: 'Launch notes' }),
      'Main chat',
      'FR-008, BDD-07.1, labels table',
    )
  })

  it('an extra with a title is "Extra chat — title"', async () => {
    await expectLabel(
      session({ id: 'sid-extra', title: 'Launch notes' }),
      `Extra chat ${EM} Launch notes`,
      'FR-008, BDD-07.1 example Extra chat — Launch notes',
    )
  })

  it('an extra with an empty title does not keep a dangling dash', async () => {
    await expectLabel(
      session({ id: 'sid-extra', title: '   ' }),
      'Extra chat',
      'labels table: the title is included only when there is one',
    )
  })

  it('a task session passes through as "Task run"', async () => {
    await expectLabel(
      session({ id: 'sid-task', type: 'task', title: 'Deploy' }),
      'Task run',
      'FR-008, BDD-07.1 Task run',
    )
  })

  it('a subordinate session passes through as "Helper"', async () => {
    await expectLabel(
      session({ id: 'sid-helper', type: 'delegate', title: 'Scan' }),
      'Helper',
      'FR-008, BDD-07.1 Helper',
    )
  })
})
