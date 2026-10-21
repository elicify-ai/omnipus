/**
 * Above-feed kind label. Oracles: the labels table (`Extra chat — title`,
 * em dash) and BDD-07.1. Task and helper kinds pass through.
 *
 * FR-008 AMENDED (founder decision 2026-10-11, team-lead dispatch): a main
 * session's header line now reads the main's TITLE — "<Agent name> -
 * Main Session", ALWAYS derived from the agent's CURRENT display name via
 * sessionDisplayTitle (S2-05 ruling, chief-confirmed 2026-10-12: the stored
 * session.title is ignored for a main) — instead of the bare "Main chat".
 * Extra chats are unchanged: "Extra chat", or "Extra chat — title" when
 * there is one; no dangling dash.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '@/lib/api'

const SPEC = '@/lib/nav/chatKindLabel'
const EM = '—'

type KindModule = {
  feedKindLabel: (session: Session) => string | null
}

const seam = vi.hoisted(() => ({
  mains: new Set<string>(),
  isMainSession: vi.fn((session: unknown) => {
    const id = session && typeof session === 'object' && 'id' in session
      ? String((session as { id: unknown }).id)
      : ''
    return seam.mains.has(id)
  }),
  // Preserve seam signature parity for the forwarding mocks; these default
  // operations must remain unavailable regardless of their arguments.
  mainSessionIdOfMember: vi.fn((_member: unknown): string | undefined => { void _member; return undefined }),
  sessionAttention: vi.fn((_session: unknown): 'unknown' => { void _session; return 'unknown' }),
  attachAckFields: vi.fn((_bound: unknown) => { void _bound; return {} }),
  attentionBoundOfFrame: vi.fn((_frame: unknown): number | undefined => { void _frame; return undefined }),
}))

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

function session(partial: Partial<Session> & Pick<Session, 'id' | 'title'> & { agentName?: string; agentId?: string | null }): Session {
  return {
    agent_id: 'mia',
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    message_count: 1,
    ...partial,
  } as Session
}

async function expectLabel(value: Session, expected: string, specRef: string): Promise<void> {
  let actual: string | null
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

describe('feedKindLabel (BDD-07.1, mains as amended 2026-10-11)', () => {
  it('an untitled main reads "<Agent> - Main Session" from the agent’s CURRENT name (FR-008 amended, founder decision 2026-10-11)', async () => {
    seam.mains.add('sid-main')
    await expectLabel(
      session({ id: 'sid-main', title: '', agentName: 'Jim' }),
      'Jim - Main Session',
      'FR-008 amendment (founder, 2026-10-11): the header shows the main’s title, never the bare kind',
    )
  })

  it('a main IGNORES its stored title and derives from the CURRENT agent name (steer 2026-10-12 — no stored copy survives a rename)', async () => {
    seam.mains.add('sid-main')
    // The stored title is an attach-time snapshot persisted in the browser;
    // for a main it is never shown, even when it looks plausible.
    await expectLabel(
      session({ id: 'sid-main', title: 'Ava - Main Session', agentName: 'Eve' }),
      'Eve - Main Session',
      'team-lead steer (architect ruling) 2026-10-12: a main always derives from the live agent list',
    )
  })

  it('a titled main that was renamed shows the CURRENT agent’s derived title once the server title is gone (rename freshness, no caching)', async () => {
    seam.mains.add('sid-main')
    const untitledMain = session({ id: 'sid-main', title: '', agentName: 'Jim' })
    await expectLabel(untitledMain, 'Jim - Main Session', 'rename freshness — before')
    await expectLabel(
      session({ id: 'sid-main', title: '', agentName: 'Jim Prime' }),
      'Jim Prime - Main Session',
      'rename freshness — after: same session object, new CURRENT name, new label',
    )
  })

  it('a main whose name cannot be resolved falls back to the AGENT ID (the server’s own empty-name fallback), never "Unknown agent" (S4-04)', async () => {
    seam.mains.add('sid-main')
    await expectLabel(
      session({ id: 'sid-main', title: '', agentName: undefined as unknown as string, agentId: 'jim' }),
      'jim - Main Session',
      'S4-04/S2-05: the id is what the server itself shows (withDisplayTitle falls back to m.AgentID)',
    )
  })

  it('a main with NEITHER a resolvable name nor an agent id yields null — the caller’s honest unavailable presentation (S4-04)', async () => {
    seam.mains.add('sid-main')
    let actual: string | null
    try {
      const mod = await import(/* @vite-ignore */ SPEC) as KindModule
      actual = mod.feedKindLabel(session({ id: 'sid-main', title: '', agentName: undefined }))
    } catch (err) {
      expect.fail(`BLOCKED: ${SPEC} feedKindLabel not implemented: ${err instanceof Error ? err.message : String(err)}`)
    }
    expect(actual).toBeNull()
  })

  it('an extra with a title is "Extra chat — title" (unchanged)', async () => {
    await expectLabel(
      session({ id: 'sid-extra', title: 'Launch notes' }),
      `Extra chat ${EM} Launch notes`,
      'FR-008, BDD-07.1 example Extra chat — Launch notes',
    )
  })

  it('an extra with an empty title does not keep a dangling dash (unchanged)', async () => {
    await expectLabel(
      session({ id: 'sid-extra', title: '   ' }),
      'Extra chat',
      'labels table: the title is included only when there is one',
    )
  })

  it('a task session passes through as "Task run" (unchanged)', async () => {
    await expectLabel(
      session({ id: 'sid-task', type: 'task', title: 'Deploy' }),
      'Task run',
      'FR-008, BDD-07.1 Task run',
    )
  })

  it('a subordinate session passes through as "Helper" (unchanged)', async () => {
    await expectLabel(
      session({ id: 'sid-helper', type: 'delegate', title: 'Scan' }),
      'Helper',
      'FR-008, BDD-07.1 Helper',
    )
  })
})
