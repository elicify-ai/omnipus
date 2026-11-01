import { Tooltip } from '@/components/ui/tooltip'
import type { EffortRecord } from '@/lib/api/generated/asyncapi-types'
import { effortRecordNote, effortStatsLine, thinkingStatsLine } from '@/lib/effort/chatEffort'

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

/**
 * Effort levels (US-3 AS6, US-7, FR-024/FR-036, FR-058): the model slug is
 * also the turn's statistics tooltip trigger. The tooltip carries
 * "Effort: High" (what the request carried) and "Thinking: N tokens
 * (reported by the provider)" / "Thinking: not reported". It appears only on
 * turns that carry an effort record or a thinking figure: a pre-feature turn
 * makes no claim either way. No output-token line (S-21).
 */
export function ModelFooter({
  model,
  effort,
  turnReasoningTokens,
}: {
  model: string | undefined
  effort?: EffortRecord
  turnReasoningTokens?: number
}) {
  if (typeof model !== 'string' || model.trim().length === 0) return null
  const slug = (
    <span
      data-testid="message-model"
      className="text-[length:var(--type-caption-size)] font-mono text-[var(--color-muted)] truncate max-w-[160px]"
    >
      {model.trim()}
    </span>
  )
  if (effort === undefined && turnReasoningTokens === undefined) return slug
  const effortLine = effortStatsLine(effort)
  return (
    <Tooltip
      label={`${model.trim()}, turn statistics`}
      content={
        <span className="flex flex-col">
          {effortLine && <span>{effortLine}</span>}
          <span>{thinkingStatsLine(turnReasoningTokens)}</span>
        </span>
      }
    >
      {slug}
    </Tooltip>
  )
}

/** The per-turn effort note (FR-058): only when what was sent differs from what was chosen. */
export function TurnEffortNote({ effort }: { effort: EffortRecord | undefined }) {
  const note = effortRecordNote(effort)
  if (!note) return null
  return (
    <p data-testid="turn-effort-note" className="px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
      {note}
    </p>
  )
}