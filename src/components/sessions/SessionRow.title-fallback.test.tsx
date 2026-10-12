// SessionRow title-fallback tests (combined-review R2-S4-01).
//
// sessionDisplayTitle returns string | null: a MAIN with neither a resolvable
// agent name nor an agent id yields null, and the row used to interpolate
// that null straight into its accessible names and confirm text — "Open
// null", `Delete "null"?`, an empty title span. The caller must pass the
// session's own agent_id (the id fallback the ruling accepts — the server's
// own empty-name fallback, pkg/gateway/session_title.go::withDisplayTitle)
// and keep the null out of every template.
//
// Scaffolding (session fixture shape) is copied from SessionRow.test.tsx.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { Session } from '@/lib/api'
import { SessionRow } from './SessionRow'

function sessionFixture(overrides: Partial<Session>): Session {
  return {
    id: 'row-1',
    type: 'main',
    agent_id: 'jim',
    title: '',
    status: 'active',
    created_at: '2026-10-10T00:00:00Z',
    updated_at: '2026-10-10T00:00:00Z',
    channel: 'webchat',
    partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...overrides,
  } as Session
}

function rowProps(session: Session) {
  return {
    session,
    isActive: false,
    isHighlighted: false,
    onSelect: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    deleting: false,
    onEditingChange: vi.fn(),
  }
}

describe('SessionRow — main title falls back to the agent id, never the literal "null" (R2-S4-01)', () => {
  it('renders the AGENT ID title when no agent name is resolved (prop omitted)', () => {
    render(<SessionRow {...rowProps(sessionFixture({}))} />)
    expect(screen.getByTestId('session-title-row-1')).toHaveTextContent('jim - Main Session')
    // The accessible name carries the derived title, never "Open null".
    expect(screen.getByRole('button', { name: 'Open jim - Main Session' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open null' })).not.toBeInTheDocument()
  })

  it('treats an empty-string agent name as unresolved — the id stands in', () => {
    // The roster can return an agent whose name is '' (?? agentId elsewhere
    // does not catch an empty string), which is exactly the trigger shape
    // the review flagged.
    render(<SessionRow {...rowProps(sessionFixture({}))} agentName="" />)
    expect(screen.getByTestId('session-title-row-1')).toHaveTextContent('jim - Main Session')
    expect(screen.queryByRole('button', { name: 'Open null' })).not.toBeInTheDocument()
  })

  it('the delete confirmation reads the derived title, never `Delete "null"?`', () => {
    render(<SessionRow {...rowProps(sessionFixture({}))} />)
    fireEvent.click(screen.getByRole('button', { name: 'Delete jim - Main Session' }))
    expect(screen.getByText('Delete "jim - Main Session"?')).toBeInTheDocument()
    expect(screen.queryByText('Delete "null"?')).not.toBeInTheDocument()
  })

  it('the delete button label reads the derived title, never `Delete null`', () => {
    render(<SessionRow {...rowProps(sessionFixture({}))} />)
    expect(screen.getByRole('button', { name: 'Delete jim - Main Session' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete null' })).not.toBeInTheDocument()
  })
})
