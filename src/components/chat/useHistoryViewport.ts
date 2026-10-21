import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'
import type { ChatMessage } from '@/store/chat'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { HistoryRowMeasurements, historyPageSizing, historyPixelTrigger, recordHistoryGeometry, forgetHistoryGeometry } from '@/lib/chat-history-sizing'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { protectHistoryPositions, releaseHistoryProtection } from '@/store/chat/history-merge'

export function drawnHistoryRows(message: ChatMessage, verbose: boolean): number {
  const tools = message.tool_calls?.filter(call => shouldRenderToolCall(call.tool, call.params, verbose, call.status === 'error')).length ?? 0
  return Number(!!message.content || (tools === 0 && message.role !== 'assistant')) + tools
}
type Anchor = { position: string; offset: number; alternatives: { position: string; screenTop: number }[] } // not-wire-format

/** One signed compensation/fill owner for virtual and plain layouts. */
export function useHistoryViewport(scrollRef: RefObject<HTMLDivElement | null>, messages: ChatMessage[]) {
  const sid = useSessionStore(s => s.activeSessionId)
  const history = useChatStore(s => s.history)
  const verbose = useChatPreferencesStore(s => s.verboseChatEnabled)
  const samples = useRef(new HistoryRowMeasurements())
  const heights = useRef(new Map<string, number>())
  const anchor = useRef<Anchor | null>(null)
  const priorFocus = useRef<Element | null>(null)
  const priorWidth = useRef(0)
  const lastTop = useRef(0)
  const compensating = useRef(false)
  const opening = useRef(true)
  const fill = useRef(false)
  const [average, setAverage] = useState(80)
  const [prefix, setPrefix] = useState(0)
  const current = useRef({ sid, history, messages, verbose })
  useLayoutEffect(() => { current.current = { sid, history, messages, verbose } })
  const rows = useCallback(() => [...(scrollRef.current?.querySelectorAll<HTMLElement>('[data-history-position]') ?? [])], [scrollRef])
  const offset = useCallback((element: Element) => {
    const scroller = scrollRef.current!
    return element.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
  }, [scrollRef])
  const capture = useCallback(() => {
    const scroller = scrollRef.current
    if (!scroller) return
    const top = scroller.getBoundingClientRect().top
    const visible = rows().filter(row => row.getBoundingClientRect().bottom > top)
    const first = visible[0]
    if (!first) return
    anchor.current = { position: first.dataset.historyPosition!, offset: offset(first), alternatives: visible.map(row => ({ position: row.dataset.historyPosition!, screenTop: row.getBoundingClientRect().top - top })) }
    const protectedPositions = new Set(visible.filter(row => row.getBoundingClientRect().top < top + 2 * scroller.clientHeight).map(row => row.dataset.historyPosition!))
    const focused = document.activeElement?.closest<HTMLElement>('[data-history-position]')
    if (focused?.dataset.historyPosition) protectedPositions.add(focused.dataset.historyPosition)
    if (current.current.sid) protectHistoryPositions(current.current.sid, protectedPositions)
    priorFocus.current = scroller.contains(document.activeElement) ? document.activeElement : null
  }, [offset, rows, scrollRef])
  const correct = useCallback(() => {
    const scroller = scrollRef.current
    const held = anchor.current
    if (!scroller || !held || current.current.history.viewMode !== 'historical') return
    const mounted = rows()
    let row = mounted.find(item => item.dataset.historyPosition === held.position)
    let delta = 0
    if (row) delta = offset(row) - held.offset
    else {
      const fallback = held.alternatives.find(item => mounted.some(element => element.dataset.historyPosition === item.position))
      row = fallback ? mounted.find(item => item.dataset.historyPosition === fallback.position) : undefined
      if (row && fallback) delta = row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - fallback.screenTop
    }
    if (Number.isFinite(delta) && delta !== 0) {
      compensating.current = true
      scroller.scrollTop += delta
      queueMicrotask(() => { compensating.current = false })
    }
    if (priorFocus.current && !priorFocus.current.isConnected) scroller.focus({ preventScroll: true })
    capture()
  }, [capture, offset, rows, scrollRef])
  const measure = useCallback(() => {
    const scroller = scrollRef.current
    const state = current.current
    if (!scroller || !state.sid) return
    const width = scroller.getBoundingClientRect().width
    if (priorWidth.current && Math.abs(width - priorWidth.current) > 1) { samples.current.reset(); heights.current.clear() }
    priorWidth.current = width
    for (const element of rows()) {
      const position = element.dataset.historyPosition!
      const message = state.messages.find(item => item.historyPosition === position)
      if (!message || message.isStreaming || message.status === 'streaming' || element.hidden || element.getAttribute('aria-hidden') === 'true') continue
      const height = element.getBoundingClientRect().height
      if (!Number.isFinite(height) || height <= 0) continue
      heights.current.set(position, height)
      // Measure drawn tool rows, not both the parent wrapper and its children.
      const tools = [...element.querySelectorAll<HTMLElement>('[data-tool]')].filter(node => !node.parentElement?.closest('[data-tool]'))
      let toolHeight = 0
      for (const [index, tool] of tools.entries()) {
        const value = tool.getBoundingClientRect().height
        toolHeight += value
        samples.current.observe(`${position}:tool:${index}`, value)
      }
      if (message.content) samples.current.observe(`${position}:text`, height - toolHeight)
      else if (tools.length === 0 && drawnHistoryRows(message, state.verbose) > 0) samples.current.observe(position, height)
    }
    const next = samples.current.average
    setAverage(value => value === next ? value : next)
    const content = scroller.querySelector('[data-history-content]')
    setPrefix(content ? offset(content) : 0)
    recordHistoryGeometry(state.sid, scroller.clientHeight, next)
  }, [offset, rows, scrollRef])

  const requestFill = useCallback(async (direction: 'older' | 'newer', initial = false) => {
    const scroller = scrollRef.current
    const first = current.current
    if (!scroller || !first.sid || fill.current || document.hidden || scroller.clientHeight <= 0) return
    const generation = first.history.viewGeneration
    const starting = new Set(first.history.readerPositions)
    const goal = (initial ? 4 : 2) * scroller.clientHeight
    fill.current = true
    try {
      for (;;) {
        const state = useChatStore.getState()
        const kept = state.sessionsById[first.sid]
        if (!kept || kept.history.viewGeneration !== generation || kept.history.requests.refresh.status !== 'idle' || kept.history.requests[direction].status !== 'idle') break
        const selected = current.current.messages.filter(message => message.historyPosition && (!starting.has(message.historyPosition) || initial))
        const added = selected.reduce((sum, message) => sum + (heights.current.get(message.historyPosition!) ?? drawnHistoryRows(message, current.current.verbose) * samples.current.average), 0)
        const remaining = Math.max(0, goal - added)
        if (remaining <= 0) break
        const before = direction === 'older' ? kept.history.olderBoundary : kept.history.newerBoundary
        const countBefore = kept.history.readerPositions.length
        const size = historyPageSizing(scroller.clientHeight, samples.current.average)
        size.limit = Math.min(100, Math.max(20, Math.ceil(remaining / samples.current.average)))
        size.min_rows = Math.min(size.limit, size.min_rows)
        await (direction === 'older' ? state.loadOlderHistory(first.sid, size) : state.loadNewerHistory(first.sid, size))
        const after = useChatStore.getState().sessionsById[first.sid]
        if (!after || after.history.requests[direction].status !== 'idle' || before === (direction === 'older' ? after.history.olderBoundary : after.history.newerBoundary)) break
        // No invisible-history walk: only positive newly rendered content can
        // sustain a chain. On reaching the window cap leave further browsing
        // to the next reader trigger after safe eviction.
        measure()
        const newRows = Object.values(after.messagesById).filter(message => message.historyPosition && !starting.has(message.historyPosition))
        if (!newRows.some(message => drawnHistoryRows(message, current.current.verbose) > 0) || countBefore === 500) break
        current.current = { ...current.current, messages: after.messageOrder.map(id => after.messagesById[id]), history: after.history }
      }
    } finally { fill.current = false; if (initial) opening.current = false }
  }, [measure, scrollRef])

  const onScroll = useCallback(() => {
    const scroller = scrollRef.current
    const state = current.current
    if (!scroller || !state.sid || compensating.current) return
    if (scroller.scrollTop < lastTop.current) useChatStore.getState().enterHistoryReading(state.sid)
    lastTop.current = scroller.scrollTop
    capture()
    if (state.history.requests.refresh.status !== 'idle') return
    if (historyPixelTrigger(scroller.scrollTop - prefix, scroller.clientHeight, !document.hidden)) void requestFill('older', opening.current)
    else if (historyPixelTrigger(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight, scroller.clientHeight, !document.hidden) && state.history.hasLater) void requestFill('newer')
  }, [capture, prefix, requestFill, scrollRef])
  useLayoutEffect(() => {
    samples.current.reset(); heights.current.clear(); anchor.current = null; opening.current = true; setAverage(80)
    return () => { if (sid) { forgetHistoryGeometry(sid); releaseHistoryProtection(sid) } }
  }, [sid, verbose])
  useLayoutEffect(() => {
    correct(); measure()
    const scroller = scrollRef.current
    if (scroller && history.historyLoaded && history.viewMode === 'latest') {
      scroller.scrollTop = scroller.scrollHeight
      capture()
      // A layoutless/plain fallback stays manually reachable rather than
      // fetching invisible content on a guessed height.
      if (opening.current && samples.current.count > 0) void requestFill('older', true)
    }
  })
  useLayoutEffect(() => {
    const scroller = scrollRef.current
    if (!scroller || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => { correct(); measure() })
    observer.observe(scroller)
    for (const row of rows()) observer.observe(row)
    const content = scroller.querySelector('[data-history-content]')
    if (content) observer.observe(content)
    return () => observer.disconnect()
  }, [messages, correct, measure, rows, scrollRef])
  const busy = Object.values(history.requests).some(request => request.status === 'loading')
  return { average, prefix, onScroll, busy, historical: history.viewMode === 'historical' }
}
