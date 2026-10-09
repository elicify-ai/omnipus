// RED pack — WC-FIX RED-F, unit UF2 (DEL-F26, DEL-F27).
//
// Spec source: docs/internal/specs/session-core-spec.md — DEL-F26/DEL-F27 and
// BDD-12.2 ("Current message/tool/lifecycle … visible and truly owned using
// canonical producer IDs, no fallback/latest guess").
//
//   DEL-F26  src/hooks/useRunningActivity.ts::resolveSpanAgentId — old branch:
//            prefers the originating delegate call's `params.agent_id` over
//            `span.agentId`. Replacement: "Use stamped span.agentId from
//            start/end reducer; no originating-params attribution workaround."
//   DEL-F27  src/hooks/useRunningActivity.ts::activityStatusForSpan — old
//            branch: `default: return span.status`. Replacement: "lifecycle-state
//            mapping; unknown pre-state has no guessed span.status fallback."
//
// Oracle provenance: the spec's replacement columns.
//   F26 — a span whose emitted `agentId` (the stamped producer id) disagrees
//   with an originating delegate call's `params.agent_id` must resolve to the
//   stamped `span.agentId`. The delegate-call params workaround is gone.
//   F27 — a span that has `status:'running'` ONLY because its start bracket
//   opened (no `lifecycleState` yet) must NOT report a running status: the open
//   bracket is explicitly documented as "must not count as running activity".

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
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
import type { ActivityItem, AgentActivityItem } from './useRunningActivity'

const SESSION_ID = 'uf2-attribution-test'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

const AGENTS: Agent[] = [
  { id: 'target-agent', name: 'Target', type: 'Subagent', locked: false, status: 'active' } as Agent,
  { id: 'other-agent', name: 'Other', type: 'Subagent', locked: false, status: 'active' } as Agent,
]

function assistantWithSpans(spans: SubagentSpan[], toolCalls: ToolCall[] = []): ChatMessage {
  return {
    id: 'msg_1',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    spans,
    tool_calls: toolCalls,
  } as ChatMessage
}

function findAgentItem(items: ActivityItem[], key: string): AgentActivityItem | undefined {
  return items.find((i): i is AgentActivityItem => i.kind === 'agent' && i.key === key)
}

beforeEach(() => {
  ;(fetchAgents as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(AGENTS)
  useChatStore.setState({ sessionsById: {}, messages: [], toolCalls: {}, toolCallOrder: [] })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('UF2/DEL-F26 — resolveSpanAgentId uses the stamped span.agentId, not the delegate params', () => {
  it('attributes the span to its stamped agentId even when a delegate call names a different agent', async () => {
    // The span's own emitted producer id is 'target-agent'. The originating
    // delegate tool call (parentCallId) names 'other-agent' in its params —
    // the now-removed workaround would have preferred that. Spec: use the
    // stamped span.agentId.
    const span: SubagentSpan = {
      spanId: 'span_attr',
      parentCallId: 'delegate_call_1',
      taskLabel: 'audit',
      agentId: 'target-agent',
      lifecycleState: 'running',
      status: 'running',
    } as SubagentSpan
    const delegateCall = {
      id: 'delegate_call_1',
      tool: 'delegate',
      params: { agent_id: 'other-agent' },
      status: 'running',
    } as unknown as ToolCall

    useChatStore.setState({ messages: [assistantWithSpans([span], [delegateCall])] })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(makeClient()) })
    await waitFor(() => expect(fetchAgents).toHaveBeenCalled())

    const item = findAgentItem(result.current.running, 'span_attr')
    expect(item, 'the running agent item must be present').toBeDefined()
    expect(item!.agentId).toBe('target-agent')
  })
})

describe('UF2/DEL-F27 — activityStatusForSpan does not guess a status from span.status', () => {
  it('does not report a running status for an open bracket that has no lifecycleState yet', async () => {
    // status:'running' with NO lifecycleState is the open `subagent_start`
    // bracket before any `subagent_state` arrived — spec says it must not
    // count as running activity, and the guessed `span.status` fallback is
    // removed.
    const span: SubagentSpan = {
      spanId: 'span_open_bracket',
      parentCallId: 'call_x',
      taskLabel: 'queued work',
      agentId: 'target-agent',
      status: 'running',
      // lifecycleState intentionally absent
    } as SubagentSpan

    useChatStore.setState({ messages: [assistantWithSpans([span])] })

    const { result } = renderHook(() => useRunningActivity(), { wrapper: makeWrapper(makeClient()) })
    await waitFor(() => expect(fetchAgents).toHaveBeenCalled())

    const item =
      findAgentItem(result.current.running, 'span_open_bracket') ??
      findAgentItem(result.current.recentlyFinished, 'span_open_bracket')
    expect(item, 'the agent item must be present somewhere in the activity list').toBeDefined()
    expect(item!.status).not.toBe('running')
  })
})
