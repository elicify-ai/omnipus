/**
 * T-02 / WU-10 workspace entry. Supersession: omnipus-system-agent-spec
 * FR-107, BDD-12.11, dataset D4; ADR OSA-10 U8 replaces Ava-first.
 * The original exact-pointer, hidden-pointer, transport, placeholder and no-ack
 * oracles remain. Only the fallback is changed to the first conversation member.
 * Main ids use the generated member field and real sessionCoreSeam, not mocks.
 */
import { describe, expect, it } from 'vitest'
import type { Agent, WorkspaceMemberConfig } from '@/lib/api/generated/openapi-types'
import type { Session } from '@/lib/api/sessions'
import { MIA, JIM, OMNIPUS, WORKER, EXTERNAL_WORKER, JUDGE, PLAN_SUPERVISOR } from '../../../tests/omnipus-l1-ui-red/fixtures'

// not-wire-format: test-only future resolver input, matching the implementer's
// documented WU-10 handoff; never serialized to the gateway.
type EntryInput = {
  workspaceId: string
  entryKind: 'workspace-name' | 'cold-reload' | 'login' | 'modal-switch'
  rememberedSessionId: string | null | undefined
  pointerVerdict: 'visible' | 'deleted' | 'partial-list' | 'hidden' | 'forbidden' | 'timeout' | 'offline' | 'server-error' | 'absent'
  sessions: Session[]
  team: { coreTeam: string[]; memberConfigs: Record<string, WorkspaceMemberConfig>; agents: Agent[] }
  committed: { sessionId: string; agentId: string } | null
}
// not-wire-format: client decision oracle, not a wire response.
type EntryResult = {
  status: string
  acknowledged: false
  [key: string]: unknown
}
function chat(partial: Pick<Session, 'id' | 'agent_id' | 'title' | 'updated_at'> & Partial<Session>): Session {
  return { type: 'chat', created_at: '2026-01-01T00:00:00Z', message_count: 1, workspace_id: 'operations', ...partial }
}
function team(mainId: string | null = 'opaque-mia-main'): EntryInput['team'] {
  return { coreTeam: ['mia'], memberConfigs: { mia: mainId === null ? {} : { main_session_id: mainId } }, agents: [MIA] }
}
function input(overrides: Partial<EntryInput> = {}): EntryInput {
  return { workspaceId: 'operations', entryKind: 'workspace-name', rememberedSessionId: null,
    pointerVerdict: 'absent', sessions: [], team: team(), committed: null, ...overrides }
}
const welcome = { status: 'welcome', sessionId: 'opaque-mia-main', agentId: 'mia', sendEnabled: true, acknowledged: false as const }
const unavailable = { status: 'unavailable', sessionId: null, sendEnabled: false, acknowledged: false as const, retry: true }
async function expectEntry(value: EntryInput, expected: EntryResult): Promise<void> {
  // The old base still takes avaMember. A dynamic typed namespace lets the RED
  // test execute the real old resolver without imposing a compilation failure.
  // Path unchanged; future input shape documented by team-lead before this edit.
  const path = '@/lib/nav/workspaceEntry'
  const module = await import(/* @vite-ignore */ path) as { resolveWorkspaceEntry: (value: EntryInput) => EntryResult }
  const actual = module.resolveWorkspaceEntry(value)
  expect(actual).toEqual(expected)
  expect(actual.acknowledged).toBe(false) // preserved no-ack oracle, now real output
}

describe('resolveWorkspaceEntry (T-02, N03–N07; amended by FR-107)', () => {
  // Original N05 / BDD-02.1 exact-pointer oracle retained; FR-107 also requires it.
  it.each(['workspace-name', 'cold-reload', 'login', 'modal-switch'] as const)(
    'N05 / BDD-12.11 %s returns the remembered chat, not the newer one', async (entryKind) => {
      const remembered = entryKind === 'workspace-name' || entryKind === 'modal-switch' ? 'remembered-main' : 'remembered-extra'
      await expectEntry(input({ entryKind, rememberedSessionId: remembered, pointerVerdict: 'visible', sessions: [
        chat({ id: remembered, agent_id: 'mia', title: 'Remembered', updated_at: '2026-01-01T00:00:00Z' }),
        chat({ id: 'newer-other', agent_id: 'jim', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
      ] }), { status: 'exact', sessionId: remembered, agentId: 'mia', sendEnabled: true, acknowledged: false })
    },
  )
  // Superseded N04 Ava main -> first colleague main, same exact shape/no-ack.
  it.each([null, undefined] as const)(
    'N04 / BDD-12.11 pointer %s opens the first colleague main', async (rememberedSessionId) => {
      await expectEntry(input({ rememberedSessionId, sessions: [
        chat({ id: 'mia-extra-newer', agent_id: 'mia', title: 'Newer extra', updated_at: '2026-10-08T00:00:00Z' }),
      ] }), welcome)
    },
  )
  // N03 remains an unavailable main, NOT a genuinely empty team.
  it('N03 / FR-107 a first colleague with no resolved main is unavailable with Retry and send disabled', async () => {
    await expectEntry(input({ entryKind: 'login', team: team(null), sessions: [
      chat({ id: 'someone-newer', agent_id: 'jim', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
    ] }), unavailable)
  })
  it.each(['deleted', 'hidden', 'forbidden'] as const)(
    'N06 / BDD-12.11 a %s pointer uses the first colleague and never reuses the invalid target', async (pointerVerdict) => {
      await expectEntry(input({ entryKind: 'cold-reload', rememberedSessionId: 'hidden-or-dead', pointerVerdict, sessions: [
        chat({ id: 'hidden-or-dead', agent_id: 'mia', title: 'Do not open', updated_at: '2026-10-08T00:00:00Z' }),
      ] }), welcome)
    },
  )
  it('N06 / FR-107 an invalid pointer with no first main never opens the hidden target', async () => {
    await expectEntry(input({ rememberedSessionId: 'hidden-target', pointerVerdict: 'hidden', team: team(null), sessions: [
      chat({ id: 'hidden-target', agent_id: 'mia', title: 'Hidden', updated_at: '2026-10-08T00:00:00Z' }),
    ] }), unavailable)
  })
  it.each(['timeout', 'offline', 'server-error', 'partial-list'] as const)(
    'N07 / section 6.3 %s keeps the committed chat and never sends or falls back', async (pointerVerdict) => {
      await expectEntry(input({ rememberedSessionId: 'session-b', pointerVerdict,
        committed: { sessionId: 'session-a', agentId: 'mia' }, sessions: [
          chat({ id: 'session-a', agent_id: 'mia', title: 'A', updated_at: '2026-01-01T00:00:00Z' }),
          chat({ id: 'newer', agent_id: 'jim', title: 'Newer', updated_at: '2026-10-08T00:00:00Z' }),
        ] }), { status: 'failed-attempt', committed: { sessionId: 'session-a', agentId: 'mia' },
        attempted: { sessionId: 'session-b', sendEnabled: false }, acknowledged: false, retry: true, fellBack: false })
    },
  )
  it('N07 / section 6.3 a timed-out pointer with nothing committed is retried, never replaced', async () => {
    await expectEntry(input({ entryKind: 'login', rememberedSessionId: 'remembered-main', pointerVerdict: 'timeout' }), {
      status: 'failed-attempt', committed: null, attempted: { sessionId: 'remembered-main', sendEnabled: false },
      acknowledged: false, retry: true, fellBack: false,
    })
  })
  it('BDD-12.11 a remembered __pending placeholder is not a saved pointer', async () => {
    await expectEntry(input({ entryKind: 'cold-reload', rememberedSessionId: '__pending' }), welcome)
  })
})

describe('WU-10 ordered team entry, dataset D4', () => {
  it('BDD-12.11 Default [Omnipus hidden, Mia] opens Mia, not Omnipus', async () => {
    await expectEntry(input({ team: { coreTeam: ['omnipus', 'mia'], agents: [OMNIPUS, MIA], memberConfigs: {
      omnipus: { main_session_id: 'opaque-omnipus' }, mia: { main_session_id: 'opaque-mia-main' },
    } } }), welcome)
  })
  it('FR-107 uses core_team order, not alphabetical names or newest sessions', async () => {
    await expectEntry(input({ team: { coreTeam: ['jim', 'mia'], agents: [MIA, JIM], memberConfigs: {
      jim: { main_session_id: 'opaque-jim-main' }, mia: { main_session_id: 'opaque-mia-main' },
    } } }), { ...welcome, sessionId: 'opaque-jim-main', agentId: 'jim' })
  })
  it('FR-107 skips native/external workers and hidden engines before the first conversation member', async () => {
    await expectEntry(input({ team: { coreTeam: ['researcher', 'external', 'judge', 'plansupervisor', 'mia'],
      agents: [WORKER, EXTERNAL_WORKER, JUDGE, PLAN_SUPERVISOR, MIA], memberConfigs: { mia: { main_session_id: 'opaque-mia-main' } },
    } }), welcome)
  })
  it('FR-107 never silently falls back to a second colleague if the first main is unresolved', async () => {
    await expectEntry(input({ team: { coreTeam: ['mia', 'jim'], agents: [MIA, JIM], memberConfigs: {
      mia: {}, jim: { main_session_id: 'opaque-jim-main' },
    } } }), unavailable)
  })
  it.each([{ coreTeam: [] }, { coreTeam: ['omnipus'] }, { coreTeam: ['researcher', 'external'] }])(
    'BDD-12.11 no conversation member in %j gives an empty team with setup, not an attach error', async ({ coreTeam }) => {
      await expectEntry(input({ team: { coreTeam, agents: [OMNIPUS, WORKER, EXTERNAL_WORKER], memberConfigs: {
        omnipus: { main_session_id: 'opaque-omnipus' },
      } } }), { status: 'empty-team', sessionId: null, sendEnabled: false, setupOffer: true, acknowledged: false })
    },
  )
})
