/**
 * ProviderWaitingIndicator — issues #1478/#1280: the calm line the chat shows
 * once the model has said nothing for 30 seconds ("the model has gone
 * quiet", docs/troubleshooting.md). Driven by the `provider_status` frame's
 * `waiting` state (store slot `SessionChatState.providerStatusEvent`); the
 * `responding` and `recovered` states clear the slot, so the component has
 * no dismiss control — the notice removes itself when data arrives again,
 * exactly like ProviderRetryIndicator.
 *
 * Same visual grammar as ProviderRetryIndicator (a muted mono line with one
 * kind-colored dot in the grey event-line style), but INFO-toned, not
 * warning-toned: nothing has failed — the model is slow, and the silence
 * limit (unchanged, 5 min by default) still guards the call.
 *
 * Elapsed seconds (C-11): `serverNow − silent_since` where
 * `serverNow = sent_at + (clientNow − receivedAt)` and receivedAt is the
 * client-clock reading captured at frame receipt — clamped at 0, never a
 * direct clock comparison. The optional "call ends at HH:MM" renders the
 * `stall_at` instant in the viewer's local time; an absolute instant needs
 * no skew correction.
 *
 * Live-region contract (MAJ-016, same as ProviderRetryIndicator): the
 * `role="status" aria-live="polite"` region announces the full line ONCE
 * per waiting frame with the seconds frozen at that moment; the per-second
 * ticking "Ns" sits OUTSIDE the live region so a tick never re-announces.
 */
import { useEffect, useMemo, useState } from 'react'
import { flushSync } from 'react-dom'

export interface ProviderWaitingIndicatorProps {
  /** ISO timestamp (server clock) — the last provider activity, or the round's start. */
  silentSince: string
  /** ISO timestamp (server clock) — when the backend sent the frame. */
  sentAt: string
  /** ISO timestamp — the client-clock reading at frame receipt. */
  receivedAt: string
  /** ISO timestamp (server clock) — when the silence limit ends this attempt; absent when unknown. */
  stallAt?: string
  /** Issue #1664 B3/B4 — the 1-based attempt of the call (frame `attempt`); absent on frames that do not count it. */
  attempt?: number
  /** Issue #1664 B3/B4 — the attempts the loop makes in total (frame `max_attempts`). */
  maxAttempts?: number
}

/**
 * C-11 skew-corrected silent seconds: `serverNow − silent_since`, clamped
 * at 0. Any unparseable timestamp renders as 0 rather than NaN.
 */
export function waitingElapsedSeconds(
  silentSince: string,
  sentAt: string,
  receivedAt: string,
  nowMs: number,
): number {
  const silentMs = Date.parse(silentSince)
  const sentMs = Date.parse(sentAt)
  const receivedMs = Date.parse(receivedAt)
  if (!Number.isFinite(silentMs) || !Number.isFinite(sentMs) || !Number.isFinite(receivedMs)) return 0
  const serverNowMs = sentMs + (nowMs - receivedMs)
  return Math.max(0, Math.floor((serverNowMs - silentMs) / 1000))
}

/**
 * The local short-time rendering of the stall instant ("14:32" / "2:35 PM"
 * per the viewer's locale) — or '' when the timestamp is missing or
 * unparseable, in which case the caller omits the end-of-call clause
 * entirely (never "Invalid Date").
 */
function formatStallTime(stallAt: string | undefined): string {
  if (!stallAt) return ''
  const d = new Date(stallAt)
  if (Number.isNaN(d.getTime())) return ''
  return new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }).format(d)
}

function isCount(n: number | undefined): n is number {
  return typeof n === 'number' && Number.isInteger(n) && n >= 1
}

/**
 * The end-of-attempt clause (issue #1664 B4, founder Q2): with a known end
 * time and an attempt count, an attempt that is not the last says "this
 * attempt ends (attempt N of M)" and the last says "the call ends (attempt
 * M of M)". Without a count (frames that do not carry it) the pre-#1664
 * "the call ends." stays; without an end time there is no clause at all.
 */
function stallClauseText(stallAt: string | undefined, attempt: number | undefined, maxAttempts: number | undefined): string {
  const stallTime = formatStallTime(stallAt)
  if (stallTime === '') return ''
  if (!isCount(attempt) || !isCount(maxAttempts)) return ` If nothing arrives by ${stallTime}, the call ends.`
  const ending = attempt < maxAttempts ? 'this attempt ends' : 'the call ends'
  return ` If nothing arrives by ${stallTime}, ${ending} (attempt ${attempt} of ${maxAttempts}).`
}

/** The full line at a given instant — the frozen announcement's builder. */
function buildWaitingText(
  silentSince: string,
  sentAt: string,
  receivedAt: string,
  stallAt: string | undefined,
  attempt: number | undefined,
  maxAttempts: number | undefined,
  nowMs: number,
): string {
  const elapsed = waitingElapsedSeconds(silentSince, sentAt, receivedAt, nowMs)
  return `The model hasn't responded for ${elapsed}s — still waiting.${stallClauseText(stallAt, attempt, maxAttempts)}`
}

export function ProviderWaitingIndicator(props: ProviderWaitingIndicatorProps) {
  const { silentSince, sentAt, receivedAt, stallAt, attempt, maxAttempts } = props
  const [nowMs, setNowMs] = useState(() => Date.now())

  useEffect(() => {
    const interval = setInterval(() => {
      // Same delivery rule as ProviderRetryIndicator: a countdown is a
      // status line whose every second is user-visible, so the render is
      // flushed synchronously rather than left to the scheduler.
      flushSync(() => setNowMs(Date.now()))
    }, 1000)
    return () => clearInterval(interval)
  }, [])

  // MAJ-016 — the announcement freezes when the notice appears (or when a
  // new waiting frame replaces it): memoized on the FRAME facts only, never
  // recomputed by the 1-second tick, so a ticking "Ns" never re-announces.
  const announcement = useMemo(
    () => buildWaitingText(silentSince, sentAt, receivedAt, stallAt, attempt, maxAttempts, Date.now()),
    [silentSince, sentAt, receivedAt, stallAt, attempt, maxAttempts],
  )

  const elapsedSeconds = waitingElapsedSeconds(silentSince, sentAt, receivedAt, nowMs)
  const visibleText = `The model hasn't responded for ${elapsedSeconds}s — still waiting.`
  const stallClause = stallClauseText(stallAt, attempt, maxAttempts)

  return (
    <div
      data-testid="provider-waiting-indicator"
      data-state="waiting"
      className="flex min-w-0 items-center gap-[var(--space-1)] px-[var(--space-3)] py-[var(--space-0-5)]"
    >
      <span aria-hidden className="size-2 shrink-0 rounded-full bg-[var(--color-info)]" />
      {/* MAJ-016 — the frozen full line is announced once per waiting frame;
          the ticking seconds span below sits OUTSIDE this live region. */}
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>
      {/* Issue #1664 B3/B4 — wraps, never truncates (same rule as
          ProviderRetryIndicator): at a narrow chat width the end time and
          "(attempt N of M)" at the end of the sentence stay readable. */}
      <span className="min-w-0 break-words font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {visibleText}
        {stallClause}
      </span>
    </div>
  )
}
