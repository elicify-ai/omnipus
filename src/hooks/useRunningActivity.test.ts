// useRunningActivity — Activity Bar data hook tests.
//
// Covers: zero running items, one running native-agent span, one running
// 3rd-party-agent span (agentType: '3p'), a running background bash item,
// an unmatched agentId (must resolve to 'unknown' without throwing), and a
// terminal span landing in recentlyFinished (not running).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'
import type { Agent, ToolCall } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn() }
})

import { fetchAgents } from '@/lib/api'
import { useRunningActivity } from './useRunningActivity'

// ── Wrapper (mirrors src/hooks/restart.test.ts's QueryClientProvider pattern) ──

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

const AGENTS: Agent[] = [
  { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active', color: '#4488ff', icon: 'compass' } as Agent,
  { id: 'ext-1', name: 'ClaudeCode', type: 'subagent_3p', locked: false, status: 'active' } as Agent,
]

function makeAssistantMessage(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: overrides.id ?? 'msg_1',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    ...overrides,
  } as ChatMessage
}

function makeSpan(overrides: Partial<SubagentSpan> & { status: SubagentSpan['status'] }): SubagentSpan {
  const base = {
    spanId: overrides.spanId ?? 'span_1',
    parentCallId: overrides.parentCallId ?? 'call_1',
    taskLabel: overrides.taskLabel ?? 'audit files',
    steps: overrides.steps ?? [],
    agentId: overrides.agentId,
  }
  if (overrides.status === 'running') {
    return { ...base, status: 'running' }
  }
  return {
    ...base,
    status: overrides.status,
    durationMs: (overrides as { durationMs?: number }).durationMs ?? 1234,
  }
}

function makeBashToolCall(overrides: Partial<ToolCall & { call_id: string }> = {}): ToolCall & { call_id: string } {
  return {
    id: overrides.id ?? 'call_bash_1',
    call_id: overrides.call_id ?? overrides.id ?? 'call_bash_1',
    tool: 'bash',
    params: { command: 'npm test', run_in_background: true, ...overrides.params },
    status: overrides.status ?? 'running',
    duration_ms: overrides.duration_ms,
  }
}

/**
 * A background-session dispatch call (`run_in_background: true`). This is
 * the crux of the bug being fixed: the CALL's own `status` is `'success'`
 * almost immediately (dispatching to the background is a fast, synchronous
 * operation), while the session itself is still running — the real
 * liveness signal is the `result` payload, a JSON-encoded
 * `pkg/tools/types.go` `ExecResponse` whose session-id field is `sessionId`
 * (camelCase — verified against the Go struct tag, see the doc comment on
 * `ParsedBashSessionResult` in useRunningActivity.ts).
 */
function makeBashDispatchCall(overrides: Partial<ToolCall & { call_id: string }> = {}): ToolCall & { call_id: string } {
  // Uses `'result' in overrides` (not `overrides.result ?? default`) so a
  // test can explicitly pass `result: undefined` to simulate a genuinely
  // missing result field — nullish-coalescing would otherwise silently fall
  // through to the default JSON and defeat that test case.
  const hasResultOverride = Object.prototype.hasOwnProperty.call(overrides, 'result')
  return {
    id: overrides.id ?? 'call_bash_1',
    call_id: overrides.call_id ?? overrides.id ?? 'call_bash_1',
    tool: 'bash',
    params: { command: 'sleep 15 && echo done', run_in_background: true, ...overrides.params },
    status: overrides.status ?? 'success',
    result: hasResultOverride ? overrides.result : JSON.stringify({ sessionId: 'abc123', status: 'running' }),
    duration_ms: overrides.duration_ms,
  }
}

/** A poll/read/kill call against an already-dispatched background session. */
function makeBashSessionActionCall(
  action: 'poll' | 'read' | 'kill',
  overrides: Partial<ToolCall & { call_id: string }> = {},
): ToolCall & { call_id: string } {
  const hasResultOverride = Object.prototype.hasOwnProperty.call(overrides, 'result')
  return {
    id: overrides.id ?? `call_${action}_1`,
    call_id: overrides.call_id ?? overrides.id ?? `call_${action}_1`,
    tool: 'bash',
    params: { action, session_id: 'abc123', ...overrides.params },
    status: overrides.status ?? 'success',
    result: hasResultOverride ? overrides.result : JSON.stringify({ sessionId: 'abc123', status: 'running' }),
    duration_ms: overrides.duration_ms,
  }
}

/** A `delegate` tool call — the originating call a SubagentSpan's parentCallId points at. */
function makeDelegateToolCall(overrides: Partial<ToolCall & { call_id: string }> = {}): ToolCall & { call_id: string } {
  return {
    id: overrides.id ?? 'call_1',
    call_id: overrides.call_id ?? overrides.id ?? 'call_1',
    tool: 'delegate',
    params: { ...overrides.params },
    status: overrides.status ?? 'running',
    duration_ms: overrides.duration_ms,
  }
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset()
  vi.mocked(fetchAgents).mockResolvedValue(AGENTS)
  act(() => {
    useChatStore.setState({ messages: [], toolCalls: {} })
  })
})

afterEach(() => {
  vi.resetAllMocks()
})

describe('useRunningActivity — empty state', () => {
  it('returns zero running items and empty arrays when nothing is active', async () => {
    const client = makeClient()
    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(fetchAgents).toHaveBeenCalled()
    })

    expect(result.current.runningCount).toBe(0)
    expect(result.current.running).toEqual([])
    expect(result.current.recentlyFinished).toEqual([])
    client.clear()
  })
})

describe('useRunningActivity — native agent span', () => {
  it('classifies a running span whose agentId matches a native agent as agentType native', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [makeSpan({ spanId: 'span_native', status: 'running', agentId: 'ray' })],
          }),
        ],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    await waitFor(() => {
      const item = result.current.running[0]
      expect(item.kind).toBe('agent')
      if (item.kind === 'agent') {
        expect(item.agentType).toBe('native')
        expect(item.agentName).toBe('Ray')
      }
    })
    expect(result.current.runningCount).toBe(1)
    expect(result.current.recentlyFinished).toEqual([])
    client.clear()
  })
})

describe('useRunningActivity — 3rd-party agent span', () => {
  it('classifies a running span whose agentId matches a subagent_3p agent as agentType 3p', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [makeSpan({ spanId: 'span_3p', status: 'running', agentId: 'ext-1' })],
          }),
        ],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    await waitFor(() => {
      const item = result.current.running[0]
      expect(item.kind).toBe('agent')
      if (item.kind === 'agent') {
        expect(item.agentType).toBe('3p')
        expect(item.steps).toEqual([])
      }
    })
    client.clear()
  })
})

describe('useRunningActivity — background bash item', () => {
  // The exact bug scenario, now fixed: a dispatch call whose OWN status is
  // already 'success' (dispatching to the background is fast/synchronous)
  // but whose `result` payload says the session itself is still running.
  // The old logic keyed off `tc.status === 'running'` and NEVER caught this
  // — it always showed "No active background work". Session-id correlation
  // (reading `result.sessionId`) is what fixes it.
  it('surfaces a bash session as running when the dispatch call\'s own status is already success but its result says the session is running', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_bash_1: makeBashDispatchCall({
            status: 'success',
            params: { command: 'sleep 15 && echo done', run_in_background: true },
            result: JSON.stringify({ sessionId: 'abc123', status: 'running' }),
          }),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    const item = result.current.running[0]
    expect(item.kind).toBe('bash')
    if (item.kind === 'bash') {
      expect(item.command).toBe('sleep 15 && echo done')
      expect(item.status).toBe('running')
    }
    expect(result.current.runningCount).toBe(1)
    client.clear()
  })

  it('does not surface a foreground (non-background) bash call', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_fg: makeBashToolCall({ id: 'call_fg', params: { command: 'ls', run_in_background: false }, status: 'running' }),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(fetchAgents).toHaveBeenCalled()
    })
    expect(result.current.running).toEqual([])
    expect(result.current.runningCount).toBe(0)
    client.clear()
  })
})

// ── Background bash session-id correlation (the Activity Bar bug fix) ──────
//
// Root cause: `tc.status` reflects whether the tool CALL itself completed,
// which for `run_in_background: true` happens almost instantly regardless of
// whether the spawned session is still alive. The real liveness signal is
// the session's own status, carried in each call's `result` JSON payload
// (`{sessionId, status, exitCode?, output?}`) and correlated across the
// dispatch call plus any later poll/read/kill calls by that shared
// `sessionId`.

describe('useRunningActivity — bash session-id correlation', () => {
  it('full lifecycle: dispatch (running) -> poll (still running) -> poll (done) ends with 1 finished, 0 running, command preserved from dispatch', async () => {
    const client = makeClient()

    // Step 1: dispatch only.
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_bash_1: makeBashDispatchCall({
            params: { command: 'sleep 15 && echo done', run_in_background: true },
            result: JSON.stringify({ sessionId: 'abc123', status: 'running' }),
          }),
        },
      })
    })
    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    expect(result.current.recentlyFinished).toHaveLength(0)

    // Step 2: a poll call for the same session, still running.
    act(() => {
      useChatStore.setState((s) => ({
        toolCalls: {
          ...s.toolCalls,
          call_poll_1: makeBashSessionActionCall('poll', {
            result: JSON.stringify({ sessionId: 'abc123', status: 'running' }),
          }),
        },
      }))
    })
    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    expect(result.current.recentlyFinished).toHaveLength(0)

    // Step 3: a second poll call for the same session, now done.
    act(() => {
      useChatStore.setState((s) => ({
        toolCalls: {
          ...s.toolCalls,
          call_poll_2: makeBashSessionActionCall('poll', {
            id: 'call_poll_2',
            call_id: 'call_poll_2',
            result: JSON.stringify({ sessionId: 'abc123', status: 'done', exitCode: 0, output: 'done\n' }),
          }),
        },
      }))
    })
    await waitFor(() => {
      expect(result.current.recentlyFinished).toHaveLength(1)
    })
    expect(result.current.running).toHaveLength(0)
    expect(result.current.runningCount).toBe(0)
    const finished = result.current.recentlyFinished[0]
    expect(finished.kind).toBe('bash')
    if (finished.kind === 'bash') {
      expect(finished.command).toBe('sleep 15 && echo done')
      expect(finished.status).toBe('success')
    }
    client.clear()
  })

  it('silently skips a bash call with a malformed/unparseable result — no crash, not running, not finished', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_bash_bad_1: makeBashDispatchCall({
            id: 'call_bash_bad_1',
            call_id: 'call_bash_bad_1',
            result: 'not json at all',
          }),
          call_bash_bad_2: makeBashDispatchCall({
            id: 'call_bash_bad_2',
            call_id: 'call_bash_bad_2',
            params: { command: 'echo hi', run_in_background: true },
            result: undefined,
          }),
        },
      })
    })

    expect(() =>
      renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) }),
    ).not.toThrow()

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await waitFor(() => {
      expect(fetchAgents).toHaveBeenCalled()
    })
    expect(result.current.running).toEqual([])
    expect(result.current.recentlyFinished).toEqual([])
    expect(result.current.runningCount).toBe(0)
    client.clear()
  })

  it('correctly buckets two concurrent independent sessions — one running, one finished', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_bash_run: makeBashDispatchCall({
            id: 'call_bash_run',
            call_id: 'call_bash_run',
            params: { command: 'tail -f app.log', run_in_background: true },
            result: JSON.stringify({ sessionId: 'session-running', status: 'running' }),
          }),
          call_bash_done: makeBashDispatchCall({
            id: 'call_bash_done',
            call_id: 'call_bash_done',
            params: { command: 'npm run build', run_in_background: true },
            result: JSON.stringify({ sessionId: 'session-done', status: 'running' }),
          }),
          call_poll_done: makeBashSessionActionCall('poll', {
            id: 'call_poll_done',
            call_id: 'call_poll_done',
            params: { action: 'poll', session_id: 'session-done' },
            result: JSON.stringify({ sessionId: 'session-done', status: 'done', exitCode: 0, output: 'built\n' }),
          }),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
      expect(result.current.recentlyFinished).toHaveLength(1)
    })

    const runningItem = result.current.running[0]
    expect(runningItem.kind).toBe('bash')
    if (runningItem.kind === 'bash') {
      expect(runningItem.key).toBe('session-running')
      expect(runningItem.command).toBe('tail -f app.log')
      expect(runningItem.status).toBe('running')
    }

    const finishedItem = result.current.recentlyFinished[0]
    expect(finishedItem.kind).toBe('bash')
    if (finishedItem.kind === 'bash') {
      expect(finishedItem.key).toBe('session-done')
      expect(finishedItem.command).toBe('npm run build')
      expect(finishedItem.status).toBe('success')
    }
    client.clear()
  })

  // Status-mapping decision (documented in useRunningActivity.ts's
  // mapBashSessionStatus doc comment): BashActivityItem only supports
  // 'running'|'success'|'error'|'cancelled', so "timeout" -> 'error'
  // (closest fit: it failed to finish) and "killed" -> 'cancelled'
  // (closest fit: something stopped it).
  it('maps a timed-out session to error and a killed session to cancelled in recentlyFinished', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_bash_timeout: makeBashDispatchCall({
            id: 'call_bash_timeout',
            call_id: 'call_bash_timeout',
            params: { command: 'sleep 999999', run_in_background: true, timeout_seconds: 5 },
            result: JSON.stringify({ sessionId: 'session-timeout', status: 'running' }),
          }),
          call_poll_timeout: makeBashSessionActionCall('poll', {
            id: 'call_poll_timeout',
            call_id: 'call_poll_timeout',
            params: { action: 'poll', session_id: 'session-timeout' },
            result: JSON.stringify({ sessionId: 'session-timeout', status: 'timeout', exitCode: -1 }),
          }),
          call_bash_killed: makeBashDispatchCall({
            id: 'call_bash_killed',
            call_id: 'call_bash_killed',
            params: { command: 'yes > /dev/null', run_in_background: true },
            result: JSON.stringify({ sessionId: 'session-killed', status: 'running' }),
          }),
          call_kill: makeBashSessionActionCall('kill', {
            id: 'call_kill',
            call_id: 'call_kill',
            params: { action: 'kill', session_id: 'session-killed' },
            result: JSON.stringify({ sessionId: 'session-killed', status: 'killed', exitCode: -1 }),
          }),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.recentlyFinished).toHaveLength(2)
    })
    expect(result.current.running).toHaveLength(0)

    const byKey = Object.fromEntries(result.current.recentlyFinished.map((i) => [i.key, i]))
    const timeoutItem = byKey['session-timeout']
    const killedItem = byKey['session-killed']
    expect(timeoutItem?.kind).toBe('bash')
    expect(killedItem?.kind).toBe('bash')
    if (timeoutItem?.kind === 'bash') expect(timeoutItem.status).toBe('error')
    if (killedItem?.kind === 'bash') expect(killedItem.status).toBe('cancelled')
    client.clear()
  })
})

describe('useRunningActivity — bash session tracking survives turn finalization', () => {
  // Regression test for the second bug found via live Playwright verification:
  // chat.ts "bakes" a finished turn's live toolCalls into that message's own
  // `tool_calls` array and clears the flat `toolCalls` map the moment the
  // turn completes (see `draft.toolCalls = {}` sites in chat.ts). A dispatch
  // reported live (still in the flat map) correctly showed as running, but
  // the instant its turn finished (baked into `message.tool_calls`, flat map
  // cleared), the session vanished from the Activity Bar entirely — not
  // moved to recentlyFinished, just gone — even though the real background
  // process kept running server-side. groupBashSessions/resolveSpanAgentId
  // must read BOTH the live flat map and every finalized message's baked
  // tool_calls.
  it('a dispatch+poll pair baked into a finalized message (not the live flat map) still counts as running', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            id: 'msg_finalized',
            status: 'done',
            tool_calls: [
              makeBashDispatchCall({
                params: { command: 'sleep 90 && echo alldone', run_in_background: true },
                result: JSON.stringify({ sessionId: 'baked-session', status: 'running' }),
              }),
              makeBashSessionActionCall('poll', {
                id: 'call_poll_baked',
                call_id: 'call_poll_baked',
                params: { action: 'poll', session_id: 'baked-session' },
                result: JSON.stringify({ sessionId: 'baked-session', status: 'running' }),
              }),
            ],
          }),
        ],
        toolCalls: {}, // the live flat map is empty — this turn has already finished and been baked
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    expect(result.current.recentlyFinished).toHaveLength(0)
    const item = result.current.running[0]
    expect(item.kind).toBe('bash')
    if (item.kind === 'bash') {
      expect(item.command).toBe('sleep 90 && echo alldone')
      expect(item.status).toBe('running')
    }
    client.clear()
  })

  it('a session that finishes in a LATER finalized turn moves to recentlyFinished, not vanishing', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            id: 'msg_finalized_1',
            status: 'done',
            tool_calls: [
              makeBashDispatchCall({
                id: 'call_dispatch_baked',
                call_id: 'call_dispatch_baked',
                params: { command: 'sleep 5 && echo ok', run_in_background: true },
                result: JSON.stringify({ sessionId: 'baked-session-2', status: 'running' }),
              }),
            ],
          }),
          makeAssistantMessage({
            id: 'msg_finalized_2',
            status: 'done',
            tool_calls: [
              makeBashSessionActionCall('poll', {
                id: 'call_poll_baked_2',
                call_id: 'call_poll_baked_2',
                params: { action: 'poll', session_id: 'baked-session-2' },
                result: JSON.stringify({ sessionId: 'baked-session-2', status: 'done', exitCode: 0, output: 'ok\n' }),
              }),
            ],
          }),
        ],
        toolCalls: {},
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.recentlyFinished).toHaveLength(1)
    })
    expect(result.current.running).toHaveLength(0)
    const item = result.current.recentlyFinished[0]
    expect(item.kind).toBe('bash')
    if (item.kind === 'bash') {
      expect(item.command).toBe('sleep 5 && echo ok')
      expect(item.status).toBe('success')
    }
    client.clear()
  })

  it('resolveSpanAgentId falls back correctly when the originating delegate call is baked into a finalized message', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            id: 'msg_delegate_baked',
            status: 'done',
            tool_calls: [makeDelegateToolCall({ id: 'call_delegate_baked', call_id: 'call_delegate_baked', params: { agent_id: 'ray' } })],
            spans: [makeSpan({ status: 'running', parentCallId: 'call_delegate_baked', agentId: 'someone-else' })],
          }),
        ],
        toolCalls: {}, // the delegate call has already been baked out of the live map
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    await waitFor(() => {
      const item = result.current.running[0]
      expect(item.kind).toBe('agent')
      if (item.kind === 'agent') {
        // Must prefer the baked delegate call's agent_id ('ray') over the
        // span's own agentId ('someone-else') — same fix as the live-map
        // case, now proven to also work when the call is baked.
        expect(item.agentName).toBe('Ray')
      }
    })
    client.clear()
  })
})

describe('useRunningActivity — unmatched agentId', () => {
  it('resolves an agentId with no matching agent to agentType unknown without throwing', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [makeSpan({ spanId: 'span_ghost', status: 'running', agentId: 'ghost-agent' })],
          }),
        ],
      })
    })

    expect(() => renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })).not.toThrow()

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    const item = result.current.running[0]
    expect(item.kind).toBe('agent')
    if (item.kind === 'agent') {
      expect(item.agentType).toBe('unknown')
    }
    client.clear()
  })
})

describe('useRunningActivity — terminal span', () => {
  it('places a terminal (success) span in recentlyFinished, not running', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [makeSpan({ spanId: 'span_done', status: 'success', agentId: 'ray', durationMs: 4200 })],
          }),
        ],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.recentlyFinished).toHaveLength(1)
    })
    expect(result.current.running).toEqual([])
    expect(result.current.runningCount).toBe(0)
    const item = result.current.recentlyFinished[0]
    expect(item.kind).toBe('agent')
    if (item.kind === 'agent') {
      expect(item.status).toBe('success')
      expect(item.durationMs).toBe(4200)
    }
    client.clear()
  })
})

// ── Native named-target delegation misattribution fix ───────────────────────
//
// Regression coverage for the bug described in useRunningActivity.ts's
// resolveSpanAgentId doc comment: for native delegation to a named target
// agent, the backend's subagent_start/subagent_end frame carries the
// PARENT's agent_id, not the target's. The hook must prefer the originating
// `delegate` tool call's own `params.agent_id` when present.

describe('useRunningActivity — agent identity resolution (native named-target delegation)', () => {
  it('prefers the originating delegate call\'s agent_id over a differing span.agentId', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [
              makeSpan({
                spanId: 'span_mismatch',
                status: 'running',
                // Backend limitation: this is the PARENT's id, not the delegate's.
                agentId: 'jim-parent',
                parentCallId: 'call_delegate_1',
              }),
            ],
          }),
        ],
        toolCalls: {
          call_delegate_1: makeDelegateToolCall({
            id: 'call_delegate_1',
            params: { agent_id: 'ray' },
          }),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    // A second waitFor: the first render (before the ['agents'] query
    // resolves) has agentName === agentId as a placeholder — wait for the
    // agents list to resolve and the name to be looked up for real.
    await waitFor(() => {
      const item = result.current.running[0]
      expect(item.kind).toBe('agent')
      if (item.kind === 'agent') {
        // Resolved via the delegate call's agent_id ('ray'), NOT span.agentId ('jim-parent').
        expect(item.agentId).toBe('ray')
        expect(item.agentName).toBe('Ray')
        expect(item.agentType).toBe('native')
      }
    })
    client.clear()
  })

  it('falls back to span.agentId without crashing when the originating call is not found (scrolled out)', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [
              makeSpan({
                spanId: 'span_scrolled',
                status: 'running',
                agentId: 'ray',
                parentCallId: 'call_gone', // not present in toolCalls at all
              }),
            ],
          }),
        ],
        toolCalls: {},
      })
    })

    expect(() =>
      renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) }),
    ).not.toThrow()

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    await waitFor(() => {
      const item = result.current.running[0]
      expect(item.kind).toBe('agent')
      if (item.kind === 'agent') {
        expect(item.agentId).toBe('ray')
        expect(item.agentName).toBe('Ray')
      }
    })
    client.clear()
  })

  it('uses span.agentId (the parent\'s own id) when the originating call has no agent_id param (untargeted delegation)', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [
              makeSpan({
                spanId: 'span_untargeted',
                status: 'running',
                agentId: 'ray', // untargeted delegation: span.agentId IS the parent's own id, and is correct here.
                parentCallId: 'call_delegate_untargeted',
              }),
            ],
          }),
        ],
        toolCalls: {
          call_delegate_untargeted: makeDelegateToolCall({
            id: 'call_delegate_untargeted',
            params: {}, // no agent_id — untargeted delegate() call
          }),
        },
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(1)
    })
    await waitFor(() => {
      const item = result.current.running[0]
      expect(item.kind).toBe('agent')
      if (item.kind === 'agent') {
        expect(item.agentId).toBe('ray')
        expect(item.agentName).toBe('Ray')
      }
    })
    client.clear()
  })
})

// ── Cap ownership: running is uncapped, recentlyFinished is capped ──────────
//
// ActivityBar.tsx owns MAX_STACK_AVATARS=4 for the avatar STACK display —
// that's a presentation concern, not this hook's. The hook itself must
// return every running item uncapped; only the finished-history retention
// (RECENTLY_FINISHED_CAP) is the hook's own concern.

describe('useRunningActivity — running is uncapped, recentlyFinished is capped', () => {
  it('returns all running items uncapped, even with more than 4 (ActivityBar owns the display cap)', async () => {
    const client = makeClient()
    const spans = Array.from({ length: 6 }, (_, i) =>
      makeSpan({
        spanId: `span_run_${i}`,
        status: 'running',
        agentId: 'ray',
        parentCallId: `call_run_${i}`,
      }),
    )
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage({ spans })],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.running).toHaveLength(6)
    })
    expect(result.current.runningCount).toBe(6)
    client.clear()
  })

  it('caps recentlyFinished at 8, ordered most-recent-first', async () => {
    const client = makeClient()
    const spans = Array.from({ length: 10 }, (_, i) =>
      makeSpan({
        spanId: `span_fin_${i}`,
        status: 'success',
        agentId: 'ray',
        durationMs: 100 + i,
        parentCallId: `call_fin_${i}`,
      }),
    )
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage({ spans })],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.recentlyFinished.length).toBeGreaterThan(0)
    })
    // RECENTLY_FINISHED_CAP in useRunningActivity.ts is 8.
    expect(result.current.recentlyFinished).toHaveLength(8)
    // Most-recent-first: the last-seeded span (span_fin_9) leads; the two
    // oldest (span_fin_0, span_fin_1) are dropped by the cap.
    expect(result.current.recentlyFinished[0].key).toBe('span_fin_9')
    expect(result.current.recentlyFinished[7].key).toBe('span_fin_2')
    client.clear()
  })
})

// ── Elapsed-time ticking ──────────────────────────────────────────────────
//
// Fake timers + waitFor don't mix well (see useCliPathValidation.test.ts's
// advanceAndFlush comment) — this suite drains microtasks directly instead
// of using waitFor after each timer advance.

describe('useRunningActivity — elapsed time ticking', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  async function flush() {
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  it('grows a running item\'s durationMs across multiple tick advances, and resets first-seen tracking once the item leaves the running set', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [
              makeSpan({ spanId: 'span_tick', status: 'running', agentId: 'ray', parentCallId: 'call_tick' }),
            ],
          }),
        ],
      })
    })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await flush()

    act(() => {
      vi.advanceTimersByTime(1000)
    })
    await flush()
    const afterFirstTick = result.current.running.find((i) => i.key === 'span_tick')
    expect(afterFirstTick).toBeDefined()
    const durationAfterFirstTick = afterFirstTick!.durationMs ?? 0
    expect(durationAfterFirstTick).toBeGreaterThanOrEqual(1000)

    act(() => {
      vi.advanceTimersByTime(2000)
    })
    await flush()
    const afterSecondTick = result.current.running.find((i) => i.key === 'span_tick')
    expect(afterSecondTick).toBeDefined()
    const durationAfterSecondTick = afterSecondTick!.durationMs ?? 0
    // Not just re-asserting once — confirm real growth between the two advances.
    expect(durationAfterSecondTick).toBeGreaterThan(durationAfterFirstTick)
    expect(durationAfterSecondTick).toBeGreaterThanOrEqual(3000)

    // Now finish the span. It must leave `running`, land in `recentlyFinished`
    // with the terminal durationMs, and its first-seen stamp must be cleared.
    act(() => {
      useChatStore.setState((s) => ({
        messages: s.messages.map((m) => ({
          ...m,
          spans: (m.spans ?? []).map((sp) =>
            sp.spanId === 'span_tick' ? { ...sp, status: 'success' as const, durationMs: 9999 } : sp,
          ),
        })),
      }))
    })
    await flush()

    expect(result.current.running.find((i) => i.key === 'span_tick')).toBeUndefined()
    const finished = result.current.recentlyFinished.find((i) => i.key === 'span_tick')
    expect(finished).toBeDefined()
    expect(finished!.durationMs).toBe(9999)

    // Advance time while finished — must not affect the (now-fixed) terminal duration.
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    await flush()
    expect(result.current.recentlyFinished.find((i) => i.key === 'span_tick')!.durationMs).toBe(9999)

    // Contrived re-run of the same spanId as running again: proves the
    // first-seen stamp was actually cleared (not left stale) when the item
    // left the running set — if it hadn't been cleared, this fresh running
    // duration would either continue from ~3000ms+ or blow up from the
    // stale timestamp instead of starting near zero.
    act(() => {
      useChatStore.setState((s) => ({
        messages: s.messages.map((m) => ({
          ...m,
          spans: (m.spans ?? []).map((sp) =>
            sp.spanId === 'span_tick' ? { spanId: sp.spanId, parentCallId: sp.parentCallId, taskLabel: sp.taskLabel, steps: sp.steps, agentId: sp.agentId, status: 'running' as const } : sp,
          ),
        })),
      }))
    })
    await flush()
    act(() => {
      vi.advanceTimersByTime(500)
    })
    await flush()
    const restarted = result.current.running.find((i) => i.key === 'span_tick')
    expect(restarted).toBeDefined()
    expect(restarted!.durationMs ?? 0).toBeLessThan(1000)

    client.clear()
  })
})

// ── Elapsed-time survives unmount+remount (route navigation regression) ────
//
// Regression coverage for the bug fixed in this branch: firstSeenAtRef used
// to be a per-mount useRef, scoped to ActivityBar's mount lifetime. Since
// ActivityBar mounts inside ChatScreen — a different TanStack Router route
// than Settings — navigating Chat -> Settings -> Chat unmounts and remounts
// ChatScreen/ActivityBar, which wiped the ref and reset a still-running
// task's displayed elapsed time toward 0 even though the real task kept
// running. The fix hoists the stamp tracking to a module-level singleton
// Map so it survives the hook instance being unmounted and a fresh one
// mounted later in the same page load.

describe('useRunningActivity — elapsed time survives hook unmount+remount (route navigation)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  async function flush() {
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  it('keeps accumulating the same running item\'s elapsed time across an unmount+remount instead of resetting toward 0', async () => {
    const client = makeClient()
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage({
            spans: [
              makeSpan({
                spanId: 'span_route_nav',
                status: 'running',
                agentId: 'ray',
                parentCallId: 'call_route_nav',
              }),
            ],
          }),
        ],
      })
    })

    // First "mount" — e.g. the user is on the Chat screen with ActivityBar showing.
    const first = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await flush()

    act(() => {
      vi.advanceTimersByTime(3000)
    })
    await flush()

    const beforeUnmount = first.result.current.running.find((i) => i.key === 'span_route_nav')
    expect(beforeUnmount).toBeDefined()
    const durationBeforeUnmount = beforeUnmount!.durationMs ?? 0
    expect(durationBeforeUnmount).toBeGreaterThanOrEqual(3000)

    // Simulate navigating away to Settings: ChatScreen/ActivityBar (and this
    // hook) unmount entirely.
    first.unmount()

    // Time passes while Settings is showing and no ActivityBar is mounted.
    act(() => {
      vi.advanceTimersByTime(2000)
    })

    // Simulate navigating back to Chat: a brand-new hook instance mounts.
    // With the old per-mount useRef, this would stamp a fresh "first seen"
    // and report ~0ms elapsed for the still-running span. With the
    // module-level map, it must pick up where the original stamp left off.
    const second = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(client) })
    await flush()

    const afterRemount = second.result.current.running.find((i) => i.key === 'span_route_nav')
    expect(afterRemount).toBeDefined()
    const durationAfterRemount = afterRemount!.durationMs ?? 0
    // Real elapsed since the original first-seen stamp is now >= 3000ms
    // (first window) + 2000ms (unmounted window) = 5000ms. A regression
    // back to a per-mount ref would show this near 0 instead.
    expect(durationAfterRemount).toBeGreaterThanOrEqual(5000)

    client.clear()
  })
})
