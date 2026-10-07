// T7's single regression lives in ui so design-system-components.json records
// its executed unit check for both published composites. It mounts the real Tasks consumer.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WorkspaceTasksTab } from '../workspaces/WorkspaceTasksTab'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from '../workspaces/tasksLayoutFixtures'
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchTasks: vi.fn(), fetchPlans: vi.fn(), fetchAgents: vi.fn(), fetchWorkspaceDelegation: vi.fn().mockResolvedValue(null),
}))
import { fetchTasks, fetchPlans, fetchAgents } from '@/lib/api'
beforeEach(() => {
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
describe('Tasks panel layout', () => {
  it('T7 uses shared flat FilterMenu/ViewSwitch and sentence-case List headers; T18 metadata is plain text', async () => {
    const user = userEvent.setup()
    renderLayout(<WorkspaceTasksTab workspaceId="ws-layout" />)
    await screen.findByText('Ray report')
    await user.click(screen.getByRole('button', { name: 'Plans' })) // T12 only changes the tile setup.
    const group = screen.getByRole('radiogroup', { name: 'Task view' })
    expect(group).toHaveAttribute('data-slot', 'view-switch')
    for (const id of ['tasks-agent-filter', 'tasks-tag-filter']) expect(screen.getByTestId(id)).toHaveAttribute('data-slot', 'filter-menu')
    const board = screen.getByRole('radio', { name: 'Board' })
    expect(board).toHaveClass('border-0', 'bg-transparent', 'text-[var(--color-accent)]')
    const card = screen.getByText('Ray report').closest('[role="button"]')!
    expect(within(card as HTMLElement).getByText('Ray').tagName).toBe('SPAN')
    expect(within(card as HTMLElement).getByText('docs').tagName).toBe('SPAN')
    expect(within(screen.getByTestId('plan-filter-tile-plan-layout')).getByText('Ray').tagName).toBe('SPAN')
    expect(card.querySelector('.rounded-full.border')).toBeNull()
    board.focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'List' })).toHaveFocus()
    expect(screen.getByRole('radio', { name: 'List' })).toHaveAttribute('aria-checked', 'true')
    for (const header of screen.getAllByRole('columnheader')) {
      const trigger = header.querySelector('button') ?? header.querySelector('span') // T11 removes the Actions reveal control.
      expect(trigger, 'every header keeps a visible sentence-case label').not.toBeNull()
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
})
