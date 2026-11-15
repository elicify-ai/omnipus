/**
 * ProviderHelperRows — #1537 (helper-model-notices spec FR-005/009/010,
 * US2–US4): the main chat's named helper rows, above the composer and below
 * the chat's own provider notices. One row per helper turn whose model is
 * retrying, silent or has just recovered: "{Helper name}: {the sentence the
 * helper's own chat shows}". Shown whether or not the main agent is writing.
 *
 * - Name: the browser's own agent list; "Helper" when the agent is missing,
 *   the list is loading or failed. Never an agent or session id.
 * - At most three rows, in each helper turn's first-notice order (store
 *   order), plus one summary row counting the rest without calling them
 *   waiting. A cleared row lets the next one move up.
 * - Text only: no focusable control, no dismiss, no Stop.
 * - Exactly one polite live region for the whole area. It holds the frozen
 *   state-change sentence (time frozen at notice start, name first); the
 *   ticking countdown stays outside it, and the reused indicators render no
 *   live region of their own here.
 */
import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchAgents } from '@/lib/api'
import type { ProviderHelperNotice, ProviderHelperRow, ProviderServerClock } from '@/store/chat/types'
import { ProviderRetryIndicator, providerRetryLineAt } from './ProviderRetryIndicator'
import { ProviderWaitingIndicator, buildWaitingText } from './ProviderWaitingIndicator'
import { clockAnchor, useNoticeClock } from './useNoticeClock'

/** FR-009 — at most this many named rows; the rest go into the summary row. */
export const MAX_NAMED_HELPER_ROWS = 3

/** The fallback prefix when the helper's name is unknown (FR-005). */
export const UNKNOWN_HELPER_NAME = 'Helper'

/** The recovered sentence — the same wording the chat's own recovered note uses. */
export function recoveredSentence(retries: number): string {
  return `Connection came back — the model answered after ${retries} ${retries === 1 ? 'retry' : 'retries'}.`
}

/** The summary row (chief's copy review): counts, never says "waiting". */
export function helperOverflowSentence(count: number): string {
  return count === 1 ? '1 more helper has a model notice' : `${count} more helpers have model notices`
}

interface AgentName {
  id?: unknown
  name?: unknown
}

function helperName(agents: readonly AgentName[] | undefined, agentId: string): string {
  const found = agents?.find((agent) => agent.id === agentId)
  return typeof found?.name === 'string' && found.name.trim().length > 0 ? found.name : UNKNOWN_HELPER_NAME
}

/** The sentence for one notice at `nowMs`, measured with the chat's clock. */
function noticeSentence(notice: ProviderHelperNotice, clock: ProviderServerClock | null | undefined, nowMs: number): string {
  if (notice.kind === 'recovered') return recoveredSentence(notice.retries)
  if (notice.kind === 'retry') {
    const anchor = clockAnchor(clock, notice.retry)
    return providerRetryLineAt({ ...notice.retry, ...anchor }, nowMs)
  }
  const w = notice.waiting
  const anchor = clockAnchor(clock, w)
  return buildWaitingText(w.silentSince, anchor.sentAt, anchor.receivedAt, w.stallAt, w.attempt, w.maxAttempts, nowMs)
}

function HelperRow({ row, name, clock }: { row: ProviderHelperRow; name: string; clock: ProviderServerClock | null | undefined }) {
  const notice = row.notice
  const own = notice?.kind === 'retry' ? notice.retry : notice?.kind === 'waiting' ? notice.waiting : null
  const anchor = useNoticeClock(notice, clock, own ? { sentAt: own.sentAt, receivedAt: own.receivedAt } : null)
  if (notice?.kind === 'retry' && anchor) {
    const r = notice.retry
    return (
      <ProviderRetryIndicator
        provider={r.provider}
        model={r.model}
        retryAt={r.retryAt}
        sentAt={anchor.sentAt}
        receivedAt={anchor.receivedAt}
        attempt={r.attempt}
        maxAttempts={r.maxAttempts}
        errorCode={r.errorCode}
        previousProvider={r.previousProvider}
        previousModel={r.previousModel}
        label={name}
        liveRegion={false}
        testId="provider-helper-retry-row"
      />
    )
  }
  if (notice?.kind === 'waiting' && anchor) {
    const w = notice.waiting
    return (
      <ProviderWaitingIndicator
        silentSince={w.silentSince}
        sentAt={anchor.sentAt}
        receivedAt={anchor.receivedAt}
        stallAt={w.stallAt}
        attempt={w.attempt}
        maxAttempts={w.maxAttempts}
        label={name}
        liveRegion={false}
        testId="provider-helper-waiting-row"
      />
    )
  }
  if (notice?.kind === 'recovered') {
    return (
      <div
        data-testid="provider-helper-recovered-row"
        className="flex min-w-0 items-center gap-[var(--space-1)] px-[var(--space-3)] py-[var(--space-0-5)]"
      >
        <span aria-hidden className="size-2 shrink-0 rounded-full bg-[var(--color-success)]" />
        <span className="min-w-0 break-words font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {`${name}: ${recoveredSentence(notice.retries)}`}
        </span>
      </div>
    )
  }
  return null
}

export function ProviderHelperRows({ rows, clock }: { rows: readonly ProviderHelperRow[] | undefined; clock: ProviderServerClock | null | undefined }) {
  const { data: agents } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const roster = Array.isArray(agents) ? (agents as readonly AgentName[]) : undefined
  const active = (rows ?? []).filter((row) => row.notice !== null)
  const shown = active.slice(0, MAX_NAMED_HELPER_ROWS)
  const overflow = active.length - shown.length

  // FR-010 — announce each shown row's state change once, frozen at the
  // moment it appeared; a countdown tick re-renders without a change.
  // A row that leaves takes its sentence out of the region (no stale text
  // describing a notice that is gone); a clear itself is not announced.
  const [announcement, setAnnouncement] = useState('')
  const seen = useRef(new Map<string, ProviderHelperNotice>())
  const announcedKeys = useRef<string[]>([])
  useEffect(() => {
    const changed: string[] = []
    const changedKeys: string[] = []
    const next = new Map<string, ProviderHelperNotice>()
    for (const row of shown) {
      if (!row.notice) continue
      next.set(row.key, row.notice)
      if (seen.current.get(row.key) !== row.notice) {
        changed.push(`${helperName(roster, row.agentId)}: ${noticeSentence(row.notice, clock, Date.now())}`)
        changedKeys.push(row.key)
      }
    }
    seen.current = next
    if (changed.length > 0) {
      announcedKeys.current = changedKeys
      setAnnouncement(changed.join(' '))
    } else if (announcedKeys.current.some((key) => !next.has(key))) {
      announcedKeys.current = []
      setAnnouncement('')
    }
  })

  // The live region is a sibling of the rows, not inside their wrapper: the
  // wrapper's text is then only what is visibly drawn, so no element whose
  // text starts with a helper's name mixes the visible row with the frozen
  // (hidden) announcement.
  return (
    <>
    <div data-testid="provider-helper-rows">
      {shown.length > 0 && (
        <div className="px-[var(--space-3)] pb-[var(--space-2)]">
          {shown.map((row) => (
            <HelperRow key={row.key} row={row} name={helperName(roster, row.agentId)} clock={clock} />
          ))}
          {overflow > 0 && (
            <div
              data-testid="provider-helper-overflow-row"
              className="min-w-0 break-words px-[var(--space-3)] py-[var(--space-0-5)] font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
            >
              {helperOverflowSentence(overflow)}
            </div>
          )}
        </div>
      )}
    </div>
    <div aria-live="polite" className="sr-only">{announcement}</div>
    </>
  )
}
