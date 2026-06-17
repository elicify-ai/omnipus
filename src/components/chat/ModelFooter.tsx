/**
 * ModelFooter — per-turn model slug displayed in the message footer.
 *
 * Used by both MessageItem.tsx (live AssistantUI render) and
 * VirtualAssistantMessageRow in ChatScreen.tsx (replay sessions) so the
 * two surfaces render the model field identically.
 *
 * FR-014: per-turn model record. Only rendered when the turn has a
 * non-empty `model` field. Legacy turns without one must NOT show any
 * model info (spec §18 Q6: no placeholder text, no "(model not
 * recorded)" string). The span uses font-mono so the model slug stays
 * readable at small sizes; `truncate max-w-[160px]` prevents a long
 * model slug from breaking the row layout.
 */

export function ModelFooter({ model }: { model: string | undefined }) {
  if (typeof model !== 'string' || model.trim().length === 0) return null
  return (
    <span
      data-testid="message-model"
      className="text-[10px] font-mono text-[var(--color-muted)] truncate max-w-[160px]"
    >
      {model.trim()}
    </span>
  )
}