/**
 * ProviderRetryIndicator — provider-messages spec §8 (TDD row 17/25/34),
 * flat per D10: no card, no border, no box — a muted mono line with one
 * kind-colored dot, in the grey event-line grammar the delegation lines use
 * (`DelegationEventLine.tsx`). It reuses `RateLimitIndicator`'s per-second
 * interval + `role="status"` pattern but deliberately does NOT copy its
 * success-colour-at-zero or its dismiss button — this indicator has NO
 * dismiss control and never shows success colour (the retrying-now state is
 * warning-toned, not success-toned).
 *
 * Countdown (C-11/C-15): the mm:ss is derived from `retry_at − serverNow`,
 * where `serverNow = sent_at + (clientNow − receivedAt)` and receivedAt is
 * the client-clock reading captured at frame receipt — clamped at 0. Clocks
 * are never compared directly.
 *
 * Live-region contract (MAJ-016): the `role="status" aria-live="polite"`
 * region announces the full line ONCE per attempt start, with the mm:ss
 * frozen at that moment. The per-second ticking mm:ss sits OUTSIDE the live
 * region so a 1-second tick never re-announces.
 */
import { useEffect, useState } from 'react'
import { flushSync } from 'react-dom'

export interface ProviderRetryIndicatorProps {
  /** The candidate the chain is currently waiting on (e.g. "OpenRouter"). */
  provider: string
  /** The candidate model (e.g. "model-b"). */
  model: string
  /** ISO timestamp — when the next attempt fires (server clock). */
  retryAt: string
  /** ISO timestamp — when the backend sent the frame (server clock). */
  sentAt: string
  /** ISO timestamp — the client-clock reading at frame receipt. */
  receivedAt: string
  attempt: number
  maxAttempts: number
  /** The chain's previous candidate — present only on a within-provider move (MIN-104). */
  previousProvider?: string
  previousModel?: string
}

/** C-15 mm:ss — "2:00", "1:59", "0:20". Negative clamps to "0:00". */
export function formatRetryCountdown(totalSeconds: number): string {
  const safe = Math.max(0, Math.floor(totalSeconds))
  const minutes = Math.floor(safe / 60)
  const seconds = safe % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

/**
 * C-11 skew-corrected remaining seconds: `retry_at − (sent_at + (clientNow −
 * receivedAt))`, clamped at 0. Any unparseable timestamp renders as 0
 * (→ "Retrying now…") rather than NaN.
 */
export function remainingRetrySeconds(
  retryAt: string,
  sentAt: string,
  receivedAt: string,
  nowMs: number,
): number {
  const retryMs = Date.parse(retryAt)
  const sentMs = Date.parse(sentAt)
  const receivedMs = Date.parse(receivedAt)
  if (!Number.isFinite(retryMs) || !Number.isFinite(sentMs) || !Number.isFinite(receivedMs)) return 0
  const serverNowMs = sentMs + (nowMs - receivedMs)
  return Math.max(0, Math.ceil((retryMs - serverNowMs) / 1000))
}

function buildWaitingLine(props: ProviderRetryIndicatorProps, remainingSecondsValue: number): string {
  const { provider, model, previousProvider, previousModel, attempt, maxAttempts } = props
  // MIN-104 — the model-switch qualifier applies only when the chain moved
  // within ONE provider (openrouter/model-a → openrouter/model-b). A
  // same-model retry or a cross-provider move renders the plain provider line.
  const showModelQualifier =
    previousProvider === provider && previousModel !== undefined && previousModel !== model
  const displayName = showModelQualifier ? `${provider} (${model})` : provider
  return `${displayName} is busy. Retrying automatically in ${formatRetryCountdown(remainingSecondsValue)} (attempt ${attempt} of ${maxAttempts}).`
}

export function ProviderRetryIndicator(props: ProviderRetryIndicatorProps) {
  const { provider, model, retryAt, sentAt, receivedAt, attempt, maxAttempts, previousProvider, previousModel } = props
  const [nowMs, setNowMs] = useState(() => Date.now())

  useEffect(() => {
    const interval = setInterval(() => {
      // The per-second mm:ss is delivered in the tick it is computed: a
      // countdown is a status line whose every second is user-visible, so the
      // render is flushed synchronously rather than left to the scheduler
      // (a plain setState here lands a frame LATE whenever the timer fires
      // outside React's scheduling — one skipped second on a slow frame).
      flushSync(() => setNowMs(Date.now()))
    }, 1000)
    return () => clearInterval(interval)
  }, [])

  const remaining = remainingRetrySeconds(retryAt, sentAt, receivedAt, nowMs)
  const waiting = remaining > 0

  // MAJ-016 — the announcement freezes at attempt start: captured once per
  // candidate/attempt change, never recomputed by the 1-second tick. A
  // mid-wait attach announces the CURRENT line once (its attempt start, for
  // this client).
  const [announcement, setAnnouncement] = useState<{ key: string; text: string }>(() => ({
    key: `${provider}|${model}|${retryAt}|${attempt}|${maxAttempts}`,
    text: buildWaitingLine(props, remainingRetrySeconds(retryAt, sentAt, receivedAt, Date.now())),
  }))
  useEffect(() => {
    const key = `${provider}|${model}|${retryAt}|${attempt}|${maxAttempts}`
    if (announcement.key === key) return
    setAnnouncement({ key, text: buildWaitingLine(props, remainingRetrySeconds(retryAt, sentAt, receivedAt, Date.now())) })
  }, [provider, model, retryAt, sentAt, receivedAt, attempt, maxAttempts, previousProvider, previousModel, announcement.key, props])

  const announcementText = waiting ? announcement.text : 'Retrying now…'
  const visibleText = waiting ? buildWaitingLine(props, remaining) : 'Retrying now…'

  return (
    <div
      data-testid="provider-retry-indicator"
      data-state={waiting ? 'waiting' : 'retrying-now'}
      className="flex min-w-0 items-center gap-[var(--space-1)] px-[var(--space-3)] py-[var(--space-0-5)]"
    >
      <span aria-hidden className="size-2 shrink-0 rounded-full bg-[var(--color-warning)]" />
      {/* MAJ-016 — the frozen full line is announced once per attempt; the
          ticking mm:ss span below sits OUTSIDE this live region. */}
      <div role="status" aria-live="polite" className="sr-only">
        {announcementText}
      </div>
      <span className="min-w-0 truncate font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {visibleText}
      </span>
    </div>
  )
}
