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
    const strip = screen.getByTestId('all-tasks-tile').parentElement
    expect(header.nextElementSibling, 'no control row between the header and tiles').toBe(strip)
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
      expect(applied(), 'Board requests the maximum, not the 641px/default width').toBe(1008)
      expect(localStorage.getItem('panel-width:admin:tasks:ws-layout')).toBe('641')
      await user.click(screen.getByRole('radio', { name: 'List' }))
      expect(useUiStore.getState().panelWidth).toBe(previous)
      await user.click(screen.getByRole('radio', { name: 'Board' }))
      expect(applied()).toBe(1008)
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

  it('T6 orders List columns Pri, Title, Status, Actions, Tags, Agent, Updated and places Run in Actions', () => {
    renderLayout(<ListView tasks={[layoutTask()]} agents={[layoutAgent()]} onTaskClick={() => {}} />)
    const headers = screen.getAllByRole('columnheader')
    expect(headers.map((header) => header.textContent?.replace(/[↑↓]/g, '').trim())).toEqual(['Pri', 'Title', 'Status', 'Actions', 'Tags', 'Agent', 'Updated'])
    const cells = screen.getAllByRole('row')[1].querySelectorAll('td')
    expect(within(cells[3]).getByRole('button', { name: 'Run task Ray report' })).toBeVisible()
    expect(within(cells[6]).queryByRole('button')).not.toBeInTheDocument()
  })

  it('T7 uses shared flat FilterMenu/ViewSwitch, kit metadata badges and sentence-case List headers', async () => {
    const user = userEvent.setup()
    await renderTab()
    const group = screen.getByRole('radiogroup', { name: 'Task view' })
    expect(group).toHaveAttribute('data-slot', 'view-switch')
    for (const id of ['tasks-agent-filter', 'tasks-tag-filter']) expect(screen.getByTestId(id)).toHaveAttribute('data-slot', 'filter-menu')
    const board = screen.getByRole('radio', { name: 'Board' })
    expect(board).toHaveClass('border-0', 'bg-transparent', 'text-[var(--color-accent)]')
    const card = screen.getByText('Ray report').closest('[role="button"]')!
    expect(within(card as HTMLElement).getByText('Ray').tagName).toBe('DIV') // Badge's published DOM, not a hand-rolled span.
    expect(within(card as HTMLElement).getByText('docs').tagName).toBe('DIV')
    expect(within(screen.getByTestId('plan-filter-tile-plan-layout')).getByText('Ray').tagName).toBe('DIV')
    board.focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'List' })).toHaveFocus()
    expect(screen.getByRole('radio', { name: 'List' })).toHaveAttribute('aria-checked', 'true')
    for (const header of screen.getAllByRole('columnheader')) {
      const trigger = header.querySelector('button')
      expect(trigger).not.toHaveClass('uppercase', 'tracking-wider')
    }
    await user.click(screen.getByRole('button', { name: 'Filter by tags' }))
    await user.click(await screen.findByRole('menuitemcheckbox', { name: 'docs' }))
    expect(screen.getByRole('menuitemcheckbox', { name: 'docs' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('menu'), 'multi-select stays open for another selection').toBeVisible()
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'build' }))
    expect(useWorkspacesStore.getState().activeTags).toEqual(['docs', 'build'])
    await user.click(screen.getByRole('menuitem', { name: 'Clear tags' }))
    expect(useWorkspacesStore.getState().activeTags).toEqual([])
  })

  it('T9 gives the plan strip and status headers the darker panel surface without filling empty columns', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([layoutTask()])
    await renderTab()
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
