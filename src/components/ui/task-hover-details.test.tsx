// T14 — one regression for the real task consumers + portal Hover Card.
import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TaskCard } from '../workspaces/TaskCard'
import { ListView } from '../workspaces/ListView'
import { GraphView } from '../workspaces/graph/GraphView'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from '../workspaces/tasksLayoutFixtures'

afterEach(() => vi.unstubAllGlobals())
it('T14 shows all task metadata in an unclipped hover/focus preview in every view and touch tap does not open the task', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  vi.stubGlobal('PointerEvent', class extends MouseEvent {
    pointerType: string
    constructor(type: string, props: PointerEventInit = {}) { super(type, props); this.pointerType = props.pointerType ?? 'mouse' }
  })
  const task = layoutTask({ title: 'Full preview title — gateway interruption and verification', plan_id: 'plan-layout', tags: ['docs', 'release'] })
  const plans = [layoutPlan()]
  const agents = [layoutAgent()]
  for (const view of ['board', 'list', 'graph']) {
    const onOpen = vi.fn()
    const mounted = renderLayout(view === 'board'
      ? <TaskCard task={task} plans={plans} agents={agents} onClick={() => onOpen(task)} showActions={false} />
      : view === 'list'
        ? <ListView tasks={[task]} plans={plans} agents={agents} onTaskClick={onOpen} />
        : <GraphView tasks={[task]} plans={plans} agents={agents} onTaskClick={onOpen} />)
    const title = await screen.findByText(task.title)
    const trigger = title.closest<HTMLElement>('button,[role="button"]')!
    await user.hover(trigger)
    const preview = await screen.findByRole('dialog', { name: 'Task preview' })
    expect(within(preview).getByText(task.title)).toBeVisible()
    for (const field of ['Inbox', 'Ray', 'docs', 'release', 'Launch plan']) expect(within(preview).getByText(field)).toBeVisible()
    expect(preview.querySelector('time')).toHaveAttribute('datetime', task.updated_at)
    expect(preview.parentElement?.closest('.overflow-auto,.overflow-hidden,.overflow-y-auto'), 'portal escapes the table/column/canvas clipping ancestors').toBeNull()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Task preview' })).not.toBeInTheDocument())
    fireEvent.focus(trigger)
    expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
    await user.keyboard('{Escape}')
    fireEvent.pointerDown(trigger, { pointerType: 'touch' })
    fireEvent.click(trigger)
    expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
    expect(onOpen, `${view}: touch tap is preview-only`).not.toHaveBeenCalled()
    await user.click(within(screen.getByRole('dialog', { name: 'Task preview' })).getByRole('button', { name: 'Open task' }))
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(task)
    mounted.unmount()
    mounted.client.clear()
  }
})
