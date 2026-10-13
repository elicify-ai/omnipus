// WU-10 RED: spec FR-104/109/112, BDD-12.4/14; §6.3 Add-agent picker.
// No mock of Popover, search, filtering, or catalogued controls.
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AddAgentPicker } from './AddAgentPicker'
import { DEFAULT, JUDGE, MIA, OMNIPUS, PLAN_SUPERVISOR, SALES, agent } from '../../../../tests/omnipus-l1-ui-red/fixtures'
import { useWorkspacesStore } from '@/store/workspacesStore'

vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(), fetchWorkspaces: vi.fn(),
}))
import { fetchWorkspaces } from '@/lib/api'
const ORPHAN = agent('orphan', { name: 'Orphan' })
let client: QueryClient

beforeEach(() => {
  vi.clearAllMocks()
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.mocked(fetchWorkspaces).mockResolvedValue([DEFAULT, SALES])
  useWorkspacesStore.setState({ activeWorkspaceId: 'sales' })
})
afterEach(() => client.clear())
function renderPicker(agents = [ORPHAN, MIA, OMNIPUS, JUDGE, PLAN_SUPERVISOR]) {
  const onAdd = vi.fn<(id: string) => void>()
  // The roster is an explicit input, not guessed from the active workspace.
  // workspaces is the optional WU-10 extension confirmed by the implementer.
  const props = { agents, memberIds: new Set(['mia']), onAdd, workspaces: [DEFAULT, SALES] }
  render(<QueryClientProvider client={client}><AddAgentPicker {...props} /></QueryClientProvider>)
  return onAdd
}
async function openPicker(user = userEvent.setup()) {
  await user.click(screen.getByRole('button', { name: /add.*agent/i }))
  return user
}

describe('WU-10 add-existing eligibility', () => {
  it('BDD-12.4 offers Orphan, but not a member or Omnipus or hidden engines', async () => {
    renderPicker()
    await openPicker()
    expect(screen.getByRole('button', { name: /Orphan/ })).toBeEnabled()
    expect(screen.queryByRole('button', { name: /Omnipus/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Mia/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Judge/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Plan Supervisor/ })).not.toBeInTheDocument()
  })
  it('BDD-12.4 marks the no-workspace candidate as Not in any workspace', async () => {
    renderPicker()
    await openPicker()
    expect(screen.getByRole('button', { name: /Orphan/ })).toHaveTextContent(/not in any workspace/i)
  })
  it('FR-104 search cannot make the hidden Omnipus identity eligible', async () => {
    renderPicker()
    const user = await openPicker()
    await user.type(screen.getByRole('textbox', { name: /search agents/i }), 'omnipus')
    expect(screen.queryByRole('button', { name: /Omnipus/ })).not.toBeInTheDocument()
    expect(screen.getByText(/no matching agents|no other agents/i)).toBeVisible()
  })
  it('FR-112 gives No other agents when the only nonmembers are excluded identities', async () => {
    renderPicker([MIA, OMNIPUS, JUDGE, PLAN_SUPERVISOR])
    await openPicker()
    expect(screen.getByText(/no other agents/i)).toBeVisible()
    expect(screen.queryByRole('button', { name: /Omnipus/ })).not.toBeInTheDocument()
  })
  it('BDD-12.14 opens with Enter, moves focus into search and selects Orphan without a pointer', async () => {
    const onAdd = renderPicker()
    const user = userEvent.setup()
    const trigger = screen.getByRole('button', { name: /add.*agent/i })
    trigger.focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(screen.getByRole('textbox', { name: /search agents/i })).toHaveFocus())
    await user.type(screen.getByRole('textbox', { name: /search agents/i }), 'Orphan')
    await user.tab()
    expect(screen.getByRole('button', { name: /Orphan/ })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(onAdd.mock.calls).toEqual([['orphan']])
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(screen.queryByRole('textbox', { name: /search agents/i })).not.toBeInTheDocument()
  })
  it('FR-109 Escape closes the picker and returns focus without adding a member', async () => {
    const onAdd = renderPicker()
    const user = await openPicker()
    await user.keyboard('{Escape}')
    expect(onAdd.mock.calls).toEqual([])
    await waitFor(() => expect(screen.getByRole('button', { name: /add.*agent/i })).toHaveFocus())
    expect(screen.queryByRole('textbox', { name: /search agents/i })).not.toBeInTheDocument()
  })
})
