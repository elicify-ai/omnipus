// omnipus.test.ts — Omnipus identity rules (spec FR-004, FR-104, FR-107).
import { describe, it, expect } from 'vitest'
import type { Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { isConversationAgent, isTeamEligible, omnipusMainSessionId, teamKindOf } from './omnipus'

describe('omnipus identity rules', () => {
  it('Omnipus and system agents are never team-eligible; workers and colleagues are', () => {
    expect(isTeamEligible(makeAgent({ id: 'omnipus', type: 'core' }))).toBe(false)
    expect(isTeamEligible(makeAgent({ id: 'judge', type: 'system' }))).toBe(false)
    expect(isTeamEligible(makeAgent({ id: 'plansupervisor', type: 'system' }))).toBe(false)
    expect(teamKindOf(makeAgent({ id: 'mia', type: 'core' }))).toBe('conversation')
    expect(teamKindOf(makeAgent({ id: 'w', type: 'subagent_3p' }))).toBe('worker')
    expect(isConversationAgent(makeAgent({ id: 'r', type: 'Subagent' }))).toBe(false)
  })

  it('Omnipus main is the default workspace member main, never guessed', () => {
    const def = { id: 'd', is_default: true, member_configs: { omnipus: { main_session_id: 'm-omni' } } } as unknown as Workspace
    const other = { id: 's', member_configs: { omnipus: { main_session_id: 'wrong' } } } as unknown as Workspace
    expect(omnipusMainSessionId([other, def])).toBe('m-omni')
    expect(omnipusMainSessionId([other])).toBeUndefined()
    expect(omnipusMainSessionId([{ ...def, member_configs: {} } as unknown as Workspace])).toBeUndefined()
  })
})
