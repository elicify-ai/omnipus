// Oracle: T8 — zoom controls occupy their own space, not the graph's card viewport.
import { afterEach, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { GraphView } from './GraphView'
import { layoutTask, renderLayout } from '../tasksLayoutFixtures'

it('T8 reserves a separate zoom footer, removes the in-app credit and reproduces the vendor MIT notice', async () => {
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
  expect(screen.queryByRole('link', { name: 'React Flow attribution' })).not.toBeInTheDocument()
  expect(mounted.container.querySelector('.react-flow__attribution'), 'removed by the library option, not hidden with CSS').toBeNull()
  const notice = readFileSync(resolve(process.cwd(), 'NOTICE'), 'utf8')
  const vendorLicense = readFileSync(resolve(process.cwd(), 'node_modules/@xyflow/react/LICENSE'), 'utf8')
  expect(notice).toContain('@xyflow/react (React Flow) - MIT License')
  expect(notice).toContain('https://github.com/xyflow/xyflow')
  expect(notice).toContain(vendorLicense.trim())
})
afterEach(() => { vi.unstubAllGlobals() })
