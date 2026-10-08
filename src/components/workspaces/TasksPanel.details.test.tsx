// Founder T15/T16/T18/T19 additions supersede hover activation and metadata pills.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TaskCard } from './TaskCard'
import { ListView } from './ListView'
import { GraphView } from './graph/GraphView'
import { PlansFilterBand } from './PlansFilterBand'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from './tasksLayoutFixtures'

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(800)
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600)
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 800, 600))
  vi.stubGlobal('ResizeObserver', class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { this.callback([{ target, contentRect: target.getBoundingClientRect() } as ResizeObserverEntry], this as unknown as ResizeObserver) }
    unobserve() {}
    disconnect() {}
  })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('T15 corrected task details open only from info hover in every view, without activating the task, and dismiss on Escape/outside click', async () => {
  const user = userEvent.setup()
  const task = layoutTask({ plan_id: 'plan-layout', tags: ['docs', 'release'] })
  for (const view of ['board', 'list', 'graph']) {
    const onOpen = vi.fn()
    const mounted = renderLayout(view === 'board'
      ? <TaskCard task={task} plans={[layoutPlan()]} agents={[layoutAgent()]} onClick={() => onOpen(task)} />
      : view === 'list'
        ? <ListView tasks={[task]} plans={[layoutPlan()]} agents={[layoutAgent()]} onTaskClick={onOpen} />
        : <GraphView tasks={[task]} plans={[layoutPlan()]} agents={[layoutAgent()]} onTaskClick={onOpen} />)
    const info = await screen.findByRole('button', { name: `Task details: ${task.title}`, hidden: true })
    fireEvent.pointerEnter(screen.getByText(task.title))
    fireEvent.focus(info)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    fireEvent.pointerEnter(info, { pointerType: 'mouse' })
    const details = await screen.findByRole('dialog', { name: 'Task details' })
    for (const text of [task.title, 'Inbox', 'Ray', 'docs', 'release', 'Launch plan']) expect(within(details).getByText(text)).toBeVisible()
    expect(details.querySelector('time')).toHaveAttribute('datetime', task.updated_at)
    expect(details.parentElement?.closest('table,.react-flow,[data-testid="side-panel"]')).toBeNull()
    expect(onOpen, `${view}: info activation must not open the task`).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', { name: 'Task details' })).not.toBeInTheDocument()
    fireEvent.pointerEnter(info, { pointerType: 'mouse' })
    expect(await screen.findByRole('dialog', { name: 'Task details' })).toBeVisible()
    await user.click(document.body)
    expect(screen.queryByRole('dialog', { name: 'Task details' })).not.toBeInTheDocument()
    mounted.unmount()
    mounted.client.clear()
  }
})

it('T16 plan info shows full title state progress owner and update time without selecting the plan', async () => {
  const user = userEvent.setup()
  const onSelect = vi.fn()
  const plan = layoutPlan()
  const tasks = [layoutTask({ plan_id: plan.id, status: 'done' }), layoutTask({ id: 'pending', plan_id: plan.id })]
  renderLayout(<PlansFilterBand plans={[plan]} tasks={tasks} agents={[layoutAgent()]} selectedPlanId={null} onSelectPlan={onSelect} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: 'Plans' }))
  const info = screen.getByRole('button', { name: `Plan details: ${plan.title}` })
  await user.click(info)
  const details = await screen.findByRole('dialog', { name: 'Plan details' })
  for (const text of [plan.title, 'Draft', '1/2', 'Ray']) expect(within(details).getByText(text)).toBeVisible()
  expect(details.querySelector('time')).toHaveAttribute('datetime', plan.updated_at)
  expect(onSelect).not.toHaveBeenCalled()
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('dialog', { name: 'Plan details' })).not.toBeInTheDocument()
})

it('T18 task and plan metadata use plain text without bordered pills, retaining exact status agent progress and tags', async () => {
  const user = userEvent.setup()
  const task = layoutTask({ tags: ['docs', 'release'], todos: [{ text: 'Done', status: 'completed' }, { text: 'Pending', status: 'pending' }] })
  const mounted = renderLayout(<TaskCard task={task} onClick={vi.fn()} />)
  expect(mounted.container.querySelector('.rounded-full.border')).toBeNull()
  for (const text of ['Inbox', 'Ray', '1/2', 'docs', 'release']) expect(screen.getByText(text)).toBeVisible()
  mounted.unmount()
  const list = renderLayout(<ListView tasks={[task]} agents={[layoutAgent()]} onTaskClick={vi.fn()} />)
  expect(list.container.querySelector('.rounded-full.border')).toBeNull()
  list.unmount()
  const plans = renderLayout(<PlansFilterBand plans={[layoutPlan()]} tasks={[layoutTask({ plan_id: 'plan-layout', status: 'done' })]} agents={[layoutAgent()]} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: 'Plans' }))
  const tile = screen.getByTestId('plan-filter-tile-plan-layout')
  expect(tile.querySelector('.rounded-full.border')).toBeNull()
  for (const text of ['Draft', 'Ray', '1/1']) expect(within(tile).getByText(text)).toBeVisible()
  plans.unmount()
})

it('T19 task actions are not hidden by a hover-only ancestor on Board or Graph', async () => {
  for (const view of ['board', 'graph']) {
    const task = layoutTask()
    const mounted = renderLayout(view === 'board' ? <TaskCard task={task} onClick={vi.fn()} /> : <GraphView tasks={[task]} agents={[layoutAgent()]} onTaskClick={vi.fn()} />)
    const action = await screen.findByRole('button', { name: `Run task ${task.title}`, hidden: true })
    expect(action.closest('.opacity-0,[hidden]')).toBeNull()
    mounted.unmount()
  }
})
