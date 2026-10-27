// #1266 x train 3: checklist recovery over PAGED history. Train 3 read the
// whole transcript in one call; #1266 made GET /sessions/{id}/messages return
// one bounded page. Recovery must page backwards with `before` until the
// current turn's start, bounded by TURN_HISTORY_PAGE_CAP, and show NO
// checklist (never an older, possibly stale one) when the cap is hit.
// Real public hook, real chat/session stores, production query client; only
// the history read is replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { fetchSessionMessages } from '@/lib/api'
import type { ToolCall } from '@/lib/api'
import type { SessionHistoryEntry, SessionHistoryPage } from '@/lib/api/history-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { TURN_HISTORY_PAGE_CAP, useCurrentTodo } from './useCurrentTodo'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessionMessages: vi.fn(),
}))

const SID = 'paged-checklist-session'
const TURN = 'long-turn'
const EARLIER_TURN = 'earlier-turn'
// Expected request size, from the hook's documented bound (100 groups/page).
const PAGE_LIMIT = 100

// Opaque position per the contract pattern ^hp1_[A-Za-z0-9_-]{43}$.
function position(label: string): string {
  return `hp1_${label.replace(/[^A-Za-z0-9_-]/g, '_').padEnd(43, 'x').slice(0, 43)}`
}

function setTodos(id: string, activeForm: string): ToolCall {
  return {
    id, tool: 'set_todos', status: 'success',
    params: { outcome: 'Paged recovery proof', todos: [{ text: 'Write the report', status: 'in_progress', active_form: activeForm }] },
  }
}

function assistant(id: string, turnId: string, toolCalls: ToolCall[] = []): SessionHistoryEntry {
  return {
    id, role: 'assistant', content: '', timestamp: '2026-10-24T08:00:00Z', status: 'done',
    turnId, tool_calls: toolCalls, historyPosition: position(id),
  }
}

function busyEntries(prefix: string, count: number): SessionHistoryEntry[] {
  return Array.from({ length: count }, (_, index) =>
    assistant(`${prefix}-${index}`, TURN, [{ id: `${prefix}-bash-${index}`, tool: 'bash', status: 'success', params: { command: 'true' } }]))
}

// not-wire-format: test-owned page builder over the SPA-normalized page type.
function historyPage(entries: SessionHistoryEntry[], earlier: boolean): SessionHistoryPage {
  const first = entries[0]?.historyPosition
  const last = entries.at(-1)?.historyPosition
  return {
    session_id: SID, entries, has_earlier: earlier, has_later: false,
    ...(earlier && first ? { next_before: first } : {}),
    page_info: {
      mode: 'latest', limit: PAGE_LIMIT, min_rows: 20, row_count: entries.length, source_group_count: entries.length,
      soft_byte_budget: 4194304, projected_bytes: 0, byte_budget_exceeded: false,
      stop_reason: earlier ? 'row_target' : 'end', retention_generation: 'fixture-retained-1',
      context: { spans: [], duplicate_ids: [] },
      ...(first && last ? { first_position: first, last_position: last } : {}),
    },
  }
}

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

const read = vi.mocked(fetchSessionMessages)

function settled() {
  return waitFor(() => expect(queryClient.getQueryState(['current-todo-history', SID, TURN])?.status).toBe('success'))
}

beforeEach(() => {
  __resetFinishedTurnIdsForTests()
  queryClient.clear()
  read.mockReset()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: 'mia' }, true)
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), isConnected: true }, true)
  act(() => {
    // A running turn with no live checklist in the bucket: only saved history can supply it.
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, message_id: 'live-answer', turn_id: TURN, content: 'Still working' })
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: SID, user_id: 'operator', pending_approvals: [], emitted_at: '2026-10-24T08:00:00Z',
      active_turn: { turn_id: TURN, agent_id: 'mia', started_at: '2026-10-24T07:00:00Z' },
    })
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.restoreAllMocks()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
})

describe('useCurrentTodo recovers the current turn checklist across history pages', () => {
  it('pages back with before to a checklist written before the latest page in a long turn', async () => {
    const latest = historyPage(busyEntries('latest', 100), true)
    const older = historyPage([
      assistant('earlier-turn-reply', EARLIER_TURN),
      assistant('turn-start-reply', TURN, [setTodos('todo-write', 'Drafting the summary')]),
      ...busyEntries('older', 40),
    ], true)
    read.mockImplementation(async (_sessionId, options) => (options?.before === undefined ? latest : older))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const { result } = renderHook(() => useCurrentTodo(true), { wrapper: Wrapper })

    await waitFor(() => expect(result.current).toEqual({ activeForm: 'Drafting the summary' }))
    expect(read.mock.calls).toEqual([
      [SID, { limit: PAGE_LIMIT }],
      [SID, { limit: PAGE_LIMIT, before: latest.next_before }],
    ])
    expect(warn).not.toHaveBeenCalled()
  })

  it('stops at the start of the current turn and shows no checklist without reading further', async () => {
    const latest = historyPage([assistant('earlier-turn-reply', EARLIER_TURN), ...busyEntries('latest', 10)], true)
    read.mockResolvedValue(latest)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const { result } = renderHook(() => useCurrentTodo(true), { wrapper: Wrapper })

    await settled()
    expect(result.current).toEqual({ activeForm: null })
    expect(read).toHaveBeenCalledTimes(1)
    expect(warn).not.toHaveBeenCalled()
  })

  it('takes the newest write from the latest page and never reads an older page', async () => {
    const latest = historyPage([
      assistant('first-write', TURN, [setTodos('todo-old', 'Collecting sources')]),
      assistant('second-write', TURN, [setTodos('todo-new', 'Writing tests')]),
    ], true)
    read.mockResolvedValue(latest)

    const { result } = renderHook(() => useCurrentTodo(true), { wrapper: Wrapper })

    await waitFor(() => expect(result.current).toEqual({ activeForm: 'Writing tests' }))
    expect(read).toHaveBeenCalledTimes(1)
  })

  it('shows no checklist and reports the cap instead of reading past it', async () => {
    expect(TURN_HISTORY_PAGE_CAP).toBe(10)
    // Every page within the cap is this turn's tool work; the only checklist
    // write sits one page past the cap and must never be shown.
    read.mockImplementation(async () => {
      const index = read.mock.calls.length
      return index > TURN_HISTORY_PAGE_CAP
        ? historyPage([assistant('beyond-cap', TURN, [setTodos('todo-beyond', 'Stale form')])], false)
        : historyPage(busyEntries(`page-${index}`, 100), true)
    })
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const { result } = renderHook(() => useCurrentTodo(true), { wrapper: Wrapper })

    await settled()
    expect(result.current).toEqual({ activeForm: null })
    expect(read).toHaveBeenCalledTimes(TURN_HISTORY_PAGE_CAP)
    await waitFor(() => expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_history_capped', {
      sessionId: SID, turnId: TURN, pageCap: TURN_HISTORY_PAGE_CAP,
    }))
  })
})
