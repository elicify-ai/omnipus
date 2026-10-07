// Oracle: founder-approved T1/T2/T4/T5/T6/T7/T9, 2026-10-07.
// Real components/stores/kit controls; only network/navigation/geometry edges are simulated.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'
import { ListView } from './ListView'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { clampPanelWidth, panelDefaultWidth } from '@/components/panel-shell/panelWidth'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from './tasksLayoutFixtures'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchTasks: vi.fn(), fetchPlans: vi.fn(), fetchAgents: vi.fn(),
  fetchWorkspaceDelegation: vi.fn().mockResolvedValue(null),
}))
import { fetchTasks, fetchPlans, fetchAgents } from '@/lib/api'

let measuredWidth = 0
const observers = new Map<Element, ResizeObserverCallback>()
class LayoutResizeObserver {
  constructor(private callback: ResizeObserverCallback) {}
  observe(target: Element) {
    observers.set(target, this.callback)
    this.callback([{ target, contentRect: { width: measuredWidth, height: 600 } } as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
  unobserve(target: Element) { observers.delete(target) }
  disconnect() { for (const [target, callback] of observers) if (callback === this.callback) observers.delete(target) }
}
function resizeFrame(width: number) {
  measuredWidth = width
  act(() => {
    for (const [target, callback] of [...observers]) {
      callback([{ target, contentRect: { width, height: 600 } } as ResizeObserverEntry], {} as ResizeObserver)
    }
  })
}

beforeEach(() => {
  measuredWidth = 0
  observers.clear()
  vi.stubGlobal('ResizeObserver', LayoutResizeObserver)
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  useWorkspacesStore.setState({ activeTags: [], activePlanId: null, boardAltitude: 'top-level' })
  useUiStore.setState({ activePanel: null, panelWidth: null })
  vi.mocked(fetchPlans).mockResolvedValue([layoutPlan()])
  vi.mocked(fetchAgents).mockResolvedValue([layoutAgent(), layoutAgent({ id: 'jim', name: 'Jim' })])
  vi.mocked(fetchTasks).mockResolvedValue([
    layoutTask({ plan_id: 'plan-layout' }),
    layoutTask({ id: 'task-jim', title: 'Jim build', agent_id: 'jim', agent_name: 'Jim', tags: ['build'], plan_id: 'plan-layout', blocked_by: ['task-ray'] }),
  ])
})
afterEach(() => { vi.unstubAllGlobals() })

async function renderTab() {
  const mounted = renderLayout(<WorkspaceTasksTab workspaceId="ws-layout" />)
  await screen.findByText('Ray report')
  return mounted
}

describe('Tasks panel layout', () => {
  it('T1 keeps Agent/Tags and New Task on one row immediately below the view switch in every view', async () => {
    const user = userEvent.setup()
    await renderTab()
    for (const name of ['Board', 'List', 'Graph']) {
      await user.click(screen.getByRole('radio', { name }))
      const create = screen.getByRole('button', { name: 'New Task' })
      const row = create.parentElement!
      expect(within(row).getByRole('button', { name: /Filter by agent/ }), `${name}: Agent shares New Task's row`).toBeVisible()
      expect(within(row).getByRole('button', { name: 'Filter by tags' }), `${name}: Tags shares New Task's row`).toBeVisible()
      const switchRow = screen.getByRole('radiogroup', { name: 'Task view' }).parentElement
      expect(row.previousElementSibling, `${name}: no extra toolbar row intervenes`).toBe(switchRow)
      expect(row).toHaveClass('flex', 'items-center', 'justify-between')
      expect(row).not.toHaveClass('flex-wrap', 'flex-col')
    }
    // Filters are never visible-but-unapplied, nor cleared when changing views.
    await user.click(screen.getByRole('button', { name: /Filter by agent/ }))
    await user.click(await screen.findByRole('menuitem', { name: 'Ray' }))
    for (const name of ['List', 'Board', 'Graph']) {
      await user.click(screen.getByRole('radio', { name }))
      await waitFor(() => expect(screen.queryByText('Jim build')).not.toBeInTheDocument())
      expect(await screen.findByText('Ray report')).toBeInTheDocument()
    }
  })

  it('T2 places the Show done checkbox in the Plans header and toggles only completed tiles', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchPlans).mockResolvedValue([layoutPlan(), layoutPlan({ id: 'done-plan', title: 'Completed launch', state: 'done' })])
    await renderTab()
    const check = screen.getByRole('checkbox', { name: 'Show done plans' })
    const header = screen.getByRole('heading', { name: 'Plans' }).parentElement!
    expect(header).toContainElement(check)
    expect(within(header).getByRole('button', { name: 'New Plan' })).toBeVisible()
    expect(within(header).getByText('Show done (1)')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Plans' })) // T12: expand before testing tile behavior.
    const strip = screen.getByTestId('all-tasks-tile').parentElement
    expect(header.nextElementSibling, 'the Accordion content directly follows the header').toContainElement(strip)
    expect(header.nextElementSibling).toHaveAttribute('role', 'region')
    expect(check).not.toBeChecked()
    expect(screen.queryByTestId('plan-filter-tile-done-plan')).not.toBeInTheDocument()
    await user.click(check)
    expect(check).toBeChecked()
    expect(screen.getByTestId('plan-filter-tile-done-plan')).toBeVisible()
    await user.click(check)
    expect(check).not.toBeChecked()
    expect(screen.queryByTestId('plan-filter-tile-done-plan')).not.toBeInTheDocument()
    expect(screen.getByTestId('plan-filter-tile-plan-layout')).toBeVisible()
  })

  it('T4 widens Board to the docked ceiling, restores explicit/default widths, and leaves full screen alone', async () => {
    const user = userEvent.setup()
    for (const previous of [641, null]) {
      useUiStore.setState({ activePanel: { id: 'tasks', context: { workspaceId: 'ws-layout' } }, panelWidth: previous })
      localStorage.setItem('panel-width:admin:tasks:ws-layout', '641')
      const mounted = renderLayout(<aside data-testid="side-panel"><WorkspaceTasksTab workspaceId="ws-layout" /></aside>)
      await screen.findByText('Ray report')
      const applied = () => clampPanelWidth(useUiStore.getState().panelWidth ?? panelDefaultWidth(1440, 0), 1440, 0)
      // SP-17 ceiling = min(1440 × .7, 1440 − 360) = 1008px; exact spec arithmetic.
      await waitFor(() => expect(applied(), 'Board requests the maximum after width hydration, not the 641px/default width').toBe(1008))
      expect(localStorage.getItem('panel-width:admin:tasks:ws-layout')).toBe('641')
      await user.click(screen.getByRole('radio', { name: 'List' }))
      expect(useUiStore.getState().panelWidth).toBe(previous)
      await user.click(screen.getByRole('radio', { name: 'Board' }))
      await waitFor(() => expect(applied()).toBe(1008))
      await user.click(screen.getByRole('radio', { name: 'Graph' }))
      expect(useUiStore.getState().panelWidth).toBe(previous)
      mounted.unmount()
      mounted.client.clear()
    }
    useUiStore.setState({ activePanel: { id: 'tasks', context: { workspaceId: 'ws-layout' } }, panelWidth: 641 })
    const full = await renderTab()
    expect(useUiStore.getState().panelWidth, 'full-screen Tasks cannot resize the docked panel').toBe(641)
    full.unmount()
  })

  it('T5 substitutes List below six readable columns and automatically restores the still-selected Board', async () => {
    measuredWidth = 973 // six × the existing readable 162px column floor = 972px.
    await renderTab()
    for (const width of [972, 971, 641, 972, 973]) {
      resizeFrame(width)
      const fallback = width < 972
      expect(screen.getByRole('radio', { name: 'Board' })).toHaveAttribute('aria-checked', 'true')
      if (fallback) {
        expect(screen.getByText('Board needs more room — showing list')).toBeVisible()
        expect(screen.getByRole('table')).toBeVisible()
        expect(screen.queryAllByRole('group', { name: / column$/ })).toHaveLength(0)
      } else {
        expect(screen.queryByText('Board needs more room — showing list')).not.toBeInTheDocument()
        expect(screen.queryByRole('table')).not.toBeInTheDocument()
        expect(screen.getAllByRole('group', { name: / column$/ })).toHaveLength(6)
      }
    }
    resizeFrame(971)
    act(() => { window.dispatchEvent(new Event('resize')) })
    expect(screen.getByRole('table'), 'window events do not override the measured panel width').toBeVisible()
  })

  it('G keeps the fallback live region mounted before and after a measured width change', async () => {
    measuredWidth = 1001
    await renderTab()
    const region = screen.getByTestId('tasks-board-fallback')
    expect(region).toHaveAttribute('role', 'status')
    expect(region).toBeEmptyDOMElement()
    resizeFrame(641)
    expect(screen.getByText('Board needs more room — showing list')).toBe(region)
    resizeFrame(1001)
    expect(screen.getByTestId('tasks-board-fallback')).toBe(region)
    expect(region).toBeEmptyDOMElement()
  })

  it('G includes the visible selected-tag count in the filter accessible name', async () => {
    await renderTab()
    act(() => useWorkspacesStore.getState().setActiveTags(['docs', 'build']))
    const filter = within(screen.getByTestId('tasks-tag-filter')).getByRole('button')
    expect(filter).toHaveTextContent('2 tags')
    expect(filter).toHaveAccessibleName('Filter by tags (2 tags)')
  })

  it('T6 orders List columns Pri, Title, Status, Actions, Tags, Agent, Updated and places Run in Actions', () => {
    renderLayout(<ListView tasks={[layoutTask()]} agents={[layoutAgent()]} onTaskClick={() => {}} />)
    const headers = screen.getAllByRole('columnheader')
    expect(headers.map((header) => header.textContent?.replace(/[↑↓]/g, '').trim())).toEqual(['Pri', 'Title', 'Status', 'Actions', 'Tags', 'Agent', 'Updated'])
    const cells = screen.getAllByRole('row')[1].querySelectorAll('td')
    expect(within(cells[3]).getByRole('button', { name: 'Run task Ray report' })).toBeVisible()
    expect(within(cells[6]).queryByRole('button')).not.toBeInTheDocument()
  })

  it('T9 gives the plan strip and status headers the darker panel surface without filling empty columns', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([layoutTask()])
    await renderTab()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Plans' }))
    const strip = screen.getByTestId('all-tasks-tile').parentElement!
    expect(strip).toHaveClass('bg-[var(--color-surface-1)]')
    const inbox = screen.getByRole('group', { name: 'Inbox column' })
    const board = inbox.parentElement!.parentElement!.parentElement!
    expect(board).toHaveClass('bg-[var(--color-surface-2)]')
    const header = inbox.parentElement!.previousElementSibling!
    expect(header).toHaveClass('bg-[var(--color-surface-1)]')
    for (const name of ['Next', 'In Progress', 'Blocked', 'Done', 'Failed']) {
      expect(screen.getByRole('group', { name: `${name} column` }).children, `${name} stays empty`).toHaveLength(0)
    }
  })
})
