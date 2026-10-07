/**
 * Production seam default, unmocked. Oracles: the wave contract — until the
 * generated main / needs_attention fields exist, every read is unavailable
 * and nothing is acknowledged. A guess or a fake ack fails this test.
 */
import { describe, expect, it } from 'vitest'
import type { Session } from '@/lib/api'
import { makeAgent } from '@/test/factories'

const SEAM = '@/lib/nav/sessionCoreSeam'

type SeamModule = {
  mainSessionIdOfMember: (member: unknown) => string | undefined
  isMainSession: (session: unknown) => boolean
  sessionAttention: (session: unknown) => 'on' | 'off' | 'unknown'
  attachAckFields: (bound: unknown) => Record<string, unknown>
}

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

async function loadSeam(): Promise<SeamModule> {
  return await import(/* @vite-ignore */ SEAM) as SeamModule
}

describe('sessionCoreSeam production default', () => {
  it('is unavailable: no main id, not a main, unknown attention, and no ack fields', async () => {
    let seam: SeamModule
    try {
      seam = await loadSeam()
    } catch (err) {
      expect.fail(
        `BLOCKED: ${SEAM} not implemented — required by the wave seam contract. Expected mainSessionIdOfMember undefined, isMainSession false, sessionAttention "unknown", attachAckFields {}. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
      )
    }
    expect(seam.mainSessionIdOfMember({})).toBeUndefined()
    expect(seam.mainSessionIdOfMember(null)).toBeUndefined()
    expect(seam.isMainSession(session)).toBe(false)
    expect(seam.isMainSession(undefined)).toBe(false)
    expect(seam.sessionAttention(session)).toBe('unknown')
    expect(seam.sessionAttention(undefined)).toBe('unknown')
    expect(seam.attachAckFields({ sessionId: session.id, observedBound: 'goal1' })).toEqual({})
  })

  it('callers show unavailable / Retry, never a fabricated main and never an ack', async () => {
    try {
      await loadSeam()
    } catch (err) {
      expect.fail(
        `BLOCKED: ${SEAM} not implemented — required before the unavailable-UI proof. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
      )
    }
    const miaMember = {}
    const agent = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
    const eligibleSpec = '@/lib/nav/eligibleMains'
    const entrySpec = '@/lib/nav/workspaceEntry'
    const attentionSpec = '@/lib/nav/mainAttention'
    let listed: { status: string; retry: boolean; rows: unknown[]; missingMainAgentIds: string[] }
    try {
      const eligible = await import(/* @vite-ignore */ eligibleSpec) as {
        eligibleMainAgents: (input: unknown) => typeof listed
      }
      listed = eligible.eligibleMainAgents({
        workspaceId: 'product-launch',
        isDefaultWorkspace: false,
        rosterState: 'fresh',
        roster: [{ agent, member: miaMember }],
        adminDefault: null,
        sessions: [session],
        cachedRows: [],
      })
    } catch (err) {
      expect.fail(
        `BLOCKED: @/lib/nav/eligibleMains not implemented — required by BDD-01.4 never a fake main. Expected status unavailable, retry true, rows []. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
      )
    }
    expect(listed).toEqual({
      status: 'unavailable',
      retry: true,
      reason: 'main-id-missing',
      rows: [],
      missingMainAgentIds: ['mia'],
    })
    expect(JSON.stringify(listed)).not.toContain('main-session-')

    let entry: { status: string; sessionId: string | null; sendEnabled: boolean; acknowledged: boolean; retry?: boolean }
    try {
      const workspaceEntry = await import(/* @vite-ignore */ entrySpec) as {
        resolveWorkspaceEntry: (input: unknown) => typeof entry
      }
      entry = workspaceEntry.resolveWorkspaceEntry({
        workspaceId: 'product-launch',
        entryKind: 'login',
        rememberedSessionId: null,
        pointerVerdict: 'absent',
        sessions: [session],
        avaMember: miaMember,
        committed: null,
      })
    } catch (err) {
      expect.fail(
        `BLOCKED: @/lib/nav/workspaceEntry not implemented — required by BDD-02.2 unavailable / Retry / no send. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
      )
    }
    expect(entry).toEqual({
      status: 'unavailable',
      sessionId: null,
      sendEnabled: false,
      acknowledged: false,
      retry: true,
    })

    let ack: { acknowledge: boolean; fields?: unknown }
    try {
      const attention = await import(/* @vite-ignore */ attentionSpec) as {
        ackForShownCommit: (input: unknown) => typeof ack
      }
      ack = attention.ackForShownCommit({
        attemptKind: 'shown-commit',
        session,
        generation: 1,
        observedBound: 'goal1',
        newerOutcomeId: null,
        foreground: { sessionId: session.id, generation: 1, observedBound: 'goal1' },
        viewerId: 'user-a',
      })
    } catch (err) {
      expect.fail(
        `BLOCKED: @/lib/nav/mainAttention not implemented — required by BDD-04.4 no read acknowledgement. Actual: module missing (${err instanceof Error ? err.message : String(err)})`,
      )
    }
    expect(ack.acknowledge).toBe(false)
    expect(ack.fields).toBeUndefined()
  })
})
