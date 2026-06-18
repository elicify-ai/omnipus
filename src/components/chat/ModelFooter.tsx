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
 *
 * Wave 6 / F3 — synthetic-turn legibility review.
 * The F3 ticket asked for model name + total tokens + latency +
 * synthetic-turn badge. Of those, only `model` is available on the
 * `Message` wire schema (`contracts/components/schemas/Message.yaml`):
 *   - `tokens?: number`    — total input+output tokens for the turn
 *   - `cost?: number`      — USD cost (rendered separately by MessageItem)
 *   - `tool_calls[].duration_ms` — per-tool latency, not per-message
 *   - `synthetic` / `latency` — do NOT exist on the wire at all
 *
 * Surface area decision (locked 2026-06-18):
 *   - `model`     → rendered here (the per-turn record, FR-014).
 *   - `tokens`    → rendered in MessageItem's broader footer when > 0
 *                   (already implemented; not duplicated here to keep the
 *                   replay row visually identical to the live render).
 *   - `cost`      → rendered in MessageItem's broader footer when > 0
 *                   (same rationale).
 *   - `latency`   → not available on the wire; deferred to a future wire
 *                   change (would need `Message.duration_ms`).
 *   - `synthetic` → no wire field; the closest existing concept is
 *                   `type: "turn_canceled"` which MessageItem already
 *                   renders as "(interrupted)". A dedicated synthetic
 *                   badge requires a wire addition and a follow-up plan
 *                   revision; out of scope for F3.
 *
 * Net effect: ModelFooter remains the per-turn model record. F3 is
 * satisfied because the per-turn model slug (the only legibility data
 * available for synthetic vs. real turns) is rendered in BOTH the live
 * AssistantUI path and the replay path, identically. Legacy turns (no
 * model) still render nothing — no placeholder, no "(model not
 * recorded)", as required by spec §18 Q6.
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