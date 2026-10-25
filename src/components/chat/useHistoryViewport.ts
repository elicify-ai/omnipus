import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'
import type { ChatMessage } from '@/store/chat'
import { emptyHistoryState, useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { HistoryRowMeasurements, historyPageSizing, historyPixelTrigger, recordHistoryGeometry, forgetHistoryGeometry } from '@/lib/chat-history-sizing'
import { shouldRenderJudgeVerdictInThread, shouldRenderToolCall } from '@/lib/toolVisibility'
import { protectHistoryPositions, releaseHistoryProtection } from '@/store/chat/history-merge'

export function drawnHistoryRows(message: ChatMessage, verbose: boolean): number {
  if (message.type === 'judge_verdict') return Number(shouldRenderJudgeVerdictInThread(verbose) && !!message.verdict)
  const tools = message.tool_calls?.filter(call => shouldRenderToolCall(call.tool, call.params, verbose, call.status === 'error')).length ?? 0
  return Number(!!message.content || (tools === 0 && message.role !== 'assistant')) + tools
}
/**
 * A row's unrounded layout (border-box) height. Unlike offsetHeight it keeps
 * the fractional part, so many measured rows do not add up to a prefix that is
 * off by their rounding (D5: a 111.48px row read as 111 made 40 rows 19px
 * short). Unlike a DOMRect of a translateY-placed virtual row it does not move
 * with the row's offset (#185). Falls back to offsetHeight where layout gives
 * no number (no stylesheet, display:contents).
 */
export function historyLayoutHeight(element: HTMLElement): number {
  const style = getComputedStyle(element)
  const height = Number.parseFloat(style.height)
  if (!Number.isFinite(height)) return element.offsetHeight
  if (style.boxSizing === 'border-box') return height
  const edges = [style.paddingTop, style.paddingBottom, style.borderTopWidth, style.borderBottomWidth]
  return height + edges.reduce((sum, value) => sum + (Number.parseFloat(value) || 0), 0)
}
type Anchor = { position: string; offset: number; alternatives: { position: string; screenTop: number }[] } // not-wire-format

function bindReaderIntent(scroller: HTMLElement, enter: () => void, dragging: { current: boolean }): () => void {
  let touchY: number | null = null
  const wheel = (event: WheelEvent) => { if (event.deltaY < 0 && !event.ctrlKey) enter() }
  const key = (event: KeyboardEvent) => {
    if (!event.defaultPrevented && ['ArrowUp', 'PageUp', 'Home'].includes(event.key) && !(event.target instanceof Element && event.target.closest('input, textarea, [contenteditable="true"], [role="combobox"]'))) enter()
  }
  const touchStart = (event: TouchEvent) => { touchY = event.touches.length === 1 ? event.touches[0].clientY : null }
  const touchMove = (event: TouchEvent) => {
    const y = event.touches.length === 1 ? event.touches[0].clientY : null
    if (y !== null && touchY !== null && y > touchY) enter()
    touchY = y
  }
  const pointerDown = (event: PointerEvent) => { dragging.current = event.target === scroller }
  const pointerEnd = () => { dragging.current = false }
  scroller.addEventListener('wheel', wheel, { passive: true })
  scroller.addEventListener('keydown', key)
  scroller.addEventListener('touchstart', touchStart, { passive: true })
  scroller.addEventListener('touchmove', touchMove, { passive: true })
  scroller.addEventListener('pointerdown', pointerDown)
  document.addEventListener('pointerup', pointerEnd)
  document.addEventListener('pointercancel', pointerEnd)
  window.addEventListener('blur', pointerEnd)
  return () => {
    scroller.removeEventListener('wheel', wheel); scroller.removeEventListener('keydown', key)
    scroller.removeEventListener('touchstart', touchStart); scroller.removeEventListener('touchmove', touchMove)
    scroller.removeEventListener('pointerdown', pointerDown); document.removeEventListener('pointerup', pointerEnd)
    document.removeEventListener('pointercancel', pointerEnd); window.removeEventListener('blur', pointerEnd)
    pointerEnd()
  }
}

/** One signed compensation/fill owner for virtual and plain layouts. */
export function useHistoryViewport(scrollRef: RefObject<HTMLDivElement | null>, messages: ChatMessage[], anchorOffset?: (position: string) => number | undefined) {
  const sid = useSessionStore(s => s.activeSessionId)
  const history = useChatStore(s => s.history)
  const catchingUp = useChatStore(s => s.isReplaying || (sid ? s.sessionsById[sid]?.awaitingCatchUp === true : false))
  const verbose = useChatPreferencesStore(s => s.verboseChatEnabled)
  const samples = useRef(new HistoryRowMeasurements())
  const heights = useRef(new Map<string, number>())
  const anchor = useRef<Anchor | null>(null)
  const priorFocus = useRef<Element | null>(null)
  const priorWidth = useRef(0)
  const lastTop = useRef(0)
  const dragging = useRef(false)
  const compensating = useRef(false)
  // scrollTop the anchor bookkeeping last accounted for (capture or correction).
  const settledTop = useRef<number | null>(null)
  // Last scrollTop an application-owned placement produced (latest pin,
  // virtualizer adjustment). Such movement never ends follow-latest.
  const ownedTop = useRef<number | null>(null)
  const opening = useRef(true)
  const fill = useRef<{ frame: number | null; finishLayout: (() => void) | null } | null>(null)
  const [average, setAverage] = useState(80)
  const [prefix, setPrefix] = useState(0)
  const [widthGeneration, bumpWidthGeneration] = useState(0)
  const current = useRef({ sid, history, messages, verbose, anchorOffset })
  useLayoutEffect(() => { current.current = { sid, history, messages, verbose, anchorOffset } })
  const rows = useCallback(() => [...(scrollRef.current?.querySelectorAll<HTMLElement>('[data-history-anchor]') ?? [])], [scrollRef])
  const offset = useCallback((element: Element) => {
    const scroller = scrollRef.current!
    return element.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
  }, [scrollRef])
  const capture = useCallback(() => {
    const scroller = scrollRef.current
    if (!scroller) return
    const top = scroller.getBoundingClientRect().top
    const mounted = rows().filter(row => row.getBoundingClientRect().height > 0)
    // Only a row actually inside the viewport can anchor the reader. Right
    // after a jump the virtualizer may still show rows far below it.
    const visible = mounted.filter(row => row.getBoundingClientRect().bottom > top && row.getBoundingClientRect().top < top + scroller.clientHeight)
    const first = visible[0]
    if (!first) return
    const alternatives = [...visible, ...mounted.filter(row => row.getBoundingClientRect().bottom <= top).reverse()]
    settledTop.current = scroller.scrollTop
    anchor.current = { position: first.dataset.historyAnchor!, offset: offset(first), alternatives: alternatives.map(row => ({ position: row.dataset.historyAnchor!, screenTop: row.getBoundingClientRect().top - top })) }
    const protectedPositions = new Set(visible.filter(row => row.dataset.historyPosition && row.getBoundingClientRect().top < top + 2 * scroller.clientHeight).map(row => row.dataset.historyPosition!))
    const focused = document.activeElement?.closest<HTMLElement>('[data-history-position]')
    if (focused?.dataset.historyPosition) protectedPositions.add(focused.dataset.historyPosition)
    if (current.current.sid) protectHistoryPositions(current.current.sid, protectedPositions)
    priorFocus.current = scroller.contains(document.activeElement) ? document.activeElement : null
  }, [offset, rows, scrollRef])
  const correct = useCallback(() => {
    const scroller = scrollRef.current
    const held = anchor.current
    if (!scroller || current.current.history.viewMode !== 'historical') return
    // A genuine reader move dropped the old anchor; adopt the first row that
    // is now really visible as soon as the virtualizer has drawn one.
    if (!held) { capture(); return }
    // Content shrinking at the scroll end makes the browser clamp scrollTop.
    // That is not reader movement: count it as not yet applied, so the anchor
    // is restored as soon as the content allows (and a shrink above the reader
    // that the clamp already compensated is not corrected a second time).
    if (settledTop.current !== null) {
      const moved = scroller.scrollTop - settledTop.current
      if (moved < 0 && scroller.scrollTop >= scroller.scrollHeight - scroller.clientHeight - 1) held.offset += moved
    }
    settledTop.current = scroller.scrollTop
    const mounted = rows()
    let row = mounted.find(item => item.dataset.historyAnchor === held.position)
    let delta = 0
    let estimatedOnly = false
    if (row) delta = offset(row) - held.offset
    else {
      // A prepend can move the retained anchor outside the mounted range before
      // this layout effect. Use the real virtualizer's current measured/estimated
      // item offset, not total scrollHeight or a guessed source rank.
      const estimated = current.current.anchorOffset?.(held.position)
      if (estimated !== undefined && Number.isFinite(estimated)) { delta = estimated - held.offset; estimatedOnly = true }
      else {
        const fallback = held.alternatives.find(item => mounted.some(element => element.dataset.historyAnchor === item.position))
        row = fallback ? mounted.find(item => item.dataset.historyAnchor === fallback.position) : undefined
        if (row && fallback) delta = row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - fallback.screenTop
      }
    }
    const exact = row !== undefined && row.dataset.historyAnchor === held.position
    let residual = delta
    if (Number.isFinite(delta) && delta !== 0) {
      compensating.current = true
      const before = scroller.scrollTop
      const top = before + delta
      if (typeof scroller.scrollTo === 'function') scroller.scrollTo({ top, behavior: 'instant' })
      else scroller.scrollTop = top
      // Consume only what the browser actually applied, so the next layout
      // pass neither applies this delta twice nor forgets the part a scroll
      // edge clamp or device-pixel snapping held back.
      const applied = scroller.scrollTop - before
      held.offset += applied
      residual = delta - applied
      settledTop.current = scroller.scrollTop
      queueMicrotask(() => { compensating.current = false })
    }
    if (priorFocus.current && !priorFocus.current.isConnected) scroller.focus({ preventScroll: true })
    // Re-anchor only once the held row sits where it belongs. Re-capturing the
    // first visible row at an estimated or clamped position would turn that
    // error into permanent drift.
    // At the top edge a negative remainder can never be applied (content
    // above shrank while the reader sits at scrollTop 0): accept the position
    // rather than letting that stale remainder distort the next correction.
    const unreachable = residual < 0 && scroller.scrollTop <= 0
    if (!estimatedOnly && (!exact || !Number.isFinite(residual) || Math.abs(residual) < 1 || unreachable)) capture()
  }, [capture, offset, rows, scrollRef])
  const measure = useCallback(() => {
    const scroller = scrollRef.current
    const state = current.current
    if (!scroller || !state.sid) return
    const stablePositions = new Set(state.messages.filter(message => message.historyPosition && !message.isStreaming && message.status !== 'streaming').map(message => message.historyPosition!))
    for (const position of heights.current.keys()) if (!stablePositions.has(position)) heights.current.delete(position)
    const width = scroller.getBoundingClientRect().width
    if (priorWidth.current && Math.abs(width - priorWidth.current) > 1) { samples.current.reset(); heights.current.clear(); bumpWidthGeneration(value => value + 1) }
    priorWidth.current = width
    for (const element of rows()) {
      const position = element.dataset.historyPosition
      if (!position) continue // Live DOM anchors are not public page selectors or history samples.
      const message = state.messages.find(item => item.historyPosition === position)
      if (!message || message.isStreaming || message.status === 'streaming' || element.hidden || element.getAttribute('aria-hidden') === 'true') {
        stablePositions.delete(position); heights.current.delete(position); continue
      }
      // Sample the transform-independent layout size the virtualizer itself
      // measures. A translated row's DOMRect height shifts by float rounding
      // with its offset (685.234375 vs 685.23388671875 near 10,000px), so an
      // unchanged row changed A -> virtualizer.measure() -> new translateY ->
      // new A, until React's update-depth limit (#185) unmounted the list.
      const height = historyLayoutHeight(element)
      if (!Number.isFinite(height) || height < 0) { stablePositions.delete(position); heights.current.delete(position); continue }
      heights.current.set(position, height)
      if (height === 0) { stablePositions.delete(position); continue }
      // One complete tool row includes its expanded body, not only a toggle.
      const tools = [...element.querySelectorAll<HTMLElement>('[data-history-tool-row]')].map(node => ({ key: node.dataset.historyToolRow!, height: historyLayoutHeight(node) })).filter(row => Number.isFinite(row.height) && row.height > 0)
      const toolHeight = tools.reduce((sum, tool) => sum + tool.height, 0)
      const overhead = !message.content && tools.length ? Math.max(0, height - toolHeight) / tools.length : 0
      for (const tool of tools) samples.current.observe(`${position}:tool:${tool.key}`, tool.height + overhead)
      if (message.content) samples.current.observe(`${position}:text`, height - toolHeight)
      else if (tools.length === 0 && drawnHistoryRows(message, state.verbose) > 0) samples.current.observe(position, height)
    }
    samples.current.retainPositions(stablePositions)
    const next = samples.current.average
    setAverage(value => value === next ? value : next)
    const content = scroller.querySelector('[data-history-content]')
    setPrefix(content ? offset(content) : 0)
    recordHistoryGeometry(state.sid, scroller.clientHeight, next)
  }, [offset, rows, scrollRef])

  const requestFill = useCallback(async (direction: 'older' | 'newer', initial = false) => {
    const scroller = scrollRef.current
    const sessionId = current.current.sid
    if (!scroller || !sessionId || fill.current || document.hidden || scroller.clientHeight <= 0) return
    const generation = current.current.history.viewGeneration
    const starting = new Set(current.current.history.readerPositions)
    const owner = { frame: null as number | null, finishLayout: null as (() => void) | null }
    fill.current = owner
    try {
      for (;;) {
        const state = useChatStore.getState()
        const kept = state.sessionsById[sessionId]
        if (fill.current !== owner || current.current.sid !== sessionId || document.hidden || scroller.clientHeight <= 0 || !kept || kept.isReplaying || kept.awaitingCatchUp || kept.history.viewGeneration !== generation || kept.history.requests.refresh.status !== 'idle' || kept.history.requests[direction].status === 'error') break
        const selected = current.current.messages.filter(message => message.historyPosition && (!starting.has(message.historyPosition) || initial))
        const added = selected.reduce((sum, message) => sum + (heights.current.get(message.historyPosition!) ?? drawnHistoryRows(message, current.current.verbose) * samples.current.average), 0)
        const remaining = Math.max(0, (initial ? 4 : 2) * scroller.clientHeight - added)
        if (remaining <= 0) break
        const before = direction === 'older' ? kept.history.olderBoundary : kept.history.newerBoundary
        const previousPositions = new Set(kept.history.readerPositions)
        const size = historyPageSizing(scroller.clientHeight, samples.current.average)
        size.limit = Math.min(100, Math.max(20, Math.ceil(remaining / samples.current.average)))
        size.min_rows = Math.min(size.limit, size.min_rows)
        await (direction === 'older' ? state.loadOlderHistory(sessionId, size) : state.loadNewerHistory(sessionId, size))
        if (fill.current !== owner || document.hidden) break
        // External-store commits must reach layout before newly drawn heights
        // can decide whether this same fill owner needs a contiguous top-up.
        await new Promise<void>(resolve => {
          owner.finishLayout = resolve
          owner.frame = requestAnimationFrame(() => { owner.frame = null; owner.finishLayout = null; resolve() })
        })
        if (fill.current !== owner || current.current.sid !== sessionId || document.hidden) break
        const after = useChatStore.getState().sessionsById[sessionId]
        if (!after || after.history.viewGeneration !== generation || after.history.requests[direction].status !== 'idle' || before === (direction === 'older' ? after.history.olderBoundary : after.history.newerBoundary)) break
        measure()
        const newRows = current.current.messages.filter(message => message.historyPosition && !previousPositions.has(message.historyPosition))
        if (!newRows.some(message => (heights.current.get(message.historyPosition!) ?? drawnHistoryRows(message, current.current.verbose) * samples.current.average) > 0) || previousPositions.size === 500) break
      }
    } finally {
      if (fill.current === owner) { fill.current = null; if (initial) opening.current = false }
    }
  }, [measure, scrollRef])

  const cancelFill = useCallback(() => {
    const owner = fill.current
    fill.current = null
    if (owner?.frame !== null && owner?.frame !== undefined) cancelAnimationFrame(owner.frame)
    owner?.finishLayout?.()
    if (owner) { owner.frame = null; owner.finishLayout = null }
  }, [])
  const onScroll = useCallback(() => {
    const scroller = scrollRef.current
    const state = current.current
    if (!scroller || !state.sid || compensating.current) return
    // Leaving follow-latest is decided by actual displacement away from the
    // live end (scrollbar, Find, momentum, keys, wheel, programmatic), unless
    // the application placed the viewport itself. Content growth leaves
    // scrollTop unchanged and fires no scroll, so it never ends following.
    const owned = ownedTop.current !== null && Math.abs(scroller.scrollTop - ownedTop.current) < 1
    const awayFromEnd = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight > 1
    if (state.history.viewMode === 'latest' && !owned && awayFromEnd && (scroller.scrollTop < lastTop.current - 1 || dragging.current)) {
      useChatStore.getState().enterHistoryReading(state.sid)
    }
    lastTop.current = scroller.scrollTop
    // Our own correction's scroll event arrives a frame later, when a row
    // measurement may already have moved the content again: keep holding the
    // same anchor then. Any other position is genuine reader movement.
    const own = settledTop.current !== null && Math.abs(scroller.scrollTop - settledTop.current) < 0.5
    if (own && state.history.viewMode === 'historical') correct()
    else {
      // Reader movement replaces the anchor. If no row is drawn at the new
      // position yet, hold none rather than keep correcting toward the old one.
      anchor.current = null
      capture()
    }
    if (state.history.requests.refresh.status !== 'idle') return
    // D5 Dtop: distance from the viewport top to the earliest loaded content
    // boundary INCLUDING the prefix controls above the first row, i.e. scrollTop.
    if (historyPixelTrigger(scroller.scrollTop, scroller.clientHeight, !document.hidden)) void requestFill('older', opening.current)
    else if (historyPixelTrigger(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight, scroller.clientHeight, !document.hidden) && state.history.hasLater) void requestFill('newer')
  }, [capture, correct, requestFill, scrollRef])
  useLayoutEffect(() => {
    samples.current.reset(); heights.current.clear(); anchor.current = null; settledTop.current = null; ownedTop.current = null; opening.current = true
    priorFocus.current = null; priorWidth.current = 0; lastTop.current = 0; setAverage(80); setPrefix(0)
    return () => {
      cancelFill(); samples.current.reset(); heights.current.clear(); anchor.current = null; priorFocus.current = null
      current.current = { sid: null, history: emptyHistoryState(), messages: [], verbose, anchorOffset: undefined }
      if (sid) { forgetHistoryGeometry(sid); releaseHistoryProtection(sid) }
    }
  }, [sid, verbose, cancelFill])
  useLayoutEffect(() => {
    const scroller = scrollRef.current
    if (!scroller || !sid) return
    return bindReaderIntent(scroller, () => useChatStore.getState().enterHistoryReading(sid), dragging)
  }, [sid, scrollRef])
  useLayoutEffect(() => {
    const visibility = () => {
      if (document.hidden) cancelFill()
      else { measure(); if (opening.current && current.current.history.viewMode === 'latest' && samples.current.count > 0) void requestFill('older', true) }
    }
    document.addEventListener('visibilitychange', visibility)
    return () => document.removeEventListener('visibilitychange', visibility)
  }, [cancelFill, measure, requestFill])
  useLayoutEffect(() => {
    cancelFill()
    if (current.current.history.viewMode === 'latest') opening.current = true
  }, [history.viewGeneration, cancelFill])
  useLayoutEffect(() => {
    correct(); measure()
    const scroller = scrollRef.current
    if (scroller && history.historyLoaded && history.viewMode === 'latest' && !catchingUp) {
      scroller.scrollTop = scroller.scrollHeight
      ownedTop.current = scroller.scrollTop
      lastTop.current = scroller.scrollTop
      capture()
      // A layoutless/plain fallback stays manually reachable rather than
      // fetching invisible content on a guessed height.
      if (opening.current && samples.current.count > 0) void requestFill('older', true)
    }
  })
  useLayoutEffect(() => {
    const scroller = scrollRef.current
    if (!scroller) return
    const settle = () => { correct(); measure() }
    scroller.addEventListener('load', settle, true); scroller.addEventListener('error', settle, true)
    window.addEventListener('resize', settle); document.fonts?.addEventListener('loadingdone', settle)
    let observer: ResizeObserver | MutationObserver | undefined
    if (typeof ResizeObserver !== 'undefined') {
      const resize = new ResizeObserver(settle)
      resize.observe(scroller)
      for (const row of rows()) resize.observe(row)
      const content = scroller.querySelector('[data-history-content]')
      if (content) resize.observe(content)
      observer = resize
    } else if (typeof MutationObserver !== 'undefined') {
      const mutations = new MutationObserver(settle)
      mutations.observe(scroller, { subtree: true, childList: true, attributes: true, attributeFilter: ['style', 'class', 'hidden', 'open'] })
      observer = mutations
    }
    return () => {
      observer?.disconnect(); scroller.removeEventListener('load', settle, true); scroller.removeEventListener('error', settle, true)
      window.removeEventListener('resize', settle); document.fonts?.removeEventListener('loadingdone', settle)
    }
  }, [messages, correct, measure, rows, scrollRef])
  const busy = Object.values(history.requests).some(request => request.status === 'loading')
  /** Record a placement the list itself made (e.g. a virtualizer size adjustment). */
  const claimScroll = useCallback(() => {
    const scroller = scrollRef.current
    if (scroller) ownedTop.current = scroller.scrollTop
  }, [scrollRef])
  return { average, prefix, widthGeneration, onScroll, claimScroll, busy, historical: history.viewMode === 'historical' }
}
