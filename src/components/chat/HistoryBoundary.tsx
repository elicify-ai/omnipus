import { useLayoutEffect, useRef } from 'react'
import { Button } from '@/components/ui/button'
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
export function HistoryBoundary({ action, average = 80 }: { action: HistoryActionKind; average?: number }) {
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
    if (ref.current) scroller.current = ref.current.closest<HTMLElement>('[aria-label="Conversation history"]')
  })
  const run = () => {
    if (!sid || request.status === 'loading') return
    const state = useChatStore.getState()
    if (request.error) { void state.retryHistory(sid, action); return }
    const region = ref.current?.closest<HTMLElement>('[aria-label="Conversation history"]')
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
  const unavailable = action === 'latest' && history.serverState?.status === 'unavailable'
  const visible = action === 'older' ? history.hasEarlier : action === 'newer' ? history.hasLater : action === 'latest' ? history.viewMode === 'historical' || unavailable : request.status !== 'idle'
  if (!visible && !request.error && request.status !== 'loading') return null
  const error = unavailable ? history.serverState!.message : request.error
  return (
    <div ref={ref} className="flex flex-col items-center gap-[var(--space-2)] py-[var(--space-2)]" onFocusCapture={event => { focused.current = event.target }} onBlurCapture={event => {
      if (event.relatedTarget && !ref.current?.contains(event.relatedTarget)) focused.current = null
    }} onClickCapture={event => {
      if (request.status === 'loading') { event.preventDefault(); event.stopPropagation() }
    }}>
      {action !== 'refresh' && !unavailable && (
        <Button variant="ghost" size="sm" aria-disabled={request.status === 'loading' || undefined} onClick={run}>
          {action === 'older' ? 'Show earlier messages' : action === 'newer' ? 'Show newer messages' : 'Return to latest'}
        </Button>
      )}
      {error && <ErrorState message={`${error}${request.status === 'error' && request.attempt > 1 ? ` Retry ${request.attempt} failed.` : ''}`} onRetry={run} className="py-[var(--space-1)]" />}
      {request.status === 'loading' && <>
        <Skeleton className="h-4 w-32" />
        <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{loading[action]}</p>
      </>}
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
