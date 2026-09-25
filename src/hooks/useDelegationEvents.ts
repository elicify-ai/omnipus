/**
 * useDelegationEvents — ordered delegation lines for one chat session.
 *
 * Reads that session's own bucket (not the foreground projection, which is
 * only the active session) and derives lines from the records a reload
 * rebuilds: spans on messages, tool calls baked onto those messages, and
 * tool calls still on the live turn. Display names come from the same
 * agents list the activity bar uses.
 *
 * A streamed token rebuilds the bucket, but not a message's spans or tool
 * calls. The subscription compares only those arrays, the message id and
 * timestamp, and the live tool-call maps — so a content-only update keeps
 * the previous lines and does not derive again. When one message's arrays
 * do change, views for the others are reused from a WeakMap keyed on those
 * arrays. Derivation still runs once for the whole session in that case:
 * a background command or a cancel can join two messages, and that join
 * lives inside `deriveDelegationEvents`.
 */

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useStoreWithEqualityFn } from 'zustand/traditional'
import { fetchAgents } from '@/lib/api'
import type { Agent, ToolCall } from '@/lib/api'
import { getMessages, useChatStore } from '@/store/chat'
import type { ChatMessage, SessionChatState, SubagentSpan } from '@/store/chat'
import { deriveDelegationEvents } from '@/lib/delegationEvents'
import type { DelegationEventSource, DelegationMessageView, DelegationSpanView } from '@/lib/delegationEvents'
import type { DelegationEvent, UseDelegationEvents } from '@/lib/delegationEvents.types'

const EMPTY_AGENTS: Agent[] = []

type DelegationBucketSlice = Pick<
  SessionChatState,
  'messageOrder' | 'messagesById' | 'toolCalls' | 'toolCallOrder' | 'toolCallOwnerMessageId'
>

interface CachedMessageView {
  id: string
  timestamp: string
  spans: ChatMessage['spans']
  toolCalls: ChatMessage['tool_calls']
  view: DelegationMessageView
}

/** Keyed on the spans array. A second map covers messages that have tool calls but no spans. */
const viewsBySpans = new WeakMap<object, CachedMessageView>()
const viewsByCalls = new WeakMap<object, CachedMessageView>()

/**
 * How many message views were built rather than reused. A token, or a change
 * to one message, must not increase this for the messages whose spans and
 * tool calls stayed the same object.
 */
let messageViewsBuilt = 0

export function delegationMessageViewsBuilt(): number {
  return messageViewsBuilt
}

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

function sameCachedView(entry: CachedMessageView, message: ChatMessage): boolean {
  return (
    entry.id === message.id &&
    entry.timestamp === message.timestamp &&
    entry.spans === message.spans &&
    entry.toolCalls === message.tool_calls
  )
}

function cachedMessageView(message: ChatMessage): DelegationMessageView {
  const spansKey = message.spans
  const callsKey = message.tool_calls
  const bySpans = spansKey ? viewsBySpans.get(spansKey) : undefined
  if (bySpans && sameCachedView(bySpans, message)) return bySpans.view
  const byCalls = callsKey ? viewsByCalls.get(callsKey) : undefined
  if (byCalls && sameCachedView(byCalls, message)) return byCalls.view
  messageViewsBuilt += 1
  const entry: CachedMessageView = {
    id: message.id,
    timestamp: message.timestamp,
    spans: spansKey,
    toolCalls: callsKey,
    view: messageView(message),
  }
  if (spansKey) viewsBySpans.set(spansKey, entry)
  if (callsKey) viewsByCalls.set(callsKey, entry)
  return entry.view
}

function selectDelegationSlice(bucket: SessionChatState | undefined): DelegationBucketSlice | null {
  if (!bucket) return null
  return {
    messageOrder: bucket.messageOrder,
    messagesById: bucket.messagesById,
    toolCalls: bucket.toolCalls,
    toolCallOrder: bucket.toolCallOrder,
    toolCallOwnerMessageId: bucket.toolCallOwnerMessageId,
  }
}

function sameMessageInputs(a: ChatMessage | undefined, b: ChatMessage | undefined): boolean {
  if (a === b) return true
  if (!a || !b) return false
  return a.id === b.id && a.timestamp === b.timestamp && a.spans === b.spans && a.tool_calls === b.tool_calls
}

function sameDelegationInputs(a: DelegationBucketSlice | null, b: DelegationBucketSlice | null): boolean {
  if (a === b) return true
  if (!a || !b) return false
  if (a.toolCalls !== b.toolCalls) return false
  if (a.toolCallOrder !== b.toolCallOrder) return false
  if (a.toolCallOwnerMessageId !== b.toolCallOwnerMessageId) return false
  if (a.messageOrder.length !== b.messageOrder.length) return false
  for (let i = 0; i < a.messageOrder.length; i++) {
    const idA = a.messageOrder[i]
    const idB = b.messageOrder[i]
    if (idA !== idB) return false
    if (!sameMessageInputs(a.messagesById[idA], b.messagesById[idB])) return false
  }
  return true
}

function agentNamesFrom(agents: readonly Agent[]): Record<string, string> {
  const agentNames: Record<string, string> = {}
  for (const agent of agents) {
    if (agent.id && agent.name) agentNames[agent.id] = agent.name
  }
  return agentNames
}

function sourceFromSlice(
  sessionId: string,
  slice: DelegationBucketSlice,
  agentNames: Record<string, string>,
): DelegationEventSource {
  return {
    sessionId,
    messages: getMessages(slice).map(cachedMessageView),
    liveToolCalls: slice.toolCalls,
    liveToolCallOrder: slice.toolCallOrder,
    toolCallOwnerMessageId: slice.toolCallOwnerMessageId,
    agentNames,
  }
}

export const useDelegationEvents: UseDelegationEvents = (sessionId) => {
  const slice = useStoreWithEqualityFn(
    useChatStore,
    (state) => selectDelegationSlice(sessionId ? state.sessionsById[sessionId] : undefined),
    sameDelegationInputs,
  )
  const { data: agents = EMPTY_AGENTS } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })

  return useMemo(() => {
    if (!sessionId || !slice) return []
    return deriveDelegationEvents(sourceFromSlice(sessionId, slice, agentNamesFrom(agents)))
  }, [sessionId, slice, agents])
}

export type { DelegationEvent }
