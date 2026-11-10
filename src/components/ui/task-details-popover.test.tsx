// T15 replaces T14's hover trigger. Preserve full metadata, portal containment
// and touch preview isolation in the real Board/List/Graph consumers.
import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TaskCard } from '../workspaces/TaskCard'
import { ListView } from '../workspaces/ListView'
import { GraphView } from '../workspaces/graph/GraphView'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from '../workspaces/tasksLayoutFixtures'

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
it('T15 shows all task metadata in an unclipped click details panel in every view and touch info never opens the task', async () => {
  const user = userEvent.setup()
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
  vi.stubGlobal('PointerEvent', class extends MouseEvent {
    pointerType: string
    constructor(type: string, props: PointerEventInit = {}) { super(type, props); this.pointerType = props.pointerType ?? 'mouse' }
  })
  const task = layoutTask({ title: 'Full details title — gateway interruption and verification', plan_id: 'plan-layout', tags: ['docs', 'release'] })
  const plans = [layoutPlan()]
  const agents = [layoutAgent()]
  for (const view of ['board', 'list', 'graph']) {
    const onOpen = vi.fn()
    const mounted = renderLayout(view === 'board'
      ? <TaskCard task={task} plans={plans} agents={agents} onClick={() => onOpen(task)} showActions={false} />
      : view === 'list'
        ? <ListView tasks={[task]} plans={plans} agents={agents} onTaskClick={onOpen} />
        : <GraphView tasks={[task]} plans={plans} agents={agents} onTaskClick={onOpen} />)
    const info = await screen.findByRole('button', { name: `Task details: ${task.title}`, hidden: true })
    const icon = info.querySelector('svg')!
    fireEvent.pointerDown(icon, { pointerType: 'touch' })
    fireEvent.click(icon)
    const details = await screen.findByRole('dialog', { name: 'Task details' })
    expect(within(details).getByText(task.title)).toBeVisible()
    for (const field of ['Inbox', 'Ray', 'docs', 'release', 'Launch plan']) expect(within(details).getByText(field)).toBeVisible()
    expect(details.querySelector('time')).toHaveAttribute('datetime', task.updated_at)
    expect(details.parentElement?.closest('.overflow-auto,.overflow-hidden,.overflow-y-auto')).toBeNull()
    expect(onOpen, `${view}: touch info is details-only`).not.toHaveBeenCalled()
    await user.click(within(details).getByRole('button', { name: 'Open task' }))
    expect(onOpen).toHaveBeenCalledExactlyOnceWith(task)
    expect(screen.queryByRole('dialog', { name: 'Task details' })).not.toBeInTheDocument()
    mounted.unmount()
    mounted.client.clear()
  }
})
