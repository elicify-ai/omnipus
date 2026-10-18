// RoomTranscript — a group chat's own transcript (omnipus-system-agent
// FR-166, FR-168, FR-170, FR-199, FR-217; K45).
//
// Entries in room order, one per entry id (a replayed entry never shows
// twice). Agent text is shown as written — it is input only and never runs
// as a command (FR-168). An answer to work started before a pause keeps its
// "after pause" mark, also after /resume (FR-170, FR-217).
//
// Older history (#1332, ROOM-HISTORY-PAGING-1332-20261018.md D6): reaching
// the top while earlier messages exist asks for the previous page — never on
// mount, never an automatic walk to the beginning. The "Earlier messages
// aren't shown" hint stays until the old end is reached. Loading shows a
// status; a failure shows an inline error with Retry for the same boundary.
// A prepended page keeps the reader's place: the scroll offset grows by the
// height the page added above the visible messages.

import { useLayoutEffect, useRef } from 'react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import { Badge } from '@/components/ui/badge'
import { ErrorState } from '@/components/ui/error-state'

/** How close to the top (px) counts as "reached the top" for loading older messages. */
const TOP_THRESHOLD_PX = 24

export interface RoomTranscriptHistory { // not-wire-format: React props for the older-history controls
  status: 'idle' | 'loading' | 'error'
  failed: boolean
  loadOlder: () => void
}

export function RoomTranscript({ entries, agentName, hasOlder = false, history }: {
  entries: RoomEntry[]
  agentName: (agentId: string) => string
  hasOlder?: boolean
  history?: RoomTranscriptHistory
}) {
  const logRef = useRef<HTMLDivElement>(null)
  const anchorRef = useRef<{ scrollHeight: number; scrollTop: number; firstSeq: number | undefined } | null>(null)
  const firstSeq = entries[0]?.seq

  // Keep the reader's place when older entries are prepended.
  useLayoutEffect(() => {
    const log = logRef.current
    const anchor = anchorRef.current
    if (!log || !anchor || firstSeq === anchor.firstSeq) return
    anchorRef.current = null
    // Only move when the prepended page actually added height above the reader.
    const added = log.scrollHeight - anchor.scrollHeight
    if (added > 0) log.scrollTop = anchor.scrollTop + added
  }, [firstSeq])

  const onScroll = () => {
    const log = logRef.current
    if (!log || !history || !hasOlder || history.status === 'loading') return
    if (log.scrollTop > TOP_THRESHOLD_PX) return
    anchorRef.current = { scrollHeight: log.scrollHeight, scrollTop: log.scrollTop, firstSeq }
    history.loadOlder()
  }

  return (
    <div ref={logRef} onScroll={onScroll} role="log" aria-label="Group conversation" className="flex flex-1 flex-col gap-[var(--space-3)] overflow-y-auto px-[var(--space-3)] py-[var(--space-3)]">
      {hasOlder && (
        <Badge variant="muted" className="self-center font-[var(--font-weight-regular)]">
          Earlier messages aren&apos;t shown
        </Badge>
      )}
      {history?.status === 'loading' && (
        <p role="status" className="self-center text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          Loading earlier messages…
        </p>
      )}
      {history?.failed && (
        <ErrorState
          message="Could not load earlier messages."
          onRetry={() => {
            anchorRef.current = { scrollHeight: logRef.current?.scrollHeight ?? 0, scrollTop: logRef.current?.scrollTop ?? 0, firstSeq }
            history.loadOlder()
          }}
          className="py-[var(--space-1)]"
        />
      )}
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
