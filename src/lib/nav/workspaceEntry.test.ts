/**
 * T-02 workspace entry. Oracles: FR-004, BDD-02.1, BDD-02.2, BDD-02.3,
 * datasets N03, N04, N05, N06, N07. Foreground commitment step 1: entry
 * validates and attaches without acknowledgement.
 *
 * Welcome ids are opaque seam returns for Ava's member, not a client-built
 * `main-session-…` id and not Ava's newer extra.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, WorkspaceMemberConfig } from '@/lib/api'

const SPEC = '@/lib/nav/workspaceEntry'

type EntryKind = 'workspace-name' | 'cold-reload' | 'login' | 'modal-switch'
type PointerVerdict = 'visible' | 'deleted' | 'hidden' | 'forbidden' | 'timeout' | 'offline' | 'server-error' | 'absent'

type EntryInput = {
  workspaceId: string
  entryKind: EntryKind
  rememberedSessionId: string | null | undefined
  pointerVerdict: PointerVerdict
  sessions: Session[]
  avaMember: WorkspaceMemberConfig
  committed: { sessionId: string; agentId: string } | null
}

type ExactEntry = {
  status: 'exact'
  sessionId: string
  agentId: string
  sendEnabled: true
  acknowledged: false
}

type WelcomeEntry = {
  status: 'welcome'
  sessionId: string
  agentId: 'ava'
  sendEnabled: true
  acknowledged: false
}

type UnavailableEntry = {
  status: 'unavailable'
  sessionId: null
  sendEnabled: false
  acknowledged: false
  retry: true
}

type FailedAttempt = {
  status: 'failed-attempt'
  committed: { sessionId: string; agentId: string } | null
  attempted: { sessionId: string; sendEnabled: false }
  acknowledged: false
  retry: true
  fellBack: false
}

type EntryResult = ExactEntry | WelcomeEntry | UnavailableEntry | FailedAttempt

const seam = vi.hoisted(() => {
  const mains = new Map<object, string | undefined>()
  return {
    mains,
    mainSessionIdOfMember: vi.fn((member: object) => mains.get(member)),
    isMainSession: vi.fn((_session: unknown) => {
      void _session
      return false
    }),
    sessionAttention: vi.fn((_session: unknown): 'unknown' => {
      void _session
      return 'unknown'
    }),
    attachAckFields: vi.fn((_bound: unknown) => {
      void _bound
      return {}
    }),
    attentionBoundOfFrame: vi.fn((_frame: unknown): number | undefined => {
      void _frame
      return undefined
    }),
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: object) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: (bound: unknown) => seam.attachAckFields(bound),
  attentionBoundOfFrame: (frame: unknown) => seam.attentionBoundOfFrame(frame),
}))

function session(partial: Pick<Session, 'id' | 'agent_id' | 'title' | 'updated_at'> & Partial<Session>): Session {
  return {
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    message_count: 1,
    workspace_id: 'operations',
    ...partial,
  }
}

async function expectEntry(input: EntryInput, expected: EntryResult, specRef: string): Promise<void> {
  let actual: EntryResult
  try {
    const mod = await import(/* @vite-ignore */ SPEC) as {
      resolveWorkspaceEntry: (value: EntryInput) => EntryResult
    }
    actual = mod.resolveWorkspaceEntry(input)
  } catch (err) {
    expect.fail(
      `BLOCKED: ${SPEC} resolveWorkspaceEntry not implemented — required by ${specRef}. Expected ${JSON.stringify(expected)}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
    )
  }
  expect(actual).toEqual(expected)
  expect(seam.attachAckFields).not.toHaveBeenCalled()
}

beforeEach(() => {
  seam.mains.clear()
  seam.mainSessionIdOfMember.mockClear()
  seam.isMainSession.mockClear()
  seam.sessionAttention.mockClear()
  seam.attachAckFields.mockClear()
})

describe('resolveWorkspaceEntry (T-02, N03–N07)', () => {
  it.each(['workspace-name', 'cold-reload', 'login', 'modal-switch'] as const)(
    'N05 / BDD-02.1 %s returns the remembered chat, not the newer one',
    async (entryKind) => {
      const avaMember = {}
      seam.mains.set(avaMember, 'seam-opaque-ava')
      const remembered = entryKind === 'workspace-name' || entryKind === 'modal-switch'
        ? 'remembered-main'
        : 'remembered-extra'
      const expected: ExactEntry = {
        status: 'exact',
        sessionId: remembered,
        agentId: 'mia',
        sendEnabled: true,
        acknowledged: false,
      }
      await expectEntry({
        workspaceId: 'operations',
        entryKind,
        rememberedSessionId: remembered,
        pointerVerdict: 'visible',
        avaMember,
        committed: null,
        sessions: [
          session({ id: remembered, agent_id: 'mia', title: 'Remembered', updated_at: '2026-01-01T00:00:00Z' }),
          session({ id: 'newer-other', agent_id: 'jim', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
        ],
      }, expected, 'FR-004, BDD-02.1, dataset N05')
    },
  )

  it.each([null, undefined] as const)(
    'N04 remembered pointer %s opens the validated Ava main',
    async (rememberedSessionId) => {
      const avaMember = {}
      seam.mains.set(avaMember, 'seam-opaque-ava')
      const expected: WelcomeEntry = {
        status: 'welcome',
        sessionId: 'seam-opaque-ava',
        agentId: 'ava',
        sendEnabled: true,
        acknowledged: false,
      }
      await expectEntry({
        workspaceId: 'operations',
        entryKind: 'workspace-name',
        rememberedSessionId,
        pointerVerdict: 'absent',
        avaMember,
        committed: null,
        sessions: [
          session({ id: 'ava-extra-newer', agent_id: 'ava', title: 'Newer Ava extra', updated_at: '2026-10-08T00:00:00Z' }),
        ],
      }, expected, 'FR-004, BDD-02.2, dataset N04')
    },
  )

  it('N03 no pointer and no Ava main is unavailable, with Retry and send disabled', async () => {
    const avaMember = {}
    seam.mains.set(avaMember, undefined)
    const expected: UnavailableEntry = {
      status: 'unavailable',
      sessionId: null,
      sendEnabled: false,
      acknowledged: false,
      retry: true,
    }
    await expectEntry({
      workspaceId: 'operations',
      entryKind: 'login',
      rememberedSessionId: null,
      pointerVerdict: 'absent',
      avaMember,
      committed: null,
      sessions: [
        session({ id: 'someone-newer', agent_id: 'jim', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
      ],
    }, expected, 'FR-004, BDD-02.2, dataset N03')
  })

  it.each(['deleted', 'hidden', 'forbidden'] as const)(
    'N06 a %s pointer opens Ava and does not reuse the invalid target',
    async (pointerVerdict) => {
      const avaMember = {}
      seam.mains.set(avaMember, 'seam-opaque-ava')
      const expected: WelcomeEntry = {
        status: 'welcome',
        sessionId: 'seam-opaque-ava',
        agentId: 'ava',
        sendEnabled: true,
        acknowledged: false,
      }
      await expectEntry({
        workspaceId: 'operations',
        entryKind: 'cold-reload',
        rememberedSessionId: 'hidden-or-dead',
        pointerVerdict,
        avaMember,
        committed: null,
        sessions: [
          session({ id: 'hidden-or-dead', agent_id: 'mia', title: 'Do not open', updated_at: '2026-10-08T00:00:00Z' }),
        ],
      }, expected, 'FR-004, BDD-02.2, dataset N06')
    },
  )

  it('N06 an invalid pointer with no Ava main never opens the hidden target', async () => {
    const avaMember = {}
    seam.mains.set(avaMember, undefined)
    const expected: UnavailableEntry = {
      status: 'unavailable',
      sessionId: null,
      sendEnabled: false,
      acknowledged: false,
      retry: true,
    }
    await expectEntry({
      workspaceId: 'operations',
      entryKind: 'workspace-name',
      rememberedSessionId: 'hidden-target',
      pointerVerdict: 'hidden',
      avaMember,
      committed: null,
      sessions: [
        session({ id: 'hidden-target', agent_id: 'mia', title: 'Hidden', updated_at: '2026-10-08T00:00:00Z' }),
      ],
    }, expected, 'FR-004, BDD-02.2, dataset N06')
  })

  it.each(['timeout', 'offline', 'server-error'] as const)(
    'N07 / BDD-02.3 %s keeps committed A and does not send or fall back',
    async (pointerVerdict) => {
      const avaMember = {}
      seam.mains.set(avaMember, 'seam-opaque-ava')
      const expected: FailedAttempt = {
        status: 'failed-attempt',
        committed: { sessionId: 'session-a', agentId: 'mia' },
        attempted: { sessionId: 'session-b', sendEnabled: false },
        acknowledged: false,
        retry: true,
        fellBack: false,
      }
      await expectEntry({
        workspaceId: 'operations',
        entryKind: 'workspace-name',
        rememberedSessionId: 'session-b',
        pointerVerdict,
        avaMember,
        committed: { sessionId: 'session-a', agentId: 'mia' },
        sessions: [
          session({ id: 'session-a', agent_id: 'mia', title: 'A', updated_at: '2026-01-01T00:00:00Z' }),
          session({ id: 'newer', agent_id: 'jim', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
        ],
      }, expected, 'FR-004, BDD-02.3, dataset N07')
    },
  )

  it('N07 a timed-out pointer with nothing committed is retried, not replaced by Ava', async () => {
    const avaMember = {}
    seam.mains.set(avaMember, 'seam-opaque-ava')
    const expected: FailedAttempt = {
      status: 'failed-attempt',
      committed: null,
      attempted: { sessionId: 'remembered-main', sendEnabled: false },
      acknowledged: false,
      retry: true,
      fellBack: false,
    }
    await expectEntry({
      workspaceId: 'operations',
      entryKind: 'login',
      rememberedSessionId: 'remembered-main',
      pointerVerdict: 'timeout',
      avaMember,
      committed: null,
      sessions: [],
    }, expected, 'dataset N07 no deletion and no fallback')
  })

  it('a remembered __pending placeholder is not a saved pointer', async () => {
    const avaMember = {}
    seam.mains.set(avaMember, 'seam-opaque-ava')
    const expected: WelcomeEntry = {
      status: 'welcome',
      sessionId: 'seam-opaque-ava',
      agentId: 'ava',
      sendEnabled: true,
      acknowledged: false,
    }
    await expectEntry({
      workspaceId: 'operations',
      entryKind: 'cold-reload',
      rememberedSessionId: '__pending',
      pointerVerdict: 'absent',
      avaMember,
      committed: null,
      sessions: [],
    }, expected, 'BDD-E02 placeholder is not a saved pointer; BDD-02.2')
  })
})
