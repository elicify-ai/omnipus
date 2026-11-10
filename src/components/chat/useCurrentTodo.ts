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

// Saved history is paged (#1266), so recovery walks backwards from the latest
// page with `before` until it finds the current turn's newest checklist write,
// reaches the turn's start (an entry owned by an earlier turn), or runs out of
// earlier history. The walk is bounded: at 100 source groups per page the cap
// covers a 1,000-group turn. Past the cap no checklist is shown — an older
// write from the same turn could be stale, so showing nothing beats a guess.
const TURN_HISTORY_PAGE_LIMIT = 100
export const TURN_HISTORY_PAGE_CAP = 10

type TurnChecklistRead = { call: ToolCall | null; capped: boolean }

async function readTurnChecklist(sessionId: string, turnId: string): Promise<TurnChecklistRead> {
  let before: string | undefined
  for (let read = 0; read < TURN_HISTORY_PAGE_CAP; read++) {
    const page = await fetchSessionMessages(sessionId, { limit: TURN_HISTORY_PAGE_LIMIT, ...(before ? { before } : {}) })
    // Pages are ascending and read newest-first, so the first page holding a
    // write for this turn holds its newest one.
    const call = latestTurnChecklist(page.entries, turnId)
    if (call) return { call, capped: false }
    const reachedTurnStart = page.entries.some((entry) => entry.turnId !== undefined && entry.turnId !== turnId)
    if (reachedTurnStart || !page.has_earlier || !page.next_before) return { call: null, capped: false }
    before = page.next_before
  }
  return { call: null, capped: true }
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
  if (!call) return { activeForm: null, issues: [] }
  // Live arguments are recorded before backend normalization. Normalize a copy,
  // never the store's raw arguments; blank strings are intentionally absent.
  const normalized = Array.isArray(call.params.todos) ? call.params.todos.map((item: unknown) => {
    if (item && typeof item === 'object' && 'active_form' in item && typeof item.active_form === 'string') {
      // Match server normalization before the generated code-point bound.
      // Only bidi controls are stripped; joiners in visible text stay intact.
      const activeForm = item.active_form
        .replace(/[\u202A-\u202E\u2066-\u2069\u200E\u200F]/gu, '')
        .replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '')
      const visible = /[^\p{White_Space}\p{Cf}]/u.test(activeForm)
      return { ...item, active_form: visible ? activeForm : undefined }
    }
    return item
  }) : call.params.todos
  const parsed = TodoListSchema.safeParse(normalized)
  const issues = parsed.success ? [] : parsed.error.issues.map((issue) => ({ code: issue.code, path: issue.path.join('.') || 'todos' }))
  // A diagnostic about another field must not erase readable accepted work.
  // The canonical generated validator alone owns optional-form validity.
  const todos = parsed.success ? parsed.data : Array.isArray(normalized) ? normalized.map(readableAcceptedTodo) : null
  if (!todos || todos.some((todo) => todo == null)) return { activeForm: null, issues }
  const index = todos.findIndex((todo) => todo?.status === 'in_progress')
  const raw = Array.isArray(normalized) ? normalized[index] : null
  const activeForm = raw && typeof raw === 'object' && 'active_form' in raw && typeof raw.active_form === 'string' ? raw.active_form : null
  const invalidForm = issues.some((issue) => issue.path === `${index}.active_form` || issue.path.startsWith(`${index}.active_form.`))
  return { activeForm: invalidForm ? null : activeForm, issues }
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
    queryFn: () => readTurnChecklist(sessionId!, turnId!),
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
  const recovered = turnId && !history.isError ? history.data?.call ?? null : null
  const capped = !!turnId && !history.isError && history.data?.capped === true
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

  useEffect(() => {
    if (!sessionId || !turnId || !capped) return
    const key = JSON.stringify(['history-cap', sessionId, turnId])
    if (reported.current.has(key)) return
    reported.current.add(key)
    const fields = { sessionId, turnId, pageCap: TURN_HISTORY_PAGE_CAP }
    console.warn('chat.current_todo_history_capped', fields)
    logDiagnostic('chatCurrentTodoHistoryCapped', fields)
  }, [sessionId, turnId, capped])

  return { activeForm: display.activeForm }
}
