// T17 measures natural card content, not a guessed fixed pixel height.
import { afterEach, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { BoardView } from './BoardView'
import { layoutTask, renderLayout } from './tasksLayoutFixtures'

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('T17 shares the tallest natural card height across all lanes and remeasures after data changes and resize', async () => {
  let tallHeight = 180
  const callbacks = new Map<Element, ResizeObserverCallback>()
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    return new DOMRect(0, 0, 162, this.textContent?.includes('Tall task') ? tallHeight : 96)
  })
  vi.stubGlobal('ResizeObserver', class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { callbacks.set(target, this.callback) }
    unobserve(target: Element) { callbacks.delete(target) }
    disconnect() { for (const [target, callback] of callbacks) if (callback === this.callback) callbacks.delete(target) }
  })
  const short = layoutTask({ title: 'Short task' })
  const tall = layoutTask({ id: 'tall', title: 'Tall task', status: 'failed' })
  const mounted = renderLayout(<BoardView tasks={[short, tall]} plans={[]} agents={[]} altitude="top-level" onTaskClick={vi.fn()} />)
  const root = mounted.container.firstElementChild as HTMLElement
  await waitFor(() => expect(root.style.getPropertyValue('--tasks-board-card-height')).toBe('180px'))
  for (const title of ['Short task', 'Tall task']) expect(screen.getByText(title).closest('[role="button"]')).toHaveClass('min-h-[var(--tasks-board-card-height,auto)]')
  tallHeight = 220
  act(() => { for (const [target, callback] of callbacks) callback([{ target } as ResizeObserverEntry], {} as ResizeObserver) })
  await waitFor(() => expect(root.style.getPropertyValue('--tasks-board-card-height')).toBe('220px'))
  mounted.rerender(<QueryClientProvider client={mounted.client}><BoardView tasks={[short]} plans={[]} agents={[]} altitude="top-level" onTaskClick={vi.fn()} /></QueryClientProvider>)
  await waitFor(() => expect(root.style.getPropertyValue('--tasks-board-card-height')).toBe('96px'))
  mounted.unmount()
  expect(callbacks.size).toBe(0)
})
