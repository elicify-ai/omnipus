// SessionOverviewList main-title tests (founder UI feedback 2026-10-11,
// point 4). The overview list shows a session's title in two places: the
// expand-toggle labels ("Expand <title> delegated sessions") and the row
// itself. Both must read "<Agent> - Main Session" for an untitled main —
// derived from the agent's CURRENT display name (rename flows through a
// rerender) — and never "Untitled session". Non-main sessions keep the
// existing fallback.
//
// Harness: real SessionOverviewList over a plain fixtures map, wrapped in a
// QueryClientProvider (SessionTree's children loader uses useQueries).

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

describe('SessionOverviewList — main session display title', () => {
  it('derives "<Agent> - Main Session" in the expand-toggle labels of an untitled main', () => {
    const main = sessionFixture({ id: 'main-1', child_count: 1 })
    const child = sessionFixture({ id: 'child-1', type: 'delegate', parent_session_id: 'main-1' })
    const view = renderList([main], new Map([['main-1', [child]]]), 'Jim')

    // Collapsed: the toggle's accessible name is the expand label.
    expect(screen.getByRole('button', { name: 'Expand Jim - Main Session delegated sessions' })).toBeInTheDocument()

    // Expanded (rerender with the main in expandedIds): the collapse label
    // takes over — same derived title in both templates.
    view.rerender(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SessionOverviewList
          {...listProps([main], new Map([['main-1', [child]]]))}
          agentName="Jim"
          expandedIds={new Set(['main-1'])}
        />
      </QueryClientProvider>,
    )
    expect(
      screen.getByRole('button', { name: 'Collapse Jim - Main Session delegated sessions' }),
    ).toBeInTheDocument()
  })

  it('derives the title on the row itself (agentName threaded through to SessionRow)', () => {
    const main = sessionFixture({ id: 'main-1' })
    renderList([main], new Map(), 'Jim')

    expect(screen.getByTestId('session-title-main-1')).toHaveTextContent('Jim - Main Session')
  })

  it('keeps "Untitled session" for an untitled NON-main row and its labels (unchanged titling)', () => {
    const chat = sessionFixture({ id: 'chat-1', type: 'chat', child_count: 1 })
    const child = sessionFixture({ id: 'child-1', type: 'delegate', parent_session_id: 'chat-1' })
    renderList([chat], new Map([['chat-1', [child]]]), 'Jim')

    expect(screen.getByRole('button', { name: 'Expand Untitled session delegated sessions' })).toBeInTheDocument()
    expect(screen.getByTestId('session-title-chat-1')).toHaveTextContent('Untitled session')
  })

  it('follows an agent rename: rerendering with the new display name updates labels and row', () => {
    const main = sessionFixture({ id: 'main-1', child_count: 1 })
    const child = sessionFixture({ id: 'child-1', type: 'delegate', parent_session_id: 'main-1' })
    const props = listProps([main], new Map([['main-1', [child]]]))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(
      <QueryClientProvider client={client}>
        <SessionOverviewList {...props} agentName="Jim" />
      </QueryClientProvider>,
    )
    expect(screen.getByTestId('session-title-main-1')).toHaveTextContent('Jim - Main Session')

    view.rerender(
      <QueryClientProvider client={client}>
        <SessionOverviewList {...props} agentName="Jim Prime" />
      </QueryClientProvider>,
    )
    expect(screen.getByTestId('session-title-main-1')).toHaveTextContent('Jim Prime - Main Session')
    expect(
      screen.getByRole('button', { name: 'Expand Jim Prime - Main Session delegated sessions' }),
    ).toBeInTheDocument()
  })
})
