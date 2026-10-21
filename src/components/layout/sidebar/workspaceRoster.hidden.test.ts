/**
 * D5 "Hide from sidebar": the roster splits hidden rows out of the sidebar
 * list, while attention keeps counting over EVERY row.
 *
 * Oracles: DECISIONS.md D5 ("the agent still works ... the workspace group
 * shows a quiet 'N hidden · Show' line") and ADR-hide-from-sidebar.md
 * §"SPA read path" ("compute signals/onCount/unknown over ALL rows, hidden
 * included ... Hiding removes a row, it does not mute the 'a main chat needs
 * you' signal"). (The retired Admin row's never-hidden pin was deleted with
 * the row — FR-008, WU-11 GREEN.)
 *
 * Same harness as workspaceRoster.attention.test.ts: a generated-shape
 * GET /sessions body through the real request path (generated Zod) and the
 * real adapter, with the seam (sessionCoreSeam) NOT mocked.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchSessionPage, type Agent, type Session, type Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { workspaceMains } from './workspaceRoster'

const MIA_MAIN = 'main-ws1-mia'
const JIM_MAIN = 'main-ws1-jim'

/** Full generated Session wire shape (Session.yaml required fields). */
function wireMainSession(id: string, agentId: string, overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id,
    type: 'main',
    agent_id: agentId,
    title: `${agentId} main`,
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

function makeWorkspace(memberConfigs: Workspace['member_configs']): Workspace {
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
    member_configs: memberConfigs,
  }
}

const agents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core' }),
  makeAgent({ id: 'admin', name: 'Admin', type: 'core' }),
]

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

describe('workspaceMains splits hidden rows but keeps their attention (D5, ADR §SPA read path)', () => {
  it('a hidden member lands in hidden, not visible, while result.rows still carries both', async () => {
    const sessions = await loadSessions([
      wireMainSession(MIA_MAIN, 'mia', { needs_attention: false }),
      wireMainSession(JIM_MAIN, 'jim', { needs_attention: false }),
    ])
    const workspace = makeWorkspace({
      mia: { main_session_id: MIA_MAIN },
      jim: { main_session_id: JIM_MAIN, hidden_from_sidebar: true },
    })

    const mains = workspaceMains(workspace, agents, sessions, 'fresh', [])

    expect(mains.visible.map((row) => row.agentId), 'only Mia renders in the list').toEqual(['mia'])
    expect(mains.hidden.map((row) => row.agentId), 'Jim is the hidden row').toEqual(['jim'])
    expect(mains.result.rows.map((row) => row.agentId), 'the full projection still carries both').toEqual(['mia', 'jim'])
  })

  it("a hidden member's attention still counts — onCount 1, signal 'on', unknown false", async () => {
    const sessions = await loadSessions([
      wireMainSession(MIA_MAIN, 'mia', { needs_attention: false }),
      wireMainSession(JIM_MAIN, 'jim', { needs_attention: true }),
    ])
    const workspace = makeWorkspace({
      mia: { main_session_id: MIA_MAIN },
      jim: { main_session_id: JIM_MAIN, hidden_from_sidebar: true },
    })

    const mains = workspaceMains(workspace, agents, sessions, 'fresh', [])

    expect(mains.signals[JIM_MAIN], 'hidden does not mute attention').toBe('on')
    expect(mains.onCount, 'the collapsed workspace row still shows 1 needing attention').toBe(1)
    expect(mains.unknown).toBe(false)
  })

  it('with no hidden flags, visible is the whole projection and hidden is empty', async () => {
    const sessions = await loadSessions([
      wireMainSession(MIA_MAIN, 'mia', { needs_attention: false }),
      wireMainSession(JIM_MAIN, 'jim', { needs_attention: false }),
    ])
    const workspace = makeWorkspace({
      mia: { main_session_id: MIA_MAIN },
      jim: { main_session_id: JIM_MAIN },
    })

    const mains = workspaceMains(workspace, agents, sessions, 'fresh', [])

    expect(mains.hidden).toEqual([])
    expect(mains.visible).toEqual(mains.result.rows)
  })

  // The "Admin's default-workspace row is never hidden" test was deleted with
  // the Admin row itself (FR-008): no adminDefault input exists any more, so
  // no Admin row can be part of the visible/hidden split; the split rule is
  // pinned by the hidden-member cases above.
})
