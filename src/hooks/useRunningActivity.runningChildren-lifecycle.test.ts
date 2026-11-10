// useRunningActivity.runningChildren-lifecycle.test.ts — ADR-091 WP-E
// cross-family review finding 20
// (src/hooks/useRunningActivity.ts::useRunningActivity).
//
// What was wrong: membership in `runningChildren` (what the Activity Bar
// pill/avatar-stack reads — FR-E-005) used only `span.status === 'running'`,
// the PARENT's own "is this span still open" flag (set at subagent_start,
// cleared at subagent_end). That is a different axis from `lifecycleState`,
// the ADR-053 six-state domain a `subagent_state` frame reduces onto the
// span. A span is 'running' (status) from the moment subagent_start fires —
// including a QUEUED launch (the spec: "a queued launch emits
// subagent_start immediately, before the child actually starts executing")
// — and STAYS 'running' (status) until subagent_end, even after its
// lifecycleState has already reached a terminal value like 'completed' or
// 'failed' via an EARLIER subagent_state frame (no ordering guarantee that
// subagent_end always arrives after/with the final subagent_state). Both
// cases inflated the pill.
//
// Fix: the pill (and the Activity Bar's avatar stack) is derived from
// direct agent spans whose `lifecycleState` is exactly 'running' — the
// ActivityPanel must still SHOW queued/waiting children, but U2 and
// ADR-20260928 D2 (Stop is "non-terminal") supersede the old assertion
// that lifecycle-stopped/completed spans belong to Running now.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'
import type { Agent, ToolCall } from '@/lib/api'
import { makeAgent } from '@/test/factories'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn() }
})

import { fetchAgents } from '@/lib/api'
import { useRunningActivity } from './useRunningActivity'

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

const AGENTS: Agent[] = [
  makeAgent({ id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active' }),
]

function makeAssistantMessage(spans: SubagentSpan[]): ChatMessage {
  return {
    id: 'msg_1',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    spans,
  } as ChatMessage
}

function runningSpan(overrides: Partial<SubagentSpan> = {}): SubagentSpan {
  return {
    spanId: 's1',
    parentCallId: 'c1',
    taskLabel: 'digging into logs',
    agentId: 'ray',
    status: 'running',
    ...overrides,
  } as SubagentSpan
}

function makeBashDispatchCall(overrides: Partial<ToolCall & { call_id: string }> = {}): ToolCall & { call_id: string } {
  return {
    id: overrides.id ?? 'call_bash_1',
    call_id: overrides.call_id ?? overrides.id ?? 'call_bash_1',
    tool: 'bash',
    params: { command: 'sleep 30', run_in_background: true, ...overrides.params },
    status: overrides.status ?? 'success',
    result: overrides.result ?? JSON.stringify({ sessionId: 'bash-running-1', status: 'running' }),
  } as ToolCall & { call_id: string }
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      toolCalls: {},
      toolCallOrder: [],
    })
  })
})

describe('useRunningActivity — runningChildren is exactly-lifecycleState-running (finding 20)', () => {
  it('the mixed dataset: one truly running child, one queued, one lifecycle-terminal-but-span-open child, and one running shell job — runningChildren is 1', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(AGENTS)
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            // Truly running: subagent_start fired AND its first subagent_state
            // already confirmed 'running'.
            runningSpan({ spanId: 'span-running', parentCallId: 'call-running', lifecycleState: 'running' }),
            // Queued: subagent_start fired (status: 'running' — the span is
            // "open") but the child hasn't actually started executing yet.
            runningSpan({ spanId: 'span-queued', parentCallId: 'call-queued', lifecycleState: 'queued' }),
            // Lifecycle-terminal but span still open: a subagent_state
            // announcing 'completed' arrived before the matching
            // subagent_end — status is still 'running' (span.status), but
            // the child is done.
            runningSpan({ spanId: 'span-done-early', parentCallId: 'call-done-early', lifecycleState: 'completed' }),
          ]),
        ],
        toolCalls: {
          call_bash_1: makeBashDispatchCall(),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    // U2: terminal lifecycle is retained as history, not Running now.
    // Queued work remains visible, without contributing to runningCount.
    await waitFor(() => {
      expect(result.current.running.map((item) => item.key)).toStrictEqual(['span-running', 'span-queued', 'bash-running-1'])
    })
    expect(result.current.recentlyFinished.map((item) => ({ key: item.key, status: item.status }))).toStrictEqual([{ key: 'span-done-early', status: 'success' }])
    expect(result.current.runningCount).toBe(2)
    expect(result.current.runningChildren).toBe(1)
    client.clear()
  })

  // Re-pinned to the settled rule (spec DEL-F27, commit a5789fb5c — UF2): an
  // open `subagent_start` bracket with no `lifecycleState` yet maps to
  // 'parked' — pending, not executing — and the guessed `span.status`
  // fallback is gone (the old assertion here had the span in `running`;
  // uf2_running-activity-attribution.test.ts pins the opposite). The span is
  // therefore not running activity at all: absent from `running` (and so
  // from runningChildren), retained among recentlyFinished until a
  // `subagent_state` frame arrives.
  it('a span with no lifecycleState yet (subagent_start received, no subagent_state yet) does not count toward runningChildren', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(AGENTS)
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ spanId: 'span-no-state-yet', parentCallId: 'call-no-state-yet' }),
          ]),
        ],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.recentlyFinished.map((item) => ({ key: item.key, status: item.status }))).toStrictEqual([
        { key: 'span-no-state-yet', status: 'parked' },
      ])
    })
    expect(result.current.running).toHaveLength(0)
    expect(result.current.runningChildren).toBe(0)
    client.clear()
  })

  it('a lifecycleState of running counts even when other lifecycle states are mixed across siblings', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(AGENTS)
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ spanId: 'span-r1', parentCallId: 'call-r1', lifecycleState: 'running' }),
            runningSpan({ spanId: 'span-r2', parentCallId: 'call-r2', lifecycleState: 'running' }),
            runningSpan({ spanId: 'span-needs-input', parentCallId: 'call-needs-input', lifecycleState: 'needs_input' }),
            runningSpan({ spanId: 'span-stopped', parentCallId: 'call-stopped', lifecycleState: 'stopped' }),
          ]),
        ],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running.map((item) => item.key)).toStrictEqual(['span-r1', 'span-r2', 'span-needs-input'])
    })
    expect(result.current.recentlyFinished.map((item) => ({ key: item.key, lifecycle: item.kind === 'agent' ? item.lifecycleState : undefined }))).toStrictEqual([{ key: 'span-stopped', lifecycle: 'stopped' }])
    expect(result.current.runningCount).toBe(2)
    expect(result.current.runningChildren).toBe(2)
    client.clear()
  })
})
