// Founder review of 579757938: unknown timing must not manufacture a dash/separator.
import { expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { TaskCard } from './TaskCard'
import { layoutTask, renderLayout } from './tasksLayoutFixtures'

it('unknown terminal duration renders only the real agent, and a real duration without an agent has no leading separator', () => {
  for (const status of ['done', 'failed'] as const) {
    const mounted = renderLayout(<TaskCard task={layoutTask({ status })} onClick={vi.fn()} />)
    const row = screen.getByTestId('task-execution-row')
    expect(row).toHaveTextContent(/^Ray$/)
    expect(row.querySelector('time')).toBeNull()
    expect(row).not.toHaveTextContent('·')
    expect(row).not.toHaveTextContent('—')
    mounted.unmount()
  }
  renderLayout(<TaskCard task={layoutTask({ status: 'done', agent_name: undefined, agent_id: undefined, started_at: '2026-10-08T18:00:00Z', completed_at: '2026-10-08T18:01:05Z' })} onClick={vi.fn()} />)
  const row = screen.getByTestId('task-execution-row')
  expect(row).toHaveTextContent(/^1m 05s$/)
  expect(row).not.toHaveTextContent('·')
  expect(row).not.toHaveTextContent('Unassigned')
})
