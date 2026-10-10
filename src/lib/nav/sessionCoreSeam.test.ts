/**
 * Production seam, unmocked. Oracles: the generated wire fields exist and this
 * seam is the only reader — main_session_id, needs_attention, and the integer
 * attention bound are read and validated; a missing or unusable value stays
 * unavailable (undefined / false / 'unknown' / an empty object). A fabricated
 * main id, a fabricated off, or an acknowledgement without a server-bound
 * integer fails this test.
 */
import { describe, expect, it } from 'vitest'
import type { Session, WorkspaceMemberConfig } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import {
  attachAckFields,
  attentionBoundOfFrame,
  isMainSession,
  mainSessionIdOfMember,
  sessionAttention,
} from '@/lib/nav/sessionCoreSeam'

const session: Session = {
  id: 'not-a-real-main',
  agent_id: 'mia',
  title: 'Question about the launch',
  type: 'chat',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-10-08T00:00:00Z',
  message_count: 1,
  workspace_id: 'product-launch',
}

/** A server frame carrying an integer attention bound (generated wire shape). */
const boundFrame = {
  type: 'attach_session',
  session_id: 'not-a-real-main',
  ack_attention: false,
  attention_bound: 7,
}

describe('sessionCoreSeam reads the generated wire fields', () => {
  it('mainSessionIdOfMember returns the member main_session_id — the read that lights the sidebar', () => {
    const member: WorkspaceMemberConfig = { main_session_id: 'x' }
    expect(mainSessionIdOfMember(member)).toBe('x')
    const padded: WorkspaceMemberConfig = { main_session_id: '  spaced-id  ' }
    expect(mainSessionIdOfMember(padded)).toBe('  spaced-id  ')
  })

  it('mainSessionIdOfMember is unavailable for every non-id: empty, blank, non-string, missing, null', () => {
    expect(mainSessionIdOfMember({ main_session_id: '' })).toBeUndefined()
    expect(mainSessionIdOfMember({ main_session_id: '   ' })).toBeUndefined()
    expect(mainSessionIdOfMember({ main_session_id: 7 })).toBeUndefined()
    expect(mainSessionIdOfMember({})).toBeUndefined()
    expect(mainSessionIdOfMember(null)).toBeUndefined()
    expect(mainSessionIdOfMember(undefined)).toBeUndefined()
    expect(mainSessionIdOfMember('main-session-workspace-mia')).toBeUndefined()
  })

  it('isMainSession is true only for the generated type "main"', () => {
    expect(isMainSession({ type: 'main' })).toBe(true)
    expect(isMainSession({ type: 'chat' })).toBe(false)
    expect(isMainSession(session)).toBe(false)
    expect(isMainSession(undefined)).toBe(false)
  })

  it('sessionAttention maps needs_attention strictly: true→on, false→off, missing→unknown', () => {
    expect(sessionAttention({ needs_attention: true })).toBe('on')
    expect(sessionAttention({ needs_attention: false })).toBe('off')
    expect(sessionAttention({})).toBe('unknown')
    expect(sessionAttention(session)).toBe('unknown')
    expect(sessionAttention({ needs_attention: 'yes' })).toBe('unknown')
    expect(sessionAttention(undefined)).toBe('unknown')
  })

  it('attentionBoundOfFrame accepts only an integer bound; a string is never a bound', () => {
    expect(attentionBoundOfFrame(boundFrame)).toBe(7)
    expect(attentionBoundOfFrame({ attention_bound: 'goal1' })).toBeUndefined()
    expect(attentionBoundOfFrame({ attention_bound: 1.5 })).toBeUndefined()
    expect(attentionBoundOfFrame({})).toBeUndefined()
    expect(attentionBoundOfFrame(undefined)).toBeUndefined()
  })

  it('attachAckFields acknowledges only through a server integer bound', () => {
    expect(attachAckFields(7)).toEqual({ ack_attention: true, attention_bound: 7 })
    expect(attachAckFields('goal1')).toEqual({})
    expect(attachAckFields(1.5)).toEqual({})
    expect(attachAckFields(undefined)).toEqual({})
  })
})

describe('sessionCoreSeam end to end at eligibleMainAgents (unmocked)', () => {
  it('a roster member with a real main_session_id produces ready rows from the seam id', async () => {
    const { eligibleMainAgents } = await import('@/lib/nav/eligibleMains')
    const member: WorkspaceMemberConfig = { main_session_id: 'x' }
    const listed = eligibleMainAgents({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      roster: [{ agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member }],
      adminDefault: null,
      sessions: [session],
      cachedRows: [],
    })
    expect(listed).toEqual({
      status: 'ready',
      retry: false,
      reason: null,
      rows: [{ agentId: 'mia', workspaceId: 'product-launch', name: 'Mia', mainSessionId: 'x' }],
      missingMainAgentIds: [],
    })
    expect(JSON.stringify(listed)).not.toContain('main-session-')
  })

  it('a roster member without a main_session_id is still unavailable — never a fabricated main', async () => {
    const { eligibleMainAgents } = await import('@/lib/nav/eligibleMains')
    const miaMember: WorkspaceMemberConfig = {}
    const listed = eligibleMainAgents({
      workspaceId: 'product-launch',
      isDefaultWorkspace: false,
      rosterState: 'fresh',
      roster: [{ agent: makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), member: miaMember }],
      adminDefault: null,
      sessions: [session],
      cachedRows: [],
    })
    expect(listed).toEqual({
      status: 'unavailable',
      retry: true,
      reason: 'main-id-missing',
      rows: [],
      missingMainAgentIds: ['mia'],
    })
    expect(JSON.stringify(listed)).not.toContain('main-session-')
  })

  it('entry without a member main id stays unavailable / Retry / no send', async () => {
    const { resolveWorkspaceEntry } = await import('@/lib/nav/workspaceEntry')
    const avaMember: WorkspaceMemberConfig = {}
    const entry = resolveWorkspaceEntry({
      workspaceId: 'product-launch',
      entryKind: 'login',
      rememberedSessionId: null,
      pointerVerdict: 'absent',
      sessions: [session],
      avaMember,
      committed: null,
    })
    expect(entry).toEqual({
      status: 'unavailable',
      sessionId: null,
      sendEnabled: false,
      acknowledged: false,
      retry: true,
    })
  })

  it('a non-main chat session is never acknowledged', async () => {
    const { ackForShownCommit } = await import('@/lib/nav/mainAttention')
    const ack = ackForShownCommit({
      attemptKind: 'shown-commit',
      session,
      generation: 1,
      frame: boundFrame,
      newerOutcomeId: null,
      foreground: {
        sessionId: session.id,
        generation: 1,
        frame: boundFrame,
      },
      viewerId: 'user-a',
    })
    expect(ack.acknowledge).toBe(false)
    expect(ack.fields).toBeUndefined()
  })
})
