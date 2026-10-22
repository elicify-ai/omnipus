// RED — FR-160/174/175, US-17 AS-1/2, §6.3, BDD-17.1/2/20.
// Existing sidebar is real; QueryClient/router/stores are real; fetch only is fake.
import type { ComponentType } from 'react'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Stage 1 ships rooms OFF. Preserve the original enabled-feature oracles.
vi.mock('@/lib/featureSwitches', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/featureSwitches')>(),
  ROOMS_ENABLED: true,
}))
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { roomsQueryKeys } from '@/lib/api/rooms'
import { queryClient } from '@/lib/queryClient'
import { WorkspaceAgentList } from './WorkspaceAgentList'
import {
  agents, json, mountAt, Network, requiredExport, resetRealStores, room,
  stopHarness, workspace, WS,
} from '../../../../tests/l1-room-ui/harness'

// §6.3 "Cached list + hint" does not quote the group-chat words. The same
// column for the team list is BDD-12.13 "may be out of date", and the sidebar
// says the cached rows are out of date. Any of these is the hint; the empty
// and first-load strings are not.
const STALE_HINT = /out of date|out-of-date|stale|cached|last known/i

let net: Network
let currentPath: () => string
beforeEach(() => { resetRealStores(); net = new Network(); currentPath = () => '' })
afterEach(() => stopHarness())

async function showSidebar(projects = [workspace()]) {
  const mounted = await mountAt(<WorkspaceAgentList
    projects={projects} expandedIds={new Set(projects.map((project) => project.id))}
    activeId={WS} agents={agents} rosterState="fresh" sessions={[]}
    onToggle={() => {}} onOpen={() => {}} onOverlayClose={() => {}}
  />, '/')
  currentPath = () => mounted.router.state.location.pathname
  expect(screen.getByRole('button', { name: 'Sales' })).toBeVisible()
  // The group itself must exist during loading too (§6.3), so absence is an
  // immediate, precise behavioural RED instead of a fetch/time-budget failure.
  const group = screen.getByTestId(`group-chats-${WS}`)
  // Follow-up (h): every enabled room-section case proves the visible mount.
  expect(group).toBeVisible()
  return group
}

describe('workspace Group chats section', () => {
  it('[FR-174; BDD-17.1/2] sorts team first, squads second, ad-hoc third regardless of HTTP order', async () => {
    net.rooms = [
      room({ id: 'adhoc', name: 'Quick group', kind: 'adhoc' }),
      room({ id: 'squad', name: 'Launch', kind: 'squad' }),
      room({ id: 'team', name: 'Team room', kind: 'team' }),
    ]
    const group = await showSidebar()
    expect(within(group).getByText('Group chats', { exact: true })).toBeVisible()
    await within(group).findByTestId('room-row-team')
    const rows = Array.from(group.querySelectorAll('[data-testid^="room-row-"]'))
    expect(rows.map((row) => row.getAttribute('data-testid'))).toEqual(['room-row-team', 'room-row-squad', 'room-row-adhoc'])
    expect(rows.map((row) => row.textContent)).toEqual([expect.stringContaining('Team room'), expect.stringContaining('Launch'), expect.stringContaining('Quick group')])
    const mia = screen.getByRole('button', { name: 'Mia' })
    expect(mia.compareDocumentPosition(group) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    expect(net.unexpected).toEqual([])
  })

  it('[FR-160; BDD-17.20] a hidden team room is not shown or deleted; a visible squad stays listed', async () => {
    net.rooms = [room({ id: 'team', kind: 'team', name: 'Team room', hidden: true, members: ['mia'] }), room()]
    const group = await showSidebar()
    expect(await within(group).findByTestId('room-row-launch')).toHaveTextContent('Launch')
    expect(within(group).queryByTestId('room-row-team')).toBeNull()
    expect(net.writes()).toEqual([])
  })

  it('[FR-162/174] every workspace has its own section and room rows never leak across workspaces', async () => {
    const marketing = workspace({ id: 'marketing', name: 'Marketing', core_team: ['ray'], member_configs: { ray: { main_session_id: 'main-ray-marketing' } } })
    net.workspaces = [workspace(), marketing]
    net.respond('GET', '/api/v1/workspaces/marketing/rooms', () => json([room({ id: 'marketing-room', workspace_id: 'marketing', name: 'Marketing launch' })]))
    const sales = await showSidebar(net.workspaces)
    const other = screen.getByTestId('group-chats-marketing')
    expect(other).toBeVisible()
    expect(await within(sales).findByTestId('room-row-launch')).toHaveTextContent('Launch')
    expect(await within(other).findByTestId('room-row-marketing-room')).toHaveTextContent('Marketing launch')
    expect(within(sales).queryByTestId('room-row-marketing-room')).toBeNull()
    expect(within(other).queryByTestId('room-row-launch')).toBeNull()
  })

  it('[FR-174; K23; §6.3] needs_attention produces a marker on that room row', async () => {
    net.rooms = [room({ needs_attention: true }), room({ id: 'quiet', name: 'Quiet', needs_attention: false })]
    const group = await showSidebar()
    const attentionRow = await within(group).findByTestId('room-row-launch')
    const quietRow = await within(group).findByTestId('room-row-quiet')
    expect(within(attentionRow).getByRole('status', { name: /attention/i })).toBeVisible()
    expect(within(quietRow).queryByRole('status', { name: /attention/i })).toBeNull()
  })

  it('[§6.3] a successfully empty room list says exactly No group chats yet', async () => {
    net.rooms = []
    const group = await showSidebar()
    expect(await within(group).findByText('No group chats yet', { exact: true })).toBeVisible()
    expect(within(group).queryByRole('button', { name: /^retry$/i })).toBeNull()
  })

  it('[§6.3] a failed list shows Error plus Retry, never the true-empty text', async () => {
    net.respond('GET', `/api/v1/workspaces/${WS}/rooms`, () => json({ error: 'storage failure' }, 500))
    const group = await showSidebar()
    const retry = await within(group).findByRole('button', { name: /^retry$/i })
    expect(retry).toBeEnabled()
    expect(group).toHaveTextContent(/could not|failed|unavailable/i)
    expect(within(group).queryByText('No group chats yet', { exact: true })).toBeNull()
    net.respond('GET', `/api/v1/workspaces/${WS}/rooms`, () => json([room()]))
    await userEvent.setup().click(retry)
    expect(await within(group).findByTestId('room-row-launch')).toHaveTextContent('Launch')
    expect(within(group).queryByText('No group chats yet', { exact: true })).toBeNull()
  })

  it('[§6.3; FR-112] a failed refresh keeps the cached rows, marks them stale, and Retry success clears the hint', async () => {
    net.rooms = [room()]
    const group = await showSidebar()
    expect(await within(group).findByTestId('room-row-launch')).toHaveTextContent('Launch')
    expect(within(group).queryByText(STALE_HINT)).toBeNull()

    net.respond('GET', `/api/v1/workspaces/${WS}/rooms`, () => json({ error: 'storage failure' }, 500))
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: roomsQueryKeys.list(WS) })
    })

    // Wait until the 500 has been painted. The row has to still be there:
    // the error arm must not replace a cached list (§6.3).
    await waitFor(() => {
      expect(within(group).getByTestId('room-row-launch')).toHaveTextContent('Launch')
      expect(within(group).getByText(STALE_HINT)).toBeVisible()
    })
    const retry = within(group).getByRole('button', { name: /^retry$/i })
    expect(retry).toBeEnabled()
    expect(within(group).queryByText('No group chats yet', { exact: true })).toBeNull()

    const user = userEvent.setup()
    await user.click(retry)
    await waitFor(() => {
      expect(within(group).getByTestId('room-row-launch')).toHaveTextContent('Launch')
      expect(within(group).getByText(STALE_HINT)).toBeVisible()
    })

    net.respond('GET', `/api/v1/workspaces/${WS}/rooms`, () => json([room({ id: 'quiet', name: 'Quiet' })]))
    await user.click(within(group).getByRole('button', { name: /^retry$/i }))
    await waitFor(() => {
      expect(within(group).getByTestId('room-row-quiet')).toHaveTextContent('Quiet')
      expect(within(group).queryByText(STALE_HINT)).toBeNull()
    })
    expect(within(group).queryByTestId('room-row-launch')).toBeNull()
    expect(within(group).queryByText('No group chats yet', { exact: true })).toBeNull()
  })

  it('[FR-174; test row 119 SPA half] clicking a room opens its workspace room route, not a new agent session', async () => {
    const group = await showSidebar()
    const row = await within(group).findByTestId('room-row-launch')
    await userEvent.setup().click(row)
    // Real memory-history navigation is the observable outcome; this accepts
    // either a link or button without prescribing the row's implementation.
    await waitFor(() => expect(currentPath()).toBe('/workspaces/sales/rooms/launch'))
    expect(net.writes()).toEqual([])
  })
})

// not-wire-format: public React props confirmed by team-lead, 2026-10-14.
type SquadDialogProps = {
  open: boolean; onOpenChange(open: boolean): void; workspaceId: string
  kind?: 'squad' | 'adhoc'; initialAgentIds?: string[]
}

describe('FR-161/175/204 squad dialog (public props, real controls)', () => {
  async function dialog() {
    const SquadDialog = await requiredExport<ComponentType<SquadDialogProps>>('/src/components/rooms/SquadDialog.tsx', 'SquadDialog', 'FR-161/175; BDD-17.2')
    const mounted = await mountAt(<SquadDialog open onOpenChange={() => {}} workspaceId={WS} />, '/')
    currentPath = () => mounted.router.state.location.pathname
    return screen.findByRole('dialog')
  }

  it.each([0, 1, 2, 3])('[FR-204; BDD-17.20; K24] %s selected agents enables Create only from two upwards', async (count) => {
    const view = await dialog()
    const user = userEvent.setup()
    await user.type(within(view).getByRole('textbox', { name: /name/i }), 'Launch')
    for (const name of ['Mia', 'Jim', 'Researcher2'].slice(0, count)) {
      await user.click(await within(view).findByRole('checkbox', { name: new RegExp(name) }))
    }
    const create = within(view).getByRole('button', { name: /create/i })
    if (count < 2) {
      expect(create).toBeDisabled()
      expect(net.writes()).toEqual([])
    } else {
      expect(create).toBeEnabled()
      net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json(room({ members: ['mia', 'jim', 'researcher2'].slice(0, count) }), 201))
      await user.click(create)
      await waitFor(() => expect(net.writes()).toHaveLength(1))
      expect(net.writes()[0]?.body).toEqual({ kind: 'squad', name: 'Launch', agent_ids: ['mia', 'jim', 'researcher2'].slice(0, count) })
      await waitFor(() => expect(currentPath()).toBe('/workspaces/sales/rooms/launch'))
    }
  })

  it('[FR-161/175; BDD-17.2; K24] an ad-hoc dialog creates the two chosen agents without a required squad name', async () => {
    const SquadDialog = await requiredExport<ComponentType<SquadDialogProps>>('/src/components/rooms/SquadDialog.tsx', 'SquadDialog', 'FR-161/175; BDD-17.2')
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json(room({ kind: 'adhoc', members: ['jim', 'researcher2'] }), 201))
    const mounted = await mountAt(<SquadDialog open onOpenChange={() => {}} workspaceId={WS} kind="adhoc" initialAgentIds={['jim', 'researcher2']} />, '/')
    const view = await screen.findByRole('dialog')
    expect(within(view).queryByRole('textbox', { name: /name/i })).toBeNull()
    await userEvent.setup().click(within(view).getByRole('button', { name: /create/i }))
    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes()[0]?.body).toEqual({ kind: 'adhoc', agent_ids: ['jim', 'researcher2'] })
    await waitFor(() => expect(mounted.router.state.location.pathname).toBe('/workspaces/sales/rooms/launch'))
  })

  it('[FR-162/175/207] the squad picker lists workspace conversation agents only', async () => {
    const view = await dialog()
    await within(view).findByRole('checkbox', { name: /Mia/ })
    expect(within(view).getAllByRole('checkbox')).toHaveLength(3)
    expect(within(view).getByRole('checkbox', { name: /Mia/ })).toBeVisible()
    expect(within(view).getByRole('checkbox', { name: /Jim/ })).toBeVisible()
    expect(within(view).getByRole('checkbox', { name: /Researcher2/ })).toBeVisible()
    for (const name of ['Ray', 'GP Worker', 'External Worker', 'Judge', 'Omnipus', 'Anna']) {
      expect(within(view).queryByRole('checkbox', { name: new RegExp(name) })).toBeNull()
    }
  })

  it.each([0, 79, 80, 81])('[FR-161/175; K24] squad name length %s respects required name and maximum 80', async (length) => {
    const view = await dialog()
    const user = userEvent.setup()
    await user.click(await within(view).findByRole('checkbox', { name: /Mia/ }))
    await user.click(within(view).getByRole('checkbox', { name: /Jim/ }))
    const name = within(view).getByRole('textbox', { name: /name/i })
    if (length > 0) await user.type(name, 'x'.repeat(length))
    const create = within(view).getByRole('button', { name: /create/i })
    if (length === 0) {
      expect(create).toBeDisabled()
    } else if (length > 80) {
      // Both explicit rejection and native maxLength=80 are legal. Neither
      // may send an oversized name; this does not invent validation copy.
      if ((name as HTMLInputElement).value.length === 81) {
        expect(create).toBeDisabled()
      } else {
        expect(name).toHaveAttribute('maxlength', '80')
        expect(name).toHaveValue('x'.repeat(80))
      }
      expect(net.writes()).toEqual([])
    } else {
      expect(name).toHaveValue('x'.repeat(length))
      expect(create).toBeEnabled()
    }
  })
})

describe('sidebar New squad… reaches the real dialog (US-17 AS-2; FR-161/174/175; BDD-17.2)', () => {
  it('[FR-161/174/175; K24; BDD-17.2] New squad… posts the two chosen agents and opens the returned room without minting a session', async () => {
    net.rooms = [room({ id: 'quiet', name: 'Quiet' })]
    const created = room({ id: 'launch', name: 'Launch', members: ['mia', 'jim'] })
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => {
      net.rooms = [room({ id: 'quiet', name: 'Quiet' }), created]
      return json(created, 201)
    })
    const group = await showSidebar()
    expect(await within(group).findByTestId('room-row-quiet')).toHaveTextContent('Quiet')

    const trigger = within(group).getByRole('button', { name: 'New group chat in Sales' })
    await act(async () => {
      fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
      fireEvent.pointerUp(trigger, { button: 0, pointerType: 'mouse' })
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole('menuitem', { name: 'New squad…' }))
    const view = await screen.findByRole('dialog')
    await user.click(await within(view).findByRole('checkbox', { name: /Mia/ }))
    await user.click(within(view).getByRole('checkbox', { name: /Jim/ }))
    await user.type(within(view).getByRole('textbox', { name: /name/i }), 'Launch')
    await user.click(within(view).getByRole('button', { name: /create/i }))

    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes()[0]).toMatchObject({
      method: 'POST',
      path: `/api/v1/workspaces/${WS}/rooms`,
      body: { kind: 'squad', name: 'Launch', agent_ids: ['mia', 'jim'] },
    })
    expect(net.writes().filter((write) => write.path.includes('/sessions'))).toEqual([])
    await waitFor(() => expect(currentPath()).toBe('/workspaces/sales/rooms/launch'))
    // Navigation swaps the route outlet, so the list has to be read again.
    const shown = screen.getByTestId(`group-chats-${WS}`)
    expect(await within(shown).findByTestId('room-row-launch')).toHaveTextContent('Launch')
    expect(within(shown).getByTestId('room-row-quiet')).toHaveTextContent('Quiet')
  })
})
