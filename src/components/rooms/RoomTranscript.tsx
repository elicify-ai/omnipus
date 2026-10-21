// RoomTranscript — a group chat's own transcript (omnipus-system-agent
// FR-166, FR-168, FR-170, FR-199, FR-217; K45).
//
// Entries in room order, one per entry id (a replayed entry never shows
// twice). Agent text is shown as written — it is input only and never runs
// as a command (FR-168). An answer to work started before a pause keeps its
// "after pause" mark, also after /resume (FR-170, FR-217).
//
// A group opens at the NEWEST message — on the first snapshot and again when
// switching rooms — so the reader starts where the conversation is.
//
// Older history (#1332, ROOM-HISTORY-PAGING-1332-20261018.md D6): reaching
// the top while earlier messages exist asks for the previous page — never on
// mount, never an automatic walk to the beginning. The "Earlier messages
// aren't shown" hint stays until the old end is reached and is itself the
// trigger when the loaded window fits without scrolling (no scrollbar exists
// to reach the top with). Loading shows a status; a failure shows an inline
// error with Retry for the same boundary. A prepended page keeps the
// reader's CURRENT place: the pending request is anchored on the entry that
// was first visible when it started, and the reader's scroll position at
// response time grows by the height added above that entry — scrolling done
// while the page was in flight and live messages appended below in the
// meantime never move the reader.

import { useLayoutEffect, useRef } from 'react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'

/** How close to the top (px) counts as "reached the top" for loading older messages. */
const TOP_THRESHOLD_PX = 24

/** The reader's place during a pending older-page request: the first visible entry. */
interface ReaderAnchor { // not-wire-format: in-memory scroll anchor
  entryId: string
  /** The entry's content offset at the last committed request layout. */
  offsetTop: number
}

/** The element's offset within the log's content — scroll-position independent. */
function contentOffsetOf(log: HTMLElement, el: Element): number {
  return el.getBoundingClientRect().top - log.getBoundingClientRect().top + log.scrollTop
}

/** Anchor on the log's first displayed entry (at the top, the first visible one). */
function captureAnchor(log: HTMLElement): ReaderAnchor {
  const first = log.querySelector('[data-room-entry-id]')
  return {
    entryId: first?.getAttribute('data-room-entry-id') ?? '',
    offsetTop: first ? contentOffsetOf(log, first) : 0,
  }
}

export interface RoomTranscriptHistory { // not-wire-format: React props for the older-history controls
  status: 'idle' | 'loading' | 'error'
  failed: boolean
  loadOlder: () => void
}

export function RoomTranscript({ roomId, snapshotReceived, entries, agentName, hasOlder = false, history }: {
  roomId: string
  snapshotReceived: boolean
  entries: RoomEntry[]
  agentName: (agentId: string) => string
  hasOlder?: boolean
  history?: RoomTranscriptHistory
}) {
  const logRef = useRef<HTMLDivElement>(null)
  const anchorRef = useRef<ReaderAnchor | null>(null)

  // Keep the opening jump pending through cached rows until this opening's
  // attach snapshot has merged its catch-up. Readiness is snapshotReceived,
  // not the wire frame's reserved complete flag. Later live entries and
  // reattaches never re-arm an already settled opening.
  const roomRef = useRef<string | undefined>(undefined)
  const jumpPendingRef = useRef(false)
  useLayoutEffect(() => {
    if (roomRef.current !== roomId) {
      roomRef.current = roomId
      jumpPendingRef.current = true
      // A place kept in another room is not a place here.
      anchorRef.current = null
    }
    const log = logRef.current
    if (!log || !jumpPendingRef.current || !snapshotReceived) return
    jumpPendingRef.current = false
    log.scrollTop = log.scrollHeight
  }, [roomId, entries, snapshotReceived])

  // Keep the reader's place through EVERY committed request layout. The
  // loading row appears before the page arrives; the hint/loading/error rows
  // can then shrink or disappear without changing the first message seq.
  // A request-start measurement alone would restore the pre-loader position
  // rather than the position the reader has immediately before completion.
  useLayoutEffect(() => {
    const log = logRef.current
    const anchor = anchorRef.current
    if (!log || !anchor) return
    const anchored = anchor.entryId
      ? log.querySelector(`[data-room-entry-id="${CSS.escape(anchor.entryId)}"]`)
      : null
    // The anchor entry is gone (the window was replaced) — never guess a position.
    if (!anchored) {
      anchorRef.current = null
      return
    }
    const offsetTop = contentOffsetOf(log, anchored)
    const delta = offsetTop - anchor.offsetTop
    // Refresh the baseline after each layout, and release it once the
    // request settles. Tail growth never enters this entry-offset delta.
    anchorRef.current = history?.status === 'loading' ? { ...anchor, offsetTop } : null
    // Negative deltas matter too: removing the history controls can shrink
    // the content above the reader, even for an empty terminal page.
    if (delta !== 0) log.scrollTop += delta
  })

  // One user action — reaching the top, or the hint when no scrollbar exists
  // to reach it with — asks for one older page.
  const requestOlder = () => {
    const log = logRef.current
    if (!log || !history || history.status === 'loading') return
    anchorRef.current = captureAnchor(log)
    history.loadOlder()
  }

  const onScroll = () => {
    const log = logRef.current
    if (!log || !hasOlder || log.scrollTop > TOP_THRESHOLD_PX) return
    requestOlder()
  }

  // Manual prepend compensation owns this scroller. Disable native scroll
  // anchoring so the browser cannot add the same offset a second time (W1).
  return (
    <div ref={logRef} onScroll={onScroll} role="log" aria-label="Group conversation" className="flex flex-1 flex-col gap-[var(--space-3)] overflow-y-auto [overflow-anchor:none] px-[var(--space-3)] py-[var(--space-3)]">
      {hasOlder && (
        <Button
          variant="ghost"
          size="sm"
          onClick={requestOlder}
          className="self-center font-[var(--font-weight-regular)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
        >
          Earlier messages aren&apos;t shown
        </Button>
      )}
      {history?.status === 'loading' && (
        <p role="status" className="self-center text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          Loading earlier messages…
        </p>
      )}
      {history?.failed && (
        <ErrorState
          message="Could not load earlier messages."
          onRetry={() => requestOlder()}
          className="py-[var(--space-1)]"
        />
      )}
      {entries.length === 0 && (
        <p className="m-auto text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          No messages yet. Write to everyone, or type @ to address members.
        </p>
      )}
      {entries.map((entry) => (
        <article key={entry.id} data-room-entry-id={entry.id} className="flex flex-col gap-[var(--space-1)]">
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
