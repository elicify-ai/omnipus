// Oracle: T8 — zoom controls occupy their own space, not the graph's card viewport.
import { afterEach, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { GraphView } from './GraphView'
import { layoutTask, renderLayout } from '../tasksLayoutFixtures'

it('T8 reserves a separate zoom-control footer outside the card viewport and keeps React Flow credit', async () => {
  class Observer { observe() {} unobserve() {} disconnect() {} }
  vi.stubGlobal('ResizeObserver', Observer)
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  const mounted = renderLayout(<GraphView tasks={[
    layoutTask({ id: 'first', title: 'First card' }),
    layoutTask({ id: 'second', title: 'Second card', blocked_by: ['first'] }),
  ]} agents={[]} onTaskClick={() => {}} />)
  expect(await screen.findByTestId('task-node-first')).toBeInTheDocument()
  const zoom = screen.getByRole('group', { name: 'Zoom graph' })
  expect(zoom.closest('.react-flow'), 'cards must never pan underneath the zoom controls').toBeNull()
  const footer = zoom.parentElement!
  expect(footer).toHaveClass('shrink-0')
  expect(footer.previousElementSibling).toContainElement(mounted.container.querySelector('.react-flow'))
  expect(screen.getByRole('link', { name: 'React Flow' })).toBeVisible()
})
afterEach(() => { vi.unstubAllGlobals() })
