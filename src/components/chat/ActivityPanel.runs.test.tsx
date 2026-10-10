// FR-033 / FR-013: Activity rows for queued and waiting runs, and the
// "Not delivered" row. The base RED pack (u13_activity-run-rows) covers the
// running row, MAIN row, tokens and Open link.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { ActivityPanel } from './ActivityPanel'
import type { ActivityItem } from '@/hooks/useRunningActivity'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn() }
})

function run(key: string, runState: 'running' | 'queued' | 'waiting'): ActivityItem {
  return {
    kind: 'task', key, taskLabel: `run ${key}`, status: runState === 'running' ? 'running' : 'parked',
    runState, runKind: 'scheduled', mode: 'isolated', agentName: 'Ava', availableTokens: null,
  }
}

describe('Activity — task run sections', () => {
  it('puts a queued run under Queued and a waiting run under Waiting, not Running now', () => {
    render(<ActivityPanel open onOpenChange={() => {}} running={[run('a', 'queued'), run('b', 'waiting'), run('c', 'running')]} recentlyFinished={[]} />)
    expect(within(screen.getByTestId('activity-section-queued')).getByText('run a')).toBeInTheDocument()
    expect(within(screen.getByTestId('activity-section-waiting')).getByText('run b')).toBeInTheDocument()
    const running = screen.getByTestId('activity-section-running')
    expect(within(running).getByText('run c')).toBeInTheDocument()
    expect(within(running).queryByText('run a')).toBeNull()
  })

  it('omits the Open control for a run with no session yet', () => {
    render(<ActivityPanel open onOpenChange={() => {}} running={[run('a', 'running')]} recentlyFinished={[]} />)
    expect(screen.queryByTestId('activity-row-open')).toBeNull()
  })
})

describe('Activity — not delivered row', () => {
  it('shows the refusal as its own row with the server line', () => {
    const item: ActivityItem = {
      kind: 'not_delivered', key: 'span-1:not_delivered', taskLabel: 'audit', status: 'cancelled',
      agentName: 'Ray', text: 'Report was not saved: the inbox is full.', at: '2026-01-01T00:00:02.000Z',
    }
    render(<ActivityPanel open onOpenChange={() => {}} running={[]} recentlyFinished={[item]} />)
    const row = screen.getByTestId('activity-row')
    expect(within(row).getByText('Not delivered · audit')).toBeInTheDocument()
    expect(within(row).getByTestId('activity-row-status-line')).toHaveTextContent('Report was not saved: the inbox is full.')
  })
})
