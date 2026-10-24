import { useLayoutEffect, useRef } from 'react'
import { ArrowDown } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { HistoryActionKind } from '@/store/chat/types'
import { historyPageSizing } from '@/lib/chat-history-sizing'
import { reachedClosedClear } from '@/store/chat/history-merge'

const loading: Record<HistoryActionKind, string> = {
  older: 'Loading earlier messages…', newer: 'Loading newer messages…', latest: 'Loading latest messages…', refresh: 'Refreshing loaded messages…',
}
/** Boundary state composes catalog controls; it owns no competing read controller. */
/** The labelled history scroller that contains this boundary. */
function historyScroller(element: HTMLElement | null): HTMLElement | null {
  return element?.closest<HTMLElement>('[aria-label="Conversation history"]') ?? null
}

/**
 * `placement="floating"` (action latest only) is the founder-ruled Return to
 * latest: a floating arrow button stuck to the bottom of the chat area, above
 * the composer, so it is always visible while reading
 * older history and focusing or activating it never scrolls the reader. It
 * owns that action's loading/error/Retry states. The in-history placement then
 * keeps only the unavailable-history recovery, plus latest-read states while
 * not reading older history.
 */
export function HistoryBoundary({ action, average = 80, placement = 'history' }: { action: HistoryActionKind; average?: number; placement?: 'history' | 'floating' }) {
  const sid = useSessionStore(s => s.activeSessionId)
  const history = useChatStore(s => s.history)
  const request = history.requests[action]
  const ref = useRef<HTMLDivElement>(null)
  const focused = useRef<Element | null>(null)
  const scroller = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => {
    if (focused.current && !focused.current.isConnected) {
      scroller.current?.focus({ preventScroll: true })
      focused.current = null
    }
    if (ref.current) scroller.current = historyScroller(ref.current)
  })
  const run = () => {
    if (!sid) return
    const state = useChatStore.getState()
    const current = state.sessionsById[sid]?.history.requests[action]
    if (current?.status === 'loading') return
    // Return is an explicit latest read, even while history recovery is unavailable.
    if (action !== 'latest' && current?.error) { void state.retryHistory(sid, action); return }
    const region = historyScroller(ref.current)
    const size = historyPageSizing(region?.clientHeight || 800, average)
    if (action === 'older') {
      state.enterHistoryReading(sid)
      const bucket = state.sessionsById[sid]
      const clear = bucket && reachedClosedClear(bucket)
      void (clear ? state.showEarlierHistory(sid, clear.historyPosition, size) : state.loadOlderHistory(sid, size))
    } else if (action === 'newer') void state.loadNewerHistory(sid, size)
    else if (action === 'latest') void state.returnToLatestHistory(sid, { ...size, limit: Math.min(100, Math.max(20, Math.ceil(4 * (region?.clientHeight || 800) / average))) })
    else void state.refreshLoadedHistory(sid)
  }
  const recover = () => {
    if (!sid) return
    const state = useChatStore.getState()
    const current = state.sessionsById[sid]?.history
    if (current?.serverState?.status !== 'unavailable' || current.requests.latest.status === 'loading') return
    // Recovery requests a snapshot; it does not request leaving the older reader.
    void state.retryHistory(sid, 'latest')
  }
  const reading = history.viewMode === 'historical'
  const unavailable = action === 'latest' && history.serverState?.status === 'unavailable'
  const unavailableMessage = unavailable ? history.serverState?.message : undefined
  const floating = action === 'latest' && placement === 'floating'
  // While reading older history the floating control owns Return and its read states.
  const latestStatesHere = action !== 'latest' || floating || !reading
  if (floating && !reading) return null
  const visible = action === 'older' ? history.hasEarlier : action === 'newer' ? history.hasLater : action === 'latest' ? floating || unavailable : request.status !== 'idle'
  if (!visible && !(latestStatesHere && (request.error || request.status === 'loading'))) return null
  const states = latestStatesHere && <>
    {request.error && <div key="history-read-error" role="group" aria-label={action === 'latest' ? 'Latest messages read' : 'History read'}>
      <ErrorState message={`${request.error}${request.status === 'error' && request.attempt > 1 ? ` Retry ${request.attempt} failed.` : ''}`} onRetry={run} retryDisabled={request.status === 'loading'} className="py-[var(--space-1)]" />
    </div>}
    {request.status === 'loading' && <>
      <Skeleton className="h-4 w-32" />
      <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{loading[action]}</p>
    </>}
  </>
  if (floating) return (
    <div ref={ref} className="pointer-events-auto flex flex-col items-center gap-[var(--space-1)] rounded-[var(--radius-medium)] bg-[var(--color-surface-1)] p-[var(--space-1)] shadow-[var(--elevation-floating)]" onFocusCapture={event => { focused.current = event.target }} onBlurCapture={event => {
      if (event.relatedTarget && !ref.current?.contains(event.relatedTarget)) focused.current = null
    }} onClickCapture={event => {
      if (request.status === 'loading') { event.preventDefault(); event.stopPropagation() }
    }}>
      {states}
      <IconButton aria-label="Return to latest" variant="outline" aria-disabled={request.status === 'loading' || undefined} onClick={run}>
        <ArrowDown size={16} aria-hidden="true" />
      </IconButton>
    </div>
  )
  return (
    <div ref={ref} className="flex flex-col items-center gap-[var(--space-2)] py-[var(--space-2)]" onFocusCapture={event => { focused.current = event.target }} onBlurCapture={event => {
      if (event.relatedTarget && !ref.current?.contains(event.relatedTarget)) focused.current = null
    }} onClickCapture={event => {
      if (request.status === 'loading') { event.preventDefault(); event.stopPropagation() }
    }}>
      {action !== 'refresh' && action !== 'latest' && (
        <Button variant="ghost" size="sm" aria-disabled={request.status === 'loading' || undefined} onClick={run}>
          {action === 'older' ? 'Show earlier messages' : action === 'newer' ? 'Show newer messages' : 'Return to latest'}
        </Button>
      )}
      {unavailableMessage && <div key="history-recovery" role="group" aria-label="History recovery">
        <ErrorState message={unavailableMessage} onRetry={recover} retryDisabled={request.status === 'loading'} className="py-[var(--space-1)]" />
      </div>}
      {states}
    </div>
  )
}

/**
 * Floating slot for Return to latest: the last child of the scrolled content,
 * stuck to the bottom of the visible chat area (above the composer, which is
 * outside the scroller). Being always in view, focusing it never scrolls the
 * reader, and its states stay inside the labelled Conversation history.
 */
export function FloatingReturnToLatest() {
  return (
    <div className="pointer-events-none sticky bottom-[var(--space-2)] z-10 flex justify-center">
      <HistoryBoundary action="latest" placement="floating" />
    </div>
  )
}

export function HistoryReaderStatus() {
  const history = useChatStore(s => s.history)
  const notice = useChatStore(s => s.historyNotice)
  return <>
    <HistoryBoundary action="refresh" />
    {history.requests.refresh.error && <p className="px-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Loaded messages may be out of date.</p>}
    {notice && <p role="status" className="px-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{notice}</p>}
    <HistoryBoundary action="latest" />
  </>
}
