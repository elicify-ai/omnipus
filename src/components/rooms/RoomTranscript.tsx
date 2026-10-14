// RoomTranscript — a group chat's own transcript (omnipus-system-agent
// FR-166, FR-168, FR-170, FR-199, FR-217; K45).
//
// Entries in room order, one per entry id (a replayed entry never shows
// twice). Agent text is shown as written — it is input only and never runs
// as a command (FR-168). An answer to work started before a pause keeps its
// "after pause" mark, also after /resume (FR-170, FR-217).

import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'

export function RoomTranscript({ entries, agentName }: { entries: RoomEntry[]; agentName: (agentId: string) => string }) {
  return (
    <div role="log" aria-label="Group conversation" className="flex flex-1 flex-col gap-[var(--space-3)] overflow-y-auto px-[var(--space-3)] py-[var(--space-3)]">
      {entries.length === 0 && (
        <p className="m-auto text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          No messages yet. Write to everyone, or type @ to address members.
        </p>
      )}
      {entries.map((entry) => (
        <article key={entry.id} className="flex flex-col gap-[var(--space-1)]">
          <div className="flex items-baseline gap-[var(--space-2)]">
            <span className="text-[length:var(--type-caption-size)] font-semibold text-[var(--color-secondary)]">
              {entry.author.kind === 'user' ? 'You' : agentName(entry.author.agent_id ?? '')}
            </span>
            {entry.after_pause && (
              <span className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">after pause</span>
            )}
          </div>
          <p className="whitespace-pre-wrap text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{entry.text}</p>
        </article>
      ))}
    </div>
  )
}
