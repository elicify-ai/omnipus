// SessionRow rename-action tests (team-lead steer, 2026-10-11): core now
// derives a main session's title server-side and REJECTS renames on a main
// with 409 "a main session's title is fixed" (core commit 9b9ef5935,
// work/session-core-titles-20261011). The SPA must therefore never offer
// Rename on a main; every other session keeps its inline rename.
//
// The row's pencil IconButton is the ONLY rename affordance in the SPA —
// SessionOverviewList's onRename plumbing exists solely to serve it (grep
// verified 2026-10-11), so hiding it here hides rename everywhere.
//
// Scaffolding (session fixture shape) is copied from SessionRow.test.tsx.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Session } from '@/lib/api'
import { SessionRow } from './SessionRow'

function sessionFixture(overrides: Partial<Session>): Session {
  return {
    id: 'row-1',
    type: 'main',
    agent_id: 'jim',
    title: 'Jim - Main Session',
    status: 'active',
    created_at: '2026-10-10T00:00:00Z',
    updated_at: '2026-10-10T00:00:00Z',
    channel: 'webchat',
    partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...overrides,
  } as Session
}

function renderRow(session: Session) {
  const onRename = vi.fn()
  render(
    <SessionRow
      session={session}
      isActive={false}
      isHighlighted={false}
      onSelect={vi.fn()}
      onRename={onRename}
      onDelete={vi.fn()}
      deleting={false}
      onEditingChange={vi.fn()}
    />,
  )
  return { onRename }
}

describe('SessionRow — rename action on a main session (server rejects it with 409)', () => {
  it('shows NO rename affordance on a main session row', () => {
    renderRow(sessionFixture({ type: 'main' }))
    expect(screen.queryByRole('button', { name: /^Rename /i })).not.toBeInTheDocument()
    expect(screen.queryByTitle('Rename')).not.toBeInTheDocument()
  })

  it('shows no rename affordance on a main even when it has no server title', () => {
    renderRow(sessionFixture({ type: 'main', title: '' }))
    expect(screen.queryByRole('button', { name: /^Rename /i })).not.toBeInTheDocument()
  })

  it('keeps the rename affordance on a non-main session row (unchanged behaviour)', async () => {
    const user = userEvent.setup()
    const { onRename } = renderRow(sessionFixture({ type: 'chat', title: 'My extra chat' }))

    const rename = screen.getByRole('button', { name: 'Rename My extra chat' })
    expect(rename).toBeInTheDocument()

    // Entering edit mode and committing still reaches the mutation.
    await user.click(rename)
    const input = screen.getByRole('textbox')
    await user.clear(input)
    await user.type(input, 'Renamed chat')
    await user.keyboard('{Enter}')
    expect(onRename).toHaveBeenCalledWith('Renamed chat')
  })
})
