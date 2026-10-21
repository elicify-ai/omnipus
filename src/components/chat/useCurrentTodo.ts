import { useEffect, useMemo, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'
import { fetchSessionMessages } from '@/lib/api'
import type { Message, ToolCall } from '@/lib/api'
import type { Todo } from '@/lib/api/generated/openapi-types'
import { Todo as TodoSchema } from '@/lib/api/generated/schemas'
import { queryClient } from '@/lib/queryClient'
import { logDiagnostic } from '@/lib/telemetry'
import { useChatStore } from '@/store/chat'
import type { SessionChatState } from '@/store/chat'
import { useSessionStore } from '@/store/session'

const TodoListSchema = z.array(TodoSchema)

function latestLiveChecklist(bucket: SessionChatState): ToolCall | null {
  for (let index = bucket.toolCallOrder.length - 1; index >= 0; index--) {
    const call = bucket.toolCalls[bucket.toolCallOrder[index]]
    if (call?.tool !== 'set_todos' || call.status !== 'success') continue
    const ownerId = bucket.toolCallOwnerMessageId?.[call.id]
    const ownerTurn = ownerId ? bucket.messagesById[ownerId]?.turnId : undefined
    if (bucket.activeTurnId && ownerTurn && ownerTurn !== bucket.activeTurnId) continue
    return call
  }
  return null
}

function latestTurnChecklist(messages: readonly Message[], turnId: string): ToolCall | null {
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]
    if (message.role !== 'assistant' || message.turnId !== turnId) continue
    const calls = message.tool_calls ?? []
    for (let callIndex = calls.length - 1; callIndex >= 0; callIndex--) {
      const call = calls[callIndex]
      if (call.tool === 'set_todos' && call.status === 'success') return call
    }
  }
  return null
}

// The tool's successful write is authoritative. Its optional/empty status
// defaults to pending. These guards only establish that accepted data can be
// displayed as literal text; they do not invent a second wire type or limit.
function readableAcceptedTodo(item: unknown): Todo | null {
  if (!item || typeof item !== 'object' || !('text' in item) || typeof item.text !== 'string' || !item.text) return null
  const status = !('status' in item) || typeof item.status !== 'string' || item.status === '' ? 'pending' : item.status
  if (status !== 'pending' && status !== 'in_progress' && status !== 'completed') return null
  return { text: item.text, status }
}

function checklistDisplay(call: ToolCall | null) {
  if (!call) return { text: null, unavailable: false, issues: [] }
  const parsed = TodoListSchema.safeParse(call.params.todos)
  const issues = parsed.success ? [] : parsed.error.issues.map((issue) => ({ code: issue.code, path: issue.path.join('.') || 'todos' }))
  // Generated maxLength counts UTF-16 units; the backend counts Unicode code
  // points. A diagnostic must not erase a readable backend-accepted item.
  const todos = parsed.success ? parsed.data : Array.isArray(call.params.todos) ? call.params.todos.map(readableAcceptedTodo) : null
  if (!todos || todos.some((todo) => todo == null)) return { text: null, unavailable: true, issues }
  return { text: todos.find((todo) => todo?.status === 'in_progress')?.text ?? null, unavailable: false, issues }
}

/** Current conversation/turn only, independent of the transient tool-card cache. */
export function useCurrentTodo(enabled: boolean) {
  const sessionId = useSessionStore((state) => state.activeSessionId)
  const bucket = useChatStore((state) => sessionId ? state.sessionsById[sessionId] : undefined)
  const active = enabled && !!bucket?.isStreaming && !bucket.isReplaying && !bucket.awaitingCatchUp
  const turnId = active ? bucket?.activeTurnId ?? null : null
  const history = useQuery({
    // Recovery is optional: its data/error state must never replace the main
    // chat-history query or unmount an otherwise usable conversation.
    queryKey: ['current-todo-history', sessionId, turnId],
    queryFn: () => fetchSessionMessages(sessionId!),
    enabled: !!sessionId && !!turnId,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    retry: false,
  }, queryClient)

  // Replay may omit turn ownership on a tool-only entry. REST Message already
  // carries both turn_id and tool_calls, so restore through that existing API,
  // not by borrowing an uncorrelated preceding replay bubble. New live writes
  // win over a history snapshot that was read earlier in the same turn.
  const live = active && bucket ? latestLiveChecklist(bucket) : null
  const recovered = turnId ? latestTurnChecklist(history.data ?? [], turnId) : null
  const call = active ? live ?? recovered : null
  const display = useMemo(() => checklistDisplay(call), [call])
  const reported = useRef(new Set<string>())
  const callId = call?.id

  useEffect(() => {
    if (!sessionId || !callId || display.issues.length === 0) return
    const key = JSON.stringify([sessionId, callId])
    if (reported.current.has(key)) return
    reported.current.add(key)
    const fields = { sessionId, callId, issues: display.issues }
    console.warn('chat.current_todo_validation_failed', fields)
    logDiagnostic('chatCurrentTodoValidationFailed', { sessionId, callId, issues: JSON.stringify(display.issues) })
  }, [sessionId, callId, display.issues])

  useEffect(() => {
    if (!sessionId || !turnId || !history.isError) return
    const key = JSON.stringify(['history', sessionId, turnId])
    if (reported.current.has(key)) return
    reported.current.add(key)
    const fields = { sessionId, turnId, error: history.error instanceof Error ? history.error.message : 'History read failed' }
    console.warn('chat.current_todo_history_failed', fields)
    logDiagnostic('chatCurrentTodoHistoryFailed', fields)
  }, [sessionId, turnId, history.isError, history.error])

  return { text: display.text, unavailable: display.unavailable || (active && !!turnId && history.isError && !call) }
}
