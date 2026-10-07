// Founder additions T11–T13, 2026-10-07. One regression per requirement.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ListView } from './ListView'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'
import { TaskCard } from './TaskCard'
import { GraphView } from './graph/GraphView'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from './tasksLayoutFixtures'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/lib/api', async (original) => ({ ...await original<typeof import('@/lib/api')>(), fetchTasks: vi.fn(), fetchPlans: vi.fn(), fetchAgents: vi.fn(), fetchWorkspaceDelegation: vi.fn().mockResolvedValue(null) }))
import { fetchTasks, fetchPlans, fetchAgents } from '@/lib/api'
beforeEach(() => {
  useWorkspacesStore.setState({ activeTags: [], activePlanId: null })
  useUiStore.setState({ activePanel: null, panelWidth: null })
  vi.mocked(fetchTasks).mockResolvedValue([layoutTask()])
  vi.mocked(fetchPlans).mockResolvedValue([layoutPlan(), layoutPlan({ id: 'completed', state: 'done', title: 'Completed plan' })])
  vi.mocked(fetchAgents).mockResolvedValue([layoutAgent()])
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
})
afterEach(() => vi.unstubAllGlobals())

it('T11 keeps every List column present in one table-only horizontal scroller with aligned header/body widths', () => {
  renderLayout(<ListView tasks={[layoutTask()]} agents={[layoutAgent()]} onTaskClick={() => {}} />)
  const table = screen.getByRole('table')
  const headers = screen.getAllByRole('columnheader')
  expect(headers.map((h) => h.textContent?.replace(/[↑↓]/g, '').trim())).toEqual(['Pri', 'Title', 'Status', 'Details', 'Actions', 'Tags', 'Agent', 'Updated'])
  for (const header of headers) {
    expect(header.className).not.toMatch(/hidden|@max/)
  }
  expect(table).toHaveClass('table-fixed', 'min-w-[calc(37rem+var(--space-8)*2+var(--space-3))]')
  expect(table.className).not.toMatch(/@max/)
  expect(table.parentElement).toHaveClass('overflow-auto')
  expect(table.parentElement?.parentElement).toHaveClass('overflow-hidden')
  expect(table.querySelector('thead')).toHaveClass('sticky')
  expect(screen.queryByRole('button', { name: /Tags and Updated columns/ })).not.toBeInTheDocument()
})

it('T12 starts the Plans flat Accordion collapsed, keeps header controls visible, and shows tiles on one click', async () => {
  const user = userEvent.setup()
  const mounted = renderLayout(<WorkspaceTasksTab workspaceId="ws-layout" />)
  await screen.findByText('Ray report')
  const trigger = screen.getByRole('button', { name: 'Plans' })
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  expect(screen.getByRole('checkbox', { name: 'Show done plans' })).toBeVisible()
  expect(screen.getByRole('button', { name: 'New Plan' })).toBeVisible()
  expect(screen.queryByTestId('all-tasks-tile')).not.toBeInTheDocument()
  await user.click(trigger)
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByTestId('all-tasks-tile')).toBeVisible()
  expect(screen.getByTestId('plan-filter-tile-plan-layout')).toBeVisible()
  await user.click(trigger)
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByTestId('all-tasks-tile')).not.toBeInTheDocument()
  mounted.unmount()
  renderLayout(<WorkspaceTasksTab workspaceId="ws-layout" />)
  expect(screen.getByRole('button', { name: 'Plans' })).toHaveAttribute('aria-expanded', 'false')
})

it('T13 reserves fixed two-line Board/Graph title slots and one-line List ellipsis without splitting normal words', async () => {
  const title = 'Post-publish build verification: gateway interruption ' + 'x'.repeat(100)
  renderLayout(<>
    <section aria-label="Board clamp"><TaskCard task={layoutTask({ title })} onClick={() => {}} showActions={false} /></section>
    <section aria-label="List clamp"><ListView tasks={[layoutTask({ title })]} agents={[layoutAgent()]} onTaskClick={() => {}} /></section>
    <section aria-label="Graph clamp"><GraphView tasks={[layoutTask({ title })]} agents={[]} onTaskClick={() => {}} /></section>
  </>)
  const board = within(screen.getByRole('region', { name: 'Board clamp' })).getByText(title)
  const graph = await within(screen.getByRole('region', { name: 'Graph clamp' })).findByText(title)
  for (const text of [board, graph]) {
    expect(text).toHaveClass('line-clamp-2', 'break-normal', 'wrap-break-word')
    expect(text.className).toMatch(/h-\[calc\(var\(--type-(?:body-compact|caption)-size\)\*var\(--type-(?:body-compact|caption)-line-height\)\*2\)\]/)
    expect(text).not.toHaveClass('break-all', 'wrap-anywhere')
  }
  const list = within(screen.getByRole('region', { name: 'List clamp' })).getByRole('button', { name: `${title}, status Inbox` })
  expect(list).toHaveClass('truncate', 'block', 'min-w-0')
  expect(list).not.toHaveClass('line-clamp-2', 'break-all')
  expect(list.textContent).toBe(title)
})
