// The status badge colour on a session row. "Interrupted is not Failed" was
// pinned by the deleted Sidebar.lifecycle-labels tests; it lives here now.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { Session } from '@/lib/api'
import { SessionRow } from './SessionRow'

function renderRow(overrides: Partial<Session>) {
  const session = {
    id: 'row-1', type: 'chat', agent_id: 'ava', title: 'A chat', status: 'active',
    created_at: '2026-10-10T00:00:00Z', updated_at: '2026-10-10T00:00:00Z', channel: 'webchat', partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...overrides,
  } as Session
  render(
    <SessionRow
      session={session} isActive={false} isHighlighted={false} onSelect={vi.fn()} onRename={vi.fn()}
      onDelete={vi.fn()} deleting={false} onEditingChange={vi.fn()}
    />,
  )
  return screen.getByTestId('session-status-row-1')
}

describe('SessionRow status badge', () => {
  // Founder ruling (supersedes the earlier warning tone): Interrupted is a normal
  // outcome, e.g. the user pressed Stop, so it is neutral grey - never warning or error.
  it('draws Interrupted in the neutral grey tone, not warning and not the error colour Failed uses', () => {
    const badge = renderRow({ lifecycle_state: 'interrupted' })
    expect(badge).toHaveTextContent('Interrupted')
    expect(badge.className).toContain('--color-muted')
    expect(badge.className).not.toContain('--color-warning')
    expect(badge.className).not.toContain('--color-error')
  })

  it('draws Failed in the error colour', () => {
    const badge = renderRow({ lifecycle_state: 'failed' })
    expect(badge).toHaveTextContent('Failed')
    expect(badge.className).toContain('--color-error')
  })

  it('shows the stop cause on a stopped row', () => {
    const badge = renderRow({ lifecycle_state: 'stopped', stop_note: { at: '2026-10-10T00:00:00Z', by: 'human:u1', seq: 1, cause: 'cascade' } })
    expect(badge).toHaveTextContent('Stopped · cascade')
  })

  it('shows no lifecycle word for a session with no lifecycle, and does not crash', () => {
    const badge = renderRow({})
    expect(badge).toHaveTextContent('Unavailable')
    expect(badge.textContent).not.toMatch(/working|waiting|done|failed|interrupted|stopped/i)
  })
})
