/**
 * Main-session attention survives the REST adapter into the roster (FR-047;
 * spec C-ATTENTION integration-boundary rows ~187-191, BDD-13.2).
 *
 * A generated-shape GET /sessions response — validated by the generated
 * SessionPage Zod schema inside the real request path — runs through the REAL
 * fetchSessionPage adapter (rawToSession) and then the REAL workspaceMains
 * projection, with the seam (sessionCoreSeam) NOT mocked.
 *
 * Oracles (docs/internal/specs/session-core-spec.md FR-047;
 * contracts/components/schemas/Session.yaml):
 *   needs_attention true    → signal 'on', onCount 1
 *   needs_attention false   → signal 'off', onCount 0 (known, not unknown)
 *   needs_attention omitted → signal 'unknown', unknown=true — never a zero
 *                             count presented as a known zero
 * and a type "main" session stays "main" through the adapter.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchSessionPage, type Agent, type Session, type Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { workspaceMains } from './workspaceRoster'

const MAIN_ID = 'main-ws1-mia'

/** Full generated Session wire shape (Session.yaml required fields), as the server would return a valid main. */
function wireMainSession(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: MAIN_ID,
    type: 'main',
    agent_id: 'mia',
    title: 'Mia main',
    status: 'active',
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-10T00:00:00Z',
    channel: 'webchat',
    partitions: [],
    workspace_id: 'ws-1',
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...overrides,
  }
}

function makeWorkspace(): Workspace {
  return {
    id: 'ws-1',
    name: 'Alpha',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    member_configs: { mia: { main_session_id: MAIN_ID } },
  }
}

const agents: Agent[] = [makeAgent({ id: 'mia', name: 'Mia', type: 'core' })]

/** Runs the wire body through the real request path (generated Zod) and the real adapter. */
async function loadSessions(wire: Record<string, unknown>[]): Promise<Session[]> {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ sessions: wire }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
  const page = await fetchSessionPage()
  return page.sessions
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('main attention survives the REST adapter into workspaceMains (FR-047, seam unmocked)', () => {
  it('needs_attention: true arrives as signal "on" with onCount 1, and type "main" stays "main"', async () => {
    const sessions = await loadSessions([wireMainSession({ needs_attention: true })])
    expect(sessions[0]?.type).toBe('main')
    expect(sessions[0]?.needs_attention).toBe(true)

    const mains = workspaceMains(makeWorkspace(), agents, sessions, 'fresh', [])
    expect(mains.signals[MAIN_ID]).toBe('on')
    expect(mains.onCount).toBe(1)
    expect(mains.unknown).toBe(false)
  })

  it('needs_attention: false arrives as signal "off" with onCount 0 — known, not unknown', async () => {
    const sessions = await loadSessions([wireMainSession({ needs_attention: false })])
    expect(sessions[0]?.needs_attention).toBe(false)

    const mains = workspaceMains(makeWorkspace(), agents, sessions, 'fresh', [])
    expect(mains.signals[MAIN_ID]).toBe('off')
    expect(mains.onCount).toBe(0)
    expect(mains.unknown).toBe(false)
  })

  it('needs_attention omitted arrives as signal "unknown" with unknown=true — never a zero count', async () => {
    const sessions = await loadSessions([wireMainSession()])
    // Absent on the wire stays absent as a value (undefined) — never coerced
    // to false; the seam reads that as 'unknown' below.
    expect(sessions[0]?.needs_attention).toBeUndefined()

    const mains = workspaceMains(makeWorkspace(), agents, sessions, 'fresh', [])
    expect(mains.signals[MAIN_ID]).toBe('unknown')
    expect(mains.unknown).toBe(true)
  })
})
