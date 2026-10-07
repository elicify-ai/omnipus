// Round-1 A/B: real width reducer/hook and real Plans Accordion/Checkbox.
import { useEffect, useRef } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useTasksBoardLayout } from './useTasksBoardLayout'
import { useUiStore } from '@/store/ui'
import { clampPanelWidth } from '@/components/panel-shell/panelWidth'
import { PlansFilterBand } from './PlansFilterBand'
import { layoutAgent, layoutPlan, renderLayout } from './tasksLayoutFixtures'

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('A keeps Board at the dock ceiling after parent hydration on both initial and cached reopen', async () => {
  const observed = new Map<Element, ResizeObserverCallback>()
  const width = () => clampPanelWidth(useUiStore.getState().panelWidth ?? 641, 1440, 0) - 7
  class Observer {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { observed.set(target, this.callback); this.callback([{ target, contentRect: { width: width() } } as ResizeObserverEntry], this as unknown as ResizeObserver) }
    disconnect() { for (const [target, callback] of observed) if (callback === this.callback) observed.delete(target) }
    unobserve(target: Element) { observed.delete(target) }
  }
  vi.stubGlobal('ResizeObserver', Observer)
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 0, width(), 600))
  const unsubscribe = useUiStore.subscribe(() => {
    for (const [target, callback] of observed) callback([{ target, contentRect: { width: width() } } as ResizeObserverEntry], {} as ResizeObserver)
  })
  function Board() {
    const surface = useRef<HTMLDivElement>(null)
    const frame = useRef<HTMLDivElement>(null)
    const { boardFits } = useTasksBoardLayout(surface, frame, true, 'ws-gate')
    return <div ref={surface}><div ref={frame} data-testid="board-mode">{boardFits ? 'Board' : 'List fallback'}</div></div>
  }
  function HydratedPanel({ show }: { show: boolean }) {
    // Same effect ordering as cached SidePanelShell: the child mounts first,
    // then the parent's real persisted-width hydration restores its 641px request.
    useEffect(() => { if (show) useUiStore.getState().setPanelWidth(641) }, [show])
    return show ? <aside data-testid="side-panel"><Board /></aside> : null
  }
  useUiStore.setState({ activePanel: { id: 'tasks', context: { workspaceId: 'ws-gate' } }, panelWidth: null })
  const mounted = render(<HydratedPanel show />)
  try {
    await waitFor(() => expect(screen.getByTestId('board-mode')).toHaveTextContent('Board'))
    await waitFor(() => expect(clampPanelWidth(useUiStore.getState().panelWidth ?? 0, 1440, 0)).toBe(1008))
    act(() => useUiStore.getState().closePanel())
    mounted.rerender(<HydratedPanel show={false} />)
    act(() => useUiStore.getState().openPanel('tasks', { workspaceId: 'ws-gate' }))
    mounted.rerender(<HydratedPanel show />)
    await waitFor(() => expect(screen.getByTestId('board-mode')).toHaveTextContent('Board'))
    expect(clampPanelWidth(useUiStore.getState().panelWidth ?? 0, 1440, 0)).toBe(1008)
    mounted.unmount()
    expect(useUiStore.getState().panelWidth, 'restore the hydrated request, not the pre-hydration/null slice').toBe(641)
  } finally { mounted.unmount(); unsubscribe() }
})

it('B checking Show done expands the collapsed band and makes completed plans visible immediately', async () => {
  const user = userEvent.setup()
  renderLayout(<PlansFilterBand plans={[layoutPlan({ state: 'done', title: 'Completed plan' })]} tasks={[]} agents={[layoutAgent()]} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} showNewPlanTile={false} />)
  expect(screen.getByRole('button', { name: 'Plans' })).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByRole('button', { name: 'Completed plan' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('checkbox', { name: 'Show done plans' }))
  expect(screen.getByRole('button', { name: 'Plans' })).toHaveAttribute('aria-expanded', 'true')
  expect(await screen.findByRole('button', { name: 'Completed plan' })).toBeVisible()
})
