// The sidebar roster renders core_team membership (founder bug 2026-10-11).
//
// Roster-level oracles live in workspaceRoster.coreTeam.test.ts; this file
// pins what the sidebar actually SHOWS for the same three states:
//   1. a core_team Main with no member_configs entry → the visible
//      "Main chat unavailable" note WITH a Retry control (never silence);
//   2. once its member_configs entry with a main id arrives → the agent's
//      row renders and the note is gone;
//   3. helper/worker core_team members (Subagent / subagent_3p, no main by
//      design) produce no note and no row.
import type { ReactNode } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { WorkspaceAgentList } from './WorkspaceAgentList'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: ReactNode }) => children,
}))

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  })
}

const WS = 'product-launch'

function makeProject(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: WS,
    name: 'Product launch',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    member_configs: {},
    is_default: false,
    ...overrides,
  }
}

const MAIN_ID = 'main-omnipus'

const omnipus = makeAgent({ id: 'omnipus', name: 'Omnipus', type: 'Main' })

function mainSession(agentId: string, needsAttention = false): Session {
  return {
    id: MAIN_ID,
    type: 'main',
    agent_id: agentId,
    title: 'main',
    status: 'active',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    channel: 'webchat',
    workspace_id: WS,
    needs_attention: needsAttention,
    message_count: 0,
  }
}

function renderList(project: Workspace, agents: Agent[], sessions: Session[]) {
  render(
    <WorkspaceAgentList
      projects={[project]}
      expandedIds={new Set([WS])}
      activeId={null}
      agents={agents}
      rosterState="fresh"
      sessions={sessions}
      onToggle={() => {}}
      onOpen={() => {}}
      onOverlayClose={() => {}}
    />,
  )
}

describe('sidebar roster shows core_team members (founder bug 2026-10-11)', () => {
  it('a core_team Main without a member_configs entry surfaces "Main chat unavailable" with Retry', () => {
    renderList(makeProject({ core_team: ['omnipus'], member_configs: {} }), [omnipus], [])

    expect(screen.getByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Retry' }).length).toBeGreaterThanOrEqual(1)
  })

  it('once the member_configs entry with a main id arrives the row renders and the note is gone', () => {
    renderList(
      makeProject({
        core_team: ['omnipus'],
        member_configs: { omnipus: { main_session_id: MAIN_ID } },
      }),
      [omnipus],
      [mainSession('omnipus')],
    )

    // Exact name: the row's two sibling icon buttons ("Past sessions with
    // Omnipus" / "New chat with Omnipus") would otherwise match a /Omnipus/
    // regex and throw on multiple matches.
    expect(screen.getByRole('button', { name: 'Omnipus' })).toBeInTheDocument()
    expect(screen.queryByRole('status', { name: 'Main chat unavailable' })).toBeNull()
  })

  it('helper/worker core_team members without a main produce no note and no row', () => {
    const planner = makeAgent({ id: 'planner', name: 'Planner', type: 'Subagent' })
    const extWorker = makeAgent({ id: 'ext-worker', name: 'Ext Worker', type: 'subagent_3p' })
    renderList(
      makeProject({ core_team: ['planner', 'ext-worker'], member_configs: {} }),
      [planner, extWorker],
      [],
    )

    expect(screen.queryByRole('status', { name: 'Main chat unavailable' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Planner' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Ext Worker' })).toBeNull()
  })
})
