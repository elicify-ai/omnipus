// SessionOverviewList title-fallback tests (combined-review R2-S4-01).
//
// The overview list shows a session's title in three places: the
// expand-toggle labels ("Expand <title> delegated sessions"), the collapse
// labels, and the row itself. A MAIN group whose agent name cannot be
// resolved used to interpolate sessionDisplayTitle's null into all three —
// "Expand null delegated sessions" and an empty row title. The caller must
// pass the session's agent_id (the ruling's id fallback) so the derivation
// lands on "jim - Main Session" instead.
//
// Harness: real SessionOverviewList over a plain fixtures map, wrapped in a
// QueryClientProvider (SessionTree's children loader uses useQueries) —
// copied from SessionOverviewList.main-title.test.tsx.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Session } from '@/lib/api'
import { SessionOverviewList } from './SessionOverviewList'

function sessionFixture(overrides: Partial<Session>): Session {
  return {
    id: 'main-1',
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

function listProps(sessions: Session[], childrenByParent: Map<string, Session[]>) {
  return {
    sessions,
    childrenByParent,
    expandedIds: new Set<string>(),
    toggleExpand: vi.fn(),
    activeSessionId: null,
    highlightedId: undefined,
    mode: 'sessions' as const,
    selectSession: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    isDeleting: () => false,
    onEditingChange: vi.fn(),
  }
}

function renderList(sessions: Session[], childrenByParent: Map<string, Session[]>, agentName?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <SessionOverviewList {...listProps(sessions, childrenByParent)} agentName={agentName} />
    </QueryClientProvider>,
  )
}

describe('SessionOverviewList — titles fall back to the agent id, never the literal "null" (R2-S4-01)', () => {
  it('an unresolved group name yields id-based expand labels and row title (prop omitted)', () => {
    const main = sessionFixture({ id: 'main-1', child_count: 1 })
    const child = sessionFixture({ id: 'child-1', type: 'delegate', parent_session_id: 'main-1' })
    renderList([main], new Map([['main-1', [child]]]))

    expect(screen.getByRole('button', { name: 'Expand jim - Main Session delegated sessions' })).toBeInTheDocument()
    expect(screen.getByTestId('session-title-main-1')).toHaveTextContent('jim - Main Session')
    expect(screen.queryByRole('button', { name: /null/ })).not.toBeInTheDocument()
  })

  it('an empty-string group name is unresolved — the id stands in everywhere', () => {
    const main = sessionFixture({ id: 'main-1', child_count: 1 })
    const child = sessionFixture({ id: 'child-1', type: 'delegate', parent_session_id: 'main-1' })
    renderList([main], new Map([['main-1', [child]]]), '')

    expect(screen.getByRole('button', { name: 'Expand jim - Main Session delegated sessions' })).toBeInTheDocument()
    expect(screen.getByTestId('session-title-main-1')).toHaveTextContent('jim - Main Session')
  })

  it('an untitled NON-main row with an unresolved name keeps "Untitled session" (unchanged titling)', () => {
    const chat = sessionFixture({ id: 'chat-1', type: 'chat' })
    renderList([chat], new Map(), '')

    expect(screen.getByTestId('session-title-chat-1')).toHaveTextContent('Untitled session')
    expect(screen.queryByText(/null/)).not.toBeInTheDocument()
  })
})
