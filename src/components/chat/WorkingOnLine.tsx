import { WordBoundaryText } from '@/components/ui/word-boundary-text'
import { Todo as TodoSchema } from '@/lib/api/generated/schemas'
import { useChatStore } from '@/store/chat'
import type { SessionChatState } from '@/store/chat'
import { useSessionStore } from '@/store/session'

function currentTodoText(bucket: SessionChatState | undefined): string | null {
  if (!bucket?.isStreaming || bucket.isReplaying) return null

  // Live calls survive message/steering boundaries but are cleared at turn end.
  // Never scan saved messages: a later turn must not revive an old checklist.
  for (let index = bucket.toolCallOrder.length - 1; index >= 0; index--) {
    const call = bucket.toolCalls[bucket.toolCallOrder[index]]
    if (call?.tool !== 'set_todos' || call.status !== 'success') continue

    // Only a successful full replacement is authoritative. In particular, an
    // empty/all-finished replacement must not fall back to an earlier list.
    if (!Array.isArray(call.params.todos)) return null
    for (const item of call.params.todos) {
      const todo = TodoSchema.safeParse(item)
      if (todo.success && todo.data.status === 'in_progress') return todo.data.text
    }
    return null
  }
  return null
}

/** Chat-only adapter: the tool supplies the content; the catalog owns text layout. */
export function WorkingOnLine() {
  const sessionId = useSessionStore((state) => state.activeSessionId)
  const text = useChatStore((state) => currentTodoText(sessionId ? state.sessionsById[sessionId] : undefined))
  if (!text) return null

  return (
    <div data-testid="chat-working-on" role="status" aria-live="polite" aria-atomic="true">
      <WordBoundaryText
        text={`Working on: ${text}`}
        className="truncate pb-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
      />
    </div>
  )
}
