/**
 * useDelegationEvents — ordered delegation lines for one chat session.
 *
 * Reads that session's own bucket (not the foreground projection, which is
 * only the active session) and derives lines from the records a reload
 * rebuilds: spans on messages, tool calls baked onto those messages, and
 * tool calls still on the live turn. Display names come from the same
 * agents list the activity bar uses.
 */

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchAgents } from '@/lib/api'
import type { Agent, ToolCall } from '@/lib/api'
import { getMessages, useChatStore } from '@/store/chat'
import type { ChatMessage, SessionChatState, SubagentSpan } from '@/store/chat'
import { deriveDelegationEvents } from '@/lib/delegationEvents'
import type { DelegationEventSource, DelegationMessageView, DelegationSpanView } from '@/lib/delegationEvents'
import type { DelegationEvent, UseDelegationEvents } from '@/lib/delegationEvents.types'

const EMPTY_AGENTS: Agent[] = []

function spanView(span: SubagentSpan): DelegationSpanView {
  return {
    spanId: span.spanId,
    parentCallId: span.parentCallId,
    taskLabel: span.taskLabel,
    agentId: span.agentId,
    childSessionId: span.childSessionId,
    status: span.status,
    lifecycleState: span.lifecycleState,
    finalResult: span.status === 'running' ? undefined : span.finalResult,
    statusLine: span.statusLine,
    lastUpdateAt: span.lastUpdateAt,
  }
}

function messageView(message: ChatMessage): DelegationMessageView {
  return {
    id: message.id,
    timestamp: message.timestamp,
    spans: message.spans?.map(spanView),
    toolCalls: message.tool_calls as ToolCall[] | undefined,
  }
}

function sourceFromBucket(
  sessionId: string,
  bucket: SessionChatState,
  agentNames: Record<string, string>,
): DelegationEventSource {
  return {
    sessionId,
    messages: getMessages(bucket).map(messageView),
    liveToolCalls: bucket.toolCalls,
    liveToolCallOrder: bucket.toolCallOrder,
    toolCallOwnerMessageId: bucket.toolCallOwnerMessageId,
    agentNames,
  }
}

export const useDelegationEvents: UseDelegationEvents = (sessionId) => {
  const bucket = useChatStore((state) => (sessionId ? state.sessionsById[sessionId] : undefined))
  const { data: agents = EMPTY_AGENTS } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })

  return useMemo(() => {
    if (!sessionId || !bucket) return []
    const agentNames: Record<string, string> = {}
    for (const agent of agents) {
      if (agent.id && agent.name) agentNames[agent.id] = agent.name
    }
    return deriveDelegationEvents(sourceFromBucket(sessionId, bucket, agentNames))
  }, [sessionId, bucket, agents])
}

export type { DelegationEvent }
