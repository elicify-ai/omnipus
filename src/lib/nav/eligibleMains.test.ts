/**
 * T-01 eligible mains. Oracles: FR-001, FR-003, BDD-01.1, BDD-01.2, BDD-01.4,
 * BDD-E01, BDD-12.2, datasets N01, N02, N10.
 *
 * Expected main ids are opaque seam fixtures. They are intentionally NOT
 * `main-session-<workspace>-<agent>` (DEP-U1: the client must not create that
 * id). A guesser that concatenates the workspace and agent ids cannot match.
 *
 * Rows keep authoritative roster order (FR-003: not recency, not name).
 * Admin in the default workspace is appended after roster rows when, and only
 * when, the seam returns a validated id (BDD-12.2: no membership required).
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, WorkspaceMemberConfig } from '@/lib/api'
import { makeAgent } from '@/test/factories'

const SPEC = '@/lib/nav/eligibleMains'

type EligibleRow = {
  agentId: string
  workspaceId: string
  name: string
  mainSessionId: string
}

type EligibleResult = {
  status: 'ready' | 'unavailable' | 'stale'
  retry: boolean
  reason: 'main-id-missing' | 'roster-failed' | null
  rows: EligibleRow[]
  missingMainAgentIds: string[]
}

type EligibleInput = {
  workspaceId: string
  isDefaultWorkspace: boolean
  rosterState: 'fresh' | 'failed-no-cache' | 'failed-stale-cache'
  roster: Array<{ agent: ReturnType<typeof makeAgent>; member: WorkspaceMemberConfig }>
  // WU-11 GREEN (FR-008): EligibleInput no longer carries adminDefault — the
  // retired Admin entry is deleted with the feature, and no input can mint an
  // Admin row.
  sessions: Session[]
  cachedRows: EligibleRow[]
}

const seam = vi.hoisted(() => {
  const mains = new Map<object, string | undefined>()
  const mainSessionIds = new Set<string>()
  return {
    mains,
    mainSessionIds,
    mainSessionIdOfMember: vi.fn((member: object) => mains.get(member)),
    isMainSession: vi.fn((session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return mainSessionIds.has(id)
    }),
    // Retain seam argument signatures for the forwarding mocks below; these
    // unavailable defaults deliberately ignore the supplied values.
    sessionAttention: vi.fn((_session: unknown): 'on' | 'off' | 'unknown' => { void _session; return 'unknown' }),
    attachAckFields: vi.fn((_bound: unknown) => { void _bound; return {} }),
    attentionBoundOfFrame: vi.fn((_frame: unknown): number | undefined => { void _frame; return undefined }),
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  // Real seam for everything the stubs don't override. (WU-11 GREEN removed the
  // Admin-main seam reader with the retired Admin row — FR-008.)
  ...await importOriginal<typeof import('@/lib/nav/sessionCoreSeam')>(),
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
    ...partial,
  }
}

function member(): WorkspaceMemberConfig {
  return {}
}

async function expectEligible(input: EligibleInput, expected: EligibleResult, specRef: string): Promise<void> {
  let actual: EligibleResult
  try {
    const mod = await import(/* @vite-ignore */ SPEC) as {
      eligibleMainAgents: (value: EligibleInput) => EligibleResult
    }
    actual = mod.eligibleMainAgents(input)
  } catch (err) {
    expect.fail(
      `BLOCKED: ${SPEC} eligibleMainAgents not implemented — required by ${specRef}. Expected ${JSON.stringify(expected)}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
    )
  }
  expect(actual).toEqual(expected)
}

beforeEach(() => {
  seam.mains.clear()
  seam.mainSessionIds.clear()
  seam.mainSessionIdOfMember.mockClear()
  seam.isMainSession.mockClear()
  seam.sessionAttention.mockClear()
  seam.attachAckFields.mockClear()
})

describe('eligibleMainAgents (T-01, N01, N02, N10)', () => {
  it('N01 / BDD-01.1 opens the seam main, not the newer extra', async () => {
    const miaMember = member()
    seam.mains.set(miaMember, 'seam-opaque-m')
    seam.mainSessionIds.add('seam-opaque-m')
    const expected: EligibleResult = {
      status: 'ready',
      retry: false,
      reason: null,
      missingMainAgentIds: [],
      rows: [{ agentId: 'mia', workspaceId: 'product-launch', name: 'Mia', mainSessionId: 'seam-opaque-m' }],
    }
    await expectEligible({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      roster: [{ agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember }],
      cachedRows: [],
      sessions: [
        session({
          id: 'extra-newer',
          agent_id: 'mia',
          workspace_id: 'product-launch',
          title: 'Newer extra',
          updated_at: '2026-10-08T00:00:00Z',
        }),
      ],
    }, expected, 'FR-003, BDD-01.1, dataset N01')
  })

  it('BDD-01.2 / N10 default workspace keeps Mia (validated members only)', async () => {
    const miaMember = member()
    const nativeMember = member()
    const externalMember = member()
    const judgeMember = member()
    const supervisorMember = member()
    seam.mains.set(miaMember, 'seam-opaque-mia')
    seam.mains.set(nativeMember, 'seam-opaque-native-must-not-show')
    seam.mains.set(externalMember, 'seam-opaque-external-must-not-show')
    seam.mains.set(judgeMember, 'seam-opaque-judge-must-not-show')
    seam.mains.set(supervisorMember, 'seam-opaque-supervisor-must-not-show')
    const expected: EligibleResult = {
      status: 'ready',
      retry: false,
      reason: null,
      missingMainAgentIds: [],
      // WU-11 GREEN retarget (FR-008): the validated-Admin row is retired —
      // a cached Admin agent never becomes a row on any workspace.
      rows: [
        { agentId: 'mia', workspaceId: 'default', name: 'Mia', mainSessionId: 'seam-opaque-mia' },
      ],
    }
    await expectEligible({
      workspaceId: 'default',
      isDefaultWorkspace: true,
      rosterState: 'fresh',
      cachedRows: [],
      sessions: [],
      roster: [
        { agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember },
        { agent: makeAgent({ id: 'native-worker', name: 'Native worker', type: 'Subagent' }), member: nativeMember },
        { agent: makeAgent({ id: 'external-worker', name: 'External worker', type: 'subagent_3p' }), member: externalMember },
        { agent: makeAgent({ id: 'judge', name: 'Judge', type: 'system' }), member: judgeMember },
        { agent: makeAgent({ id: 'plan-supervisor', name: 'Plan Supervisor', type: 'system' }), member: supervisorMember },
      ],
    }, expected, 'FR-001, BDD-01.2, BDD-12.2, dataset N10')
  })

  // The "other workspace hides Admin even when the roster lists Admin" test
  // was deleted with the feature (FR-008): Admin workspace-scoping was an
  // identity branch of the retired Admin entry. Retirement is now enforced
  // once, at eligibility (lib/omnipus.ts::isTeamEligible ->
  // lib/agentKind.ts::isChatEligibleAgent — a retired built-in a stale cache
  // carries is never OFFERED); the roster renders membership, and no input of
  // eligibleMainAgents can mint an Admin row any more.
  it('N02 / BDD-E01 returns the roster identity, not the same-name agent in another workspace', async () => {
    const opsMember = member()
    seam.mains.set(opsMember, 'seam-opaque-mia-ops')
    const expected: EligibleResult = {
      status: 'ready',
      retry: false,
      reason: null,
      missingMainAgentIds: [],
      rows: [{ agentId: 'mia-ops', workspaceId: 'operations', name: 'Mia', mainSessionId: 'seam-opaque-mia-ops' }],
    }
    await expectEligible({
      workspaceId: 'operations',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      cachedRows: [],
      roster: [{ agent: makeAgent({ id: 'mia-ops', name: 'Mia', type: 'Main' }), member: opsMember }],
      sessions: [
        session({
          id: 'seam-opaque-mia-product',
          agent_id: 'mia-product',
          workspace_id: 'product-launch',
          title: 'Other Mia',
          updated_at: '2026-10-08T00:00:00Z',
        }),
      ],
    }, expected, 'FR-003, BDD-E01, dataset N02')
  })

  it('keeps roster order when a later name has the newer session', async () => {
    const zoeMember = member()
    const miaMember = member()
    seam.mains.set(zoeMember, 'seam-opaque-zoe')
    seam.mains.set(miaMember, 'seam-opaque-mia')
    const expected: EligibleResult = {
      status: 'ready',
      retry: false,
      reason: null,
      missingMainAgentIds: [],
      rows: [
        { agentId: 'zoe', workspaceId: 'operations', name: 'Zoe', mainSessionId: 'seam-opaque-zoe' },
        { agentId: 'mia', workspaceId: 'operations', name: 'Mia', mainSessionId: 'seam-opaque-mia' },
      ],
    }
    await expectEligible({
      workspaceId: 'operations',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      cachedRows: [],
      roster: [
        { agent: makeAgent({ id: 'zoe', name: 'Zoe', type: 'Main' }), member: zoeMember },
        { agent: makeAgent({ id: 'mia', name: 'Mia', type: 'Main' }), member: miaMember },
      ],
      sessions: [
        session({ id: 'newer', agent_id: 'mia', workspace_id: 'operations', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
        session({ id: 'older', agent_id: 'zoe', workspace_id: 'operations', title: 'Older', updated_at: '2026-01-01T00:00:00Z' }),
      ],
    }, expected, 'FR-003 not-recency; roster order is the stable order')
  })

  it('BDD-01.2 a fresh roster of only workers and hidden engines is an honest empty list', async () => {
    const workerMember = member()
    seam.mains.set(workerMember, 'seam-opaque-worker-must-not-show')
    const expected: EligibleResult = {
      status: 'ready',
      retry: false,
      reason: null,
      rows: [],
      missingMainAgentIds: [],
    }
    await expectEligible({
      workspaceId: 'operations',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      cachedRows: [],
      sessions: [],
      roster: [{ agent: makeAgent({ id: 'native-worker', name: 'Native worker', type: 'Subagent' }), member: workerMember }],
    }, expected, 'BDD-01.2 workers absent; a successful roster is not BDD-01.4 empty-team failure')
  })
})

describe('eligibleMainAgents failure and missing-ID states (T-01, N01, N10)', () => {
  it('BDD-01.4 no cache and a failed roster is Retry, never a seam id or a session id', async () => {
    const miaMember = member()
    seam.mains.set(miaMember, 'seam-opaque-mia')
    const expected: EligibleResult = {
      status: 'unavailable',
      retry: true,
      reason: 'roster-failed',
      rows: [],
      missingMainAgentIds: [],
    }
    await expectEligible({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'failed-no-cache',
      cachedRows: [],
      roster: [{ agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember }],
      sessions: [
        session({ id: 'extra-newer', agent_id: 'mia', workspace_id: 'product-launch', title: 'Extra', updated_at: '2026-10-08T00:00:00Z' }),
      ],
    }, expected, 'FR-011, BDD-01.4, dataset N01 negative')
  })

  it('BDD-01.4 a failed refresh with a prior cache is marked stale and does not adopt the newer extra', async () => {
    const miaMember = member()
    seam.mains.set(miaMember, 'seam-opaque-mia-live-must-not-replace-cache')
    const cached = [{ agentId: 'mia', workspaceId: 'product-launch', name: 'Mia', mainSessionId: 'seam-opaque-cached' }]
    const expected: EligibleResult = {
      status: 'stale',
      retry: true,
      reason: null,
      rows: cached,
      missingMainAgentIds: [],
    }
    await expectEligible({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'failed-stale-cache',
      cachedRows: cached,
      roster: [
        { agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember },
        { agent: makeAgent({ id: 'native-worker', name: 'Native worker', type: 'Subagent' }), member: member() },
      ],
      sessions: [
        session({ id: 'extra-newer', agent_id: 'mia', workspace_id: 'product-launch', title: 'Extra', updated_at: '2026-10-08T00:00:00Z' }),
      ],
    }, expected, 'BDD-01.4 prior cache is marked stale; never a fake main')
  })

  it('drops a worker that was cached, instead of showing a stale ineligible row', async () => {
    const expected: EligibleResult = {
      status: 'stale',
      retry: true,
      reason: null,
      missingMainAgentIds: [],
      rows: [{ agentId: 'mia', workspaceId: 'product-launch', name: 'Mia', mainSessionId: 'seam-opaque-cached' }],
    }
    await expectEligible({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'failed-stale-cache',
      sessions: [],
      roster: [
        { agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: member() },
        { agent: makeAgent({ id: 'native-worker', name: 'Native worker', type: 'Subagent' }), member: member() },
      ],
      cachedRows: [
        { agentId: 'native-worker', workspaceId: 'product-launch', name: 'Native worker', mainSessionId: 'seam-opaque-worker' },
        { agentId: 'mia', workspaceId: 'product-launch', name: 'Mia', mainSessionId: 'seam-opaque-cached' },
      ],
    }, expected, 'FR-001 a stale cache still excludes workers')
  })

  it('a missing seam id is unavailable and is not replaced with a fabricated main id', async () => {
    const miaMember = member()
    seam.mains.set(miaMember, undefined)
    const expected: EligibleResult = {
      status: 'unavailable',
      retry: true,
      reason: 'main-id-missing',
      rows: [],
      missingMainAgentIds: ['mia'],
    }
    await expectEligible({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      cachedRows: [],
      roster: [{ agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember }],
      sessions: [
        session({ id: 'extra-newer', agent_id: 'mia', workspace_id: 'product-launch', title: 'Extra', updated_at: '2026-10-08T00:00:00Z' }),
      ],
    }, expected, 'BDD-01.2 no main-ID guess; BDD-01.4 never a fake main')
  })

  it('lists the known main and names the eligible member whose seam id is missing', async () => {
    const jimMember = member()
    const miaMember = member()
    seam.mains.set(jimMember, 'seam-opaque-jim')
    seam.mains.set(miaMember, undefined)
    const expected: EligibleResult = {
      status: 'unavailable',
      retry: true,
      reason: 'main-id-missing',
      missingMainAgentIds: ['mia'],
      rows: [{ agentId: 'jim', workspaceId: 'operations', name: 'Jim', mainSessionId: 'seam-opaque-jim' }],
    }
    await expectEligible({
      workspaceId: 'operations',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      cachedRows: [],
      sessions: [],
      roster: [
        { agent: makeAgent({ id: 'jim', name: 'Jim', type: 'core' }), member: jimMember },
        { agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember },
      ],
    }, expected, 'BDD-01.2 no main-ID guess; known mains stay, the gap is named')
  })

  // The "default-workspace Admin without a seam id is a named gap" test was
  // deleted with the mechanism (FR-008): EligibleInput no longer carries an
  // adminDefault entry, so no input can produce a missing-Admin gap; the
  // never-fabricate-a-main rule stays pinned by the main-id-missing cases.
})
