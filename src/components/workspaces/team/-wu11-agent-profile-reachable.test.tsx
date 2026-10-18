/**
 * -wu11-agent-profile-reachable.test.tsx — WU-11 KEEP-GREEN GUARD (NOT RED).
 *
 * team-lead steer, frontend impact map item 3: when /agents/$agentId goes
 * (FR-108), the agent profile slide-over must stay reachable from the
 * surfaces that SURVIVE the removal:
 *   1. a Team card's row menu (TeamListView → Edit),
 *   2. the sidebar row menu (AgentRowMenu → Edit),
 *   3. Settings → System agents (SystemAgentsSection → Edit).
 *
 * All three drive the ONE store-driven editor (useUiStore.openEditAgentSlideOver;
 * the slide-over is hosted once in the app shell — see agents.tsx's layout
 * note). This file replaces the openEditAgentSlideOver store function with a
 * spy and asserts each real control still calls it with the right agent and
 * workspace context. It PASSES today and MUST still pass after GREEN; if the
 * WU-11 removal accidentally cuts one of these paths, this guard fails.
 *
 * Oracle: omnipus-system-agent-spec.md US-12 (Team as the agent home),
 * BDD-12.10 ("Edit, Remove and Delete everywhere are reachable"), §15 WU-11.
 * Labelled keep-green in tests/l1-wu11-red/ORACLE.md.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { useUiStore } from '@/store/ui'
import type { Agent, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { TeamListView } from './TeamListView'
import { AgentRowMenu } from '@/components/layout/sidebar/AgentRowMenu'
import { SystemAgentsSection } from '@/components/settings/SystemAgentsSection'

const openEditAgentSlideOver = vi.hoisted(() => vi.fn())

vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn(),
}))

import { fetchAgents } from '@/lib/api'

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'sales',
    name: 'Sales',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    core_team: ['mia'],
    member_configs: { mia: { main_session_id: 'main-mia-1' } },
    ...overrides,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ openEditAgentSlideOver, toasts: [] })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('WU-11 keep-green — the agent profile editor stays reachable without /agents/$agentId', () => {
  it('a Team card menu opens the editor for the agent in its workspace', async () => {
    const user = userEvent.setup()
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <TeamListView
          workspace={makeWorkspace()}
          agents={[mia]}
          load="ready"
          onRetry={() => {}}
          workspaces={[makeWorkspace()]}
          busy={false}
          onRemove={() => {}}
          onSetUpTeam={() => {}}
          addAgent={<span data-testid="guard-add-agent-slot" />}
        />
      </QueryClientProvider>,
    )
    await user.click(screen.getByTestId('team-card-actions-mia'))
    await user.click(screen.getByRole('button', { name: 'Edit Mia' }))
    expect(openEditAgentSlideOver).toHaveBeenCalledWith('mia', 'sales')
  })

  it('the sidebar row menu opens the editor with the workspace context', async () => {
    const user = userEvent.setup()
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
    render(
      <AgentRowMenu workspace={makeWorkspace()} agent={mia} agentId="mia" agentName="Mia" />,
    )
    await user.click(screen.getByRole('button', { name: 'More actions for Mia' }))
    await user.click(screen.getByRole('menuitem', { name: 'Edit' }))
    expect(openEditAgentSlideOver).toHaveBeenCalledWith('mia', 'sales')
  })

  it('Settings → System agents opens the same editor for Omnipus', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'omnipus', name: 'Omnipus', type: 'core' }),
    ] as Agent[])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <SystemAgentsSection />
      </QueryClientProvider>,
    )
    await user.click(await screen.findByRole('button', { name: 'Edit Omnipus' }))
    expect(openEditAgentSlideOver).toHaveBeenCalledWith('omnipus')
  })

  // CHECK F4 (WU-11): the three controls above only OPEN the editor — the
  // place that RENDERS it is the shell's single `<AgentProfile />` mount
  // (AppShell.tsx, hosted once at shell level since agents.tsx was deleted,
  // FR-108). Pin the mount itself: if a future change drops it, every
  // openEditAgentSlideOver call renders nowhere and this dies with them.
  it('the app shell hosts the store-driven editor exactly once', () => {
    const shell = readFileSync(join(import.meta.dirname, '..', '..', '..', '..', 'src/components/layout/AppShell.tsx'), 'utf8')
    expect(shell, 'the shell must import the editor it hosts').toContain("import { AgentProfile } from '@/components/agents/AgentProfile'")
    expect(shell.match(/<AgentProfile \/>/g) ?? [], 'exactly one <AgentProfile /> mount in AppShell').toHaveLength(1)
  })
})
