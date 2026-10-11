// SessionRow main-title tests (founder UI feedback 2026-10-11, point 4).
//
// An untitled MAIN session row must read "<Agent> - Main Session" — derived
// from the agent's CURRENT display name, so a rename flows through without a
// reload — and never "Untitled session". A server-set title always wins.
// Non-main rows keep the existing "Untitled session" fallback (core owns
// auto-titling of user sessions; this surface must not change it).
//
// Scaffolding (session fixture shape) is copied from SessionRow.test.tsx.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
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

function renderTitle(session: Session, agentName?: string) {
  render(<SessionRow {...rowProps(session)} agentName={agentName} />)
  return screen.getByTestId('session-title-row-1')
}

describe('SessionRow — main session display title', () => {
  it('reads "<Agent> - Main Session" for an untitled main', () => {
    expect(renderTitle(sessionFixture({}), 'Jim')).toHaveTextContent('Jim - Main Session')
  })

  it('shows the server title when one is present, ignoring the agent name', () => {
    expect(renderTitle(sessionFixture({ title: 'Weekly planning' }), 'Jim')).toHaveTextContent('Weekly planning')
  })

  it('keeps "Untitled session" for an untitled NON-main row (unchanged titling)', () => {
    expect(renderTitle(sessionFixture({ type: 'chat' }), 'Jim')).toHaveTextContent('Untitled session')
  })

  it('follows an agent rename: rerendering with the new display name updates the row', () => {
    const props = rowProps(sessionFixture({}))
    const view = render(<SessionRow {...props} agentName="Jim" />)
    expect(screen.getByTestId('session-title-row-1')).toHaveTextContent('Jim - Main Session')
    view.rerender(<SessionRow {...props} agentName="Jim Prime" />)
    expect(screen.getByTestId('session-title-row-1')).toHaveTextContent('Jim Prime - Main Session')
    expect(screen.queryByText(/Untitled/i)).not.toBeInTheDocument()
  })
})
