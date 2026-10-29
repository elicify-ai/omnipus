import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { RefObject } from 'react'
import { useRouter } from '@tanstack/react-router'
import type { ChatMessage } from '@/store/chat'
import { emptyHistoryState, useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { HistoryRowMeasurements, historyPageSizing, historyPixelTrigger, recordHistoryGeometry, forgetHistoryGeometry } from '@/lib/chat-history-sizing'
import { shouldRenderJudgeVerdictInThread, shouldRenderToolCall } from '@/lib/toolVisibility'
import { protectHistoryPositions, releaseHistoryProtection } from '@/store/chat/history-merge'
import { logDiagnostic } from '@/lib/telemetry'

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
type FillDirection = 'older' | 'newer'
type FillOwner = { frame: number | null; finishLayout: (() => void) | null } // not-wire-format
type Anchor = { position: string; offset: number; part?: { position: string; offset: number }; alternatives: { position: string; screenTop: number }[] } // not-wire-format

/** A row's drawn source parts (#1452 S11 reading position): the `data-history-part` markers that name a position. */
function partMarkers(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>('[data-history-part]')].filter(part => !!part.dataset.historyPart)
}
/** The drawn marker of one source position, if mounted. */
function partMarker(root: ParentNode, position: string): HTMLElement | undefined {
  return root.querySelector<HTMLElement>(`[data-history-part="${position.replace(/["\\]/g, '\\$&')}"]`) ?? undefined
}

/**
 * A discarded history read must never become an unhandled rejection. The
 * store's read shows its own action-specific error and Retry first and then
 * re-throws a raw TypeError (an internal fault, not a transport error); the
 * viewport discards these promises, so the fault is reported here instead of
 * vanishing.
 */
function logViewportReadRejection(action: FillDirection | 'latest', error: unknown): void {
  logDiagnostic('chatHistoryReadRejected', { action, error: error instanceof Error ? error.name : 'unknown' })
}

/**
 * Reader intent from real input. `downward` is armed by the reader's own
 * downward input (wheel, ArrowDown/PageDown/End/Space, a touch swipe up) and
 * disarmed by upward input: only an armed reader may resume follow-latest by
 * reaching the end of the loaded window. A scroll with no reader input behind
 * it (the list's own placement, a measurement re-anchor, a restore) never
 * counts as the reader returning to the bottom.
 */
function bindReaderIntent(scroller: HTMLElement, enterReading: () => void, dragging: { current: boolean }, downward: { current: boolean }): () => void {
  // Intent to read earlier content means something only when there is earlier
  // content to scroll to: a conversation that fits never leaves the live end,
  // so it must not show Return to latest (final-gate architect item A).
  const enter = () => { if (scroller.scrollHeight > scroller.clientHeight) enterReading() }
  const up = () => { downward.current = false; enter() }
  let touchY: number | null = null
  const wheel = (event: WheelEvent) => {
    if (event.ctrlKey) return
    if (event.deltaY < 0) up()
    else if (event.deltaY > 0) downward.current = true
  }
  const key = (event: KeyboardEvent) => {
    if (event.defaultPrevented || (event.target instanceof Element && event.target.closest('input, textarea, [contenteditable="true"], [role="combobox"]'))) return
    if (['ArrowUp', 'PageUp', 'Home'].includes(event.key)) up()
    else if (event.key === ' ' && event.shiftKey) downward.current = false
    else if (['ArrowDown', 'PageDown', 'End', ' '].includes(event.key)) downward.current = true
  }
  const touchStart = (event: TouchEvent) => { touchY = event.touches.length === 1 ? event.touches[0].clientY : null }
  const touchMove = (event: TouchEvent) => {
    const y = event.touches.length === 1 ? event.touches[0].clientY : null
    if (y !== null && touchY !== null && y > touchY) up()
    else if (y !== null && touchY !== null && y < touchY) downward.current = true
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

type AnchorRefs = { // not-wire-format: the viewport's anchor bookkeeping refs
  anchor: RefObject<Anchor | null>; settledTop: RefObject<number | null>; compensating: RefObject<boolean>; priorFocus: RefObject<Element | null>
}
type ViewportState = { // not-wire-format: the latest rendered inputs, read by async fill/settle work
  sid: string | null; history: ReturnType<typeof emptyHistoryState>; messages: ChatMessage[]; verbose: boolean; anchorOffset?: (position: string) => number | undefined
}
type FillEnv = { // not-wire-format
  scroller: HTMLElement; current: RefObject<ViewportState>; fills: RefObject<Record<FillDirection, FillOwner | null>>
  heights: RefObject<Map<string, number>>; samples: RefObject<HistoryRowMeasurements>; opening: RefObject<boolean>
  evaluateFill: RefObject<(direction: FillDirection) => void>; measure: () => void
}

/** Hold the first row really inside the viewport as the reader's anchor. */
function captureAnchor(scroller: HTMLElement, rows: HTMLElement[], offset: (element: Element) => number, refs: AnchorRefs, sid: string | null): void {
  const top = scroller.getBoundingClientRect().top
  const mounted = rows.filter(row => row.getBoundingClientRect().height > 0)
  // Only a row actually inside the viewport can anchor the reader. Right
  // after a jump the virtualizer may still show rows far below it.
  const visible = mounted.filter(row => row.getBoundingClientRect().bottom > top && row.getBoundingClientRect().top < top + scroller.clientHeight)
  const within = (element: Element, band: number) => element.getBoundingClientRect().bottom > top && element.getBoundingClientRect().top < top + band
  // S11: a re-anchor after our own correction keeps the held source part
  // while it is still in view, instead of whatever row a not yet re-laid-out
  // list shows first.
  const heldPart = refs.anchor.current?.part ? partMarker(scroller, refs.anchor.current.part.position) : undefined
  const kept = heldPart && within(heldPart, scroller.clientHeight) ? heldPart : undefined
  const keptRow = kept?.closest<HTMLElement>('[data-history-anchor]') ?? undefined
  const first = keptRow ?? visible[0]
  if (!first) return
  const alternatives = [...visible, ...mounted.filter(row => row.getBoundingClientRect().bottom <= top).reverse()]
  refs.settledTop.current = scroller.scrollTop
  // Inside a row that combines several sources, the reader's point is the
  // topmost visible source part, not the row's own (first) position.
  const part = kept ?? partMarkers(first).find(item => within(item, scroller.clientHeight))
  refs.anchor.current = { position: first.dataset.historyAnchor!, offset: offset(first), ...(part ? { part: { position: part.dataset.historyPart!, offset: offset(part) } } : {}),
    alternatives: alternatives.map(row => ({ position: row.dataset.historyAnchor!, screenTop: row.getBoundingClientRect().top - top })) }
  const protectedPositions = new Set(visible.filter(row => row.getBoundingClientRect().top < top + 2 * scroller.clientHeight).flatMap(row => {
    const parts = partMarkers(row)
    if (parts.length) return parts.filter(item => within(item, 2 * scroller.clientHeight)).map(item => item.dataset.historyPart!)
    return row.dataset.historyPosition ? [row.dataset.historyPosition] : []
  }))
  const focusedPart = document.activeElement?.closest<HTMLElement>('[data-history-part]')
  const focused = focusedPart?.dataset.historyPart ? undefined : document.activeElement?.closest<HTMLElement>('[data-history-position]')
  if (focusedPart?.dataset.historyPart) protectedPositions.add(focusedPart.dataset.historyPart)
  if (focused?.dataset.historyPosition) protectedPositions.add(focused.dataset.historyPosition)
  if (sid) protectHistoryPositions(sid, protectedPositions)
  refs.priorFocus.current = scroller.contains(document.activeElement) ? document.activeElement : null
}

// A correction's own scroll can synchronously re-render the list, whose
// layout pass would correct again from the not-yet-advanced anchor and apply
// the same delta twice. The outer correction settles it instead.
const correcting = new WeakSet<HTMLElement>()

/** Signed compensation: put the held anchor back where the reader saw it. */
function correctAnchor(scroller: HTMLElement, rows: () => HTMLElement[], offset: (element: Element) => number, refs: AnchorRefs,
  anchorOffset: ((position: string) => number | undefined) | undefined, capture: () => void, partRow?: (position: string) => string | undefined): void {
  const held = refs.anchor.current
  if (correcting.has(scroller)) return
  // A genuine reader move dropped the old anchor; adopt the first row that
  // is now really visible as soon as the virtualizer has drawn one.
  if (!held) { capture(); return }
  // Content shrinking at the scroll end makes the browser clamp scrollTop.
  // That is not reader movement: count it as not yet applied, so the anchor
  // is restored as soon as the content allows (and a shrink above the reader
  // that the clamp already compensated is not corrected a second time).
  if (refs.settledTop.current !== null) {
    const moved = scroller.scrollTop - refs.settledTop.current
    if (moved < 0 && scroller.scrollTop >= scroller.scrollHeight - scroller.clientHeight - 1) held.offset += moved
  }
  const observedTop = scroller.scrollTop
  const mounted = rows()
  let row = mounted.find(item => item.dataset.historyAnchor === held.position)
  let delta = 0
  let estimatedOnly = false
  // S11: a held source part is found wherever it is drawn now, also after a
  // fold re-keyed or re-slotted the row around it.
  const part = held.part ? partMarker(scroller, held.part.position) : undefined
  if (part) { delta = offset(part) - held.part!.offset; row = part.closest<HTMLElement>('[data-history-anchor]') ?? row; if (row) held.position = row.dataset.historyAnchor!; held.offset = row ? offset(row) - delta : held.offset }
  else if (row) delta = offset(row) - held.offset
  else {
    // A prepend can move the retained anchor outside the mounted range before
    // this layout effect. Use the real virtualizer's current measured/estimated
    // item offset, not total scrollHeight or a guessed source rank.
    // A held source part outside the mounted range is estimated from the row
    // that draws it now plus its captured offset inside that row.
    const owner = held.part ? partRow?.(held.part.position) : undefined
    if (owner !== undefined) held.position = owner
    const estimated = anchorOffset?.(held.position)
    if (estimated !== undefined && Number.isFinite(estimated)) { delta = estimated - held.offset; estimatedOnly = true }
    else {
      const fallback = held.alternatives.find(item => mounted.some(element => element.dataset.historyAnchor === item.position))
      row = fallback ? mounted.find(item => item.dataset.historyAnchor === fallback.position) : undefined
      if (row && fallback) delta = row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - fallback.screenTop
    }
  }
  // Only a position the held anchor was actually measured at is accounted
  // for; an estimate-only pass that applies nothing must not claim a reader's
  // own scroll as ours (its scroll event then re-anchors the reader).
  if (!estimatedOnly || delta !== 0) refs.settledTop.current = observedTop
  const exact = row !== undefined && row.dataset.historyAnchor === held.position
  let residual = delta
  if (Number.isFinite(delta) && delta !== 0) {
    refs.compensating.current = true
    const before = scroller.scrollTop
    const top = before + delta
    correcting.add(scroller)
    try {
      if (typeof scroller.scrollTo === 'function') scroller.scrollTo({ top, behavior: 'instant' })
      else scroller.scrollTop = top
    } finally { correcting.delete(scroller) }
    // Consume only what the browser actually applied, so the next layout
    // pass neither applies this delta twice nor forgets the part a scroll
    // edge clamp or device-pixel snapping held back.
    const applied = scroller.scrollTop - before
    held.offset += applied
    if (held.part) held.part.offset += applied
    residual = delta - applied
    refs.settledTop.current = scroller.scrollTop
    queueMicrotask(() => { refs.compensating.current = false })
    // The scroll can re-lay out the list synchronously (a virtual range
    // re-render with new estimates above). A held part that is not where it
    // belongs yet is corrected by the next layout pass, never re-anchored here.
    if (part?.isConnected && held.part) residual = offset(part) - held.part.offset
  }
  if (refs.priorFocus.current && !refs.priorFocus.current.isConnected) scroller.focus({ preventScroll: true })
  // Re-anchor only once the held row sits where it belongs. Re-capturing the
  // first visible row at an estimated or clamped position would turn that
  // error into permanent drift.
  // At the top edge a negative remainder can never be applied (content
  // above shrank while the reader sits at scrollTop 0): accept the position
  // rather than letting that stale remainder distort the next correction.
  const unreachable = residual < 0 && scroller.scrollTop <= 0
  if (!estimatedOnly && (!exact || !Number.isFinite(residual) || Math.abs(residual) < 1 || unreachable)) capture()
}

/** Sample drawn-row heights (D5 running average). Returns true on a width change. */
function sampleHistoryRows(scroller: HTMLElement, rows: HTMLElement[], messages: ChatMessage[], verbose: boolean,
  samples: HistoryRowMeasurements, heights: Map<string, number>, priorWidth: RefObject<number>): boolean {
  const stablePositions = new Set(messages.filter(message => message.historyPosition && !message.isStreaming && message.status !== 'streaming').map(message => message.historyPosition!))
  for (const position of heights.keys()) if (!stablePositions.has(position)) heights.delete(position)
  const width = scroller.getBoundingClientRect().width
  const widthChanged = !!priorWidth.current && Math.abs(width - priorWidth.current) > 1
  if (widthChanged) { samples.reset(); heights.clear() }
  priorWidth.current = width
  for (const element of rows) {
    const position = element.dataset.historyPosition
    if (!position) continue // Live DOM anchors are not public page selectors or history samples.
    const message = messages.find(item => item.historyPosition === position)
    if (!message || message.isStreaming || message.status === 'streaming' || element.hidden || element.getAttribute('aria-hidden') === 'true') {
      stablePositions.delete(position); heights.delete(position); continue
    }
    // Sample the transform-independent layout size the virtualizer itself
    // measures. A translated row's DOMRect height shifts by float rounding
    // with its offset (685.234375 vs 685.23388671875 near 10,000px), so an
    // unchanged row changed A -> virtualizer.measure() -> new translateY ->
    // new A, until React's update-depth limit (#185) unmounted the list.
    const height = historyLayoutHeight(element)
    if (!Number.isFinite(height) || height < 0) { stablePositions.delete(position); heights.delete(position); continue }
    heights.set(position, height)
    if (height === 0) { stablePositions.delete(position); continue }
    // One complete tool row includes its expanded body, not only a toggle.
    const tools = [...element.querySelectorAll<HTMLElement>('[data-history-tool-row]')].map(node => ({ key: node.dataset.historyToolRow!, height: historyLayoutHeight(node) })).filter(row => Number.isFinite(row.height) && row.height > 0)
    const toolHeight = tools.reduce((sum, tool) => sum + tool.height, 0)
    const overhead = !message.content && tools.length ? Math.max(0, height - toolHeight) / tools.length : 0
    for (const tool of tools) samples.observe(`${position}:tool:${tool.key}`, tool.height + overhead)
    if (message.content) samples.observe(`${position}:text`, height - toolHeight)
    else if (tools.length === 0 && drawnHistoryRows(message, verbose) > 0) samples.observe(position, height)
  }
  samples.retainPositions(stablePositions)
  return widthChanged
}

/** One direction's fill owner (D5): fetch contiguous pages until the goal is covered. */
async function runHistoryFill(env: FillEnv, direction: FillDirection, initial: boolean): Promise<void> {
  const { scroller, current, fills, heights, samples, opening, evaluateFill, measure } = env
  const sessionId = current.current.sid
  if (!sessionId || fills.current[direction] || document.hidden || scroller.clientHeight <= 0) return
  const generation = current.current.history.viewGeneration
  const starting = new Set(current.current.history.readerPositions)
  const owner: FillOwner = { frame: null, finishLayout: null }
  fills.current[direction] = owner
  let committed = false
  try {
    for (;;) {
      const state = useChatStore.getState()
      const kept = state.sessionsById[sessionId]
      if (fills.current[direction] !== owner || current.current.sid !== sessionId || document.hidden || scroller.clientHeight <= 0 || !kept || kept.isReplaying || kept.awaitingCatchUp || kept.history.viewGeneration !== generation || kept.history.requests.refresh.status !== 'idle' || kept.history.requests[direction].status === 'error') break
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
      if (fills.current[direction] !== owner || document.hidden) break
      // External-store commits must reach layout before newly drawn heights
      // can decide whether this same fill owner needs a contiguous top-up.
      await new Promise<void>(resolve => {
        owner.finishLayout = resolve
        owner.frame = requestAnimationFrame(() => { owner.frame = null; owner.finishLayout = null; resolve() })
      })
      if (fills.current[direction] !== owner || current.current.sid !== sessionId || document.hidden) break
      const after = useChatStore.getState().sessionsById[sessionId]
      if (!after || after.history.viewGeneration !== generation || after.history.requests[direction].status !== 'idle' || before === (direction === 'older' ? after.history.olderBoundary : after.history.newerBoundary)) break
      committed = true
      measure()
      const newRows = current.current.messages.filter(message => message.historyPosition && !previousPositions.has(message.historyPosition))
      if (!newRows.some(message => (heights.current.get(message.historyPosition!) ?? drawnHistoryRows(message, current.current.verbose) * samples.current.average) > 0) || previousPositions.size === 500) break
    }
  } finally {
    if (fills.current[direction] === owner) {
      fills.current[direction] = null
      if (initial) opening.current = false
      // New content moved the geometry: ask the OTHER direction again, once,
      // in case the reader reached its edge meanwhile. Only after a real
      // commit, so two edges that both cannot load never ping-pong.
      if (committed) requestAnimationFrame(() => { if (current.current.sid === sessionId) evaluateFill.current(direction === 'older' ? 'newer' : 'older') })
    }
  }
}

/** Re-settle on every layout-changing event of the conversation area. */
function observeHistoryLayout(scroller: HTMLElement, rows: () => HTMLElement[], settle: () => void): () => void {
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
  // Armed by the reader's own downward input; see bindReaderIntent.
  const downward = useRef(false)
  const compensating = useRef(false)
  // scrollTop the anchor bookkeeping last accounted for (capture or correction).
  const settledTop = useRef<number | null>(null)
  // Last scrollTop an application-owned placement produced (latest pin,
  // virtualizer adjustment). Such movement never ends follow-latest.
  const ownedTop = useRef<number | null>(null)
  const opening = useRef(true)
  // Armed when the reader LEAVES follow-latest (the view mode changes from
  // latest to historical) and cleared when follow-latest resumes. Only a
  // reader who left follow-latest can come back to it by reaching the end of
  // the loaded window: a reader who was placed into history reading without
  // ever following the latest has no end to return to.
  const leftFollowLatest = useRef(false)
  const anchorRefs = useMemo<AnchorRefs>(() => ({ anchor, settledTop, compensating, priorFocus }), [])
  // D5: one fill owner per view AND per direction. A single shared owner let
  // an in-flight older fill refuse the newer fill of a reader who jumped to the
  // newer edge, and nothing asked again once the older fill settled (T16 B12).
  const fills = useRef<Record<FillDirection, FillOwner | null>>({ older: null, newer: null })
  // Re-evaluates one direction's pixel trigger; assigned below, after requestFill.
  const evaluateFill = useRef<(direction: FillDirection) => void>(() => {})
  const [average, setAverage] = useState(80)
  const [prefix, setPrefix] = useState(0)
  const [widthGeneration, bumpWidthGeneration] = useState(0)
  const current = useRef({ sid, history, messages, verbose, anchorOffset })
  useLayoutEffect(() => {
    const previousMode = current.current.history.viewMode
    if (previousMode !== history.viewMode) {
      if (history.viewMode === 'latest') leftFollowLatest.current = false
      else if (previousMode === 'latest') leftFollowLatest.current = true
    }
    current.current = { sid, history, messages, verbose, anchorOffset }
  })
  const rows = useCallback(() => [...(scrollRef.current?.querySelectorAll<HTMLElement>('[data-history-anchor]') ?? [])], [scrollRef])
  const offset = useCallback((element: Element) => {
    const scroller = scrollRef.current!
    return element.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
  }, [scrollRef])
  const capture = useCallback(() => {
    const scroller = scrollRef.current
    if (scroller) captureAnchor(scroller, rows(), offset, anchorRefs, current.current.sid)
  }, [anchorRefs, offset, rows, scrollRef])
  const correct = useCallback(() => {
    const scroller = scrollRef.current
    if (!scroller || current.current.history.viewMode !== 'historical') return
    correctAnchor(scroller, rows, offset, anchorRefs, current.current.anchorOffset, capture, position => {
      const row = current.current.messages.find(message => message.historyPosition === position || message.historyParts?.some(part => part.position === position))
      return row ? row.historyPosition ?? `live:${row.id}` : undefined
    })
  }, [anchorRefs, capture, offset, rows, scrollRef])
  const measure = useCallback(() => {
    const scroller = scrollRef.current
    const state = current.current
    if (!scroller || !state.sid) return
    if (sampleHistoryRows(scroller, rows(), state.messages, state.verbose, samples.current, heights.current, priorWidth)) bumpWidthGeneration(value => value + 1)
    const next = samples.current.average
    setAverage(value => value === next ? value : next)
    const content = scroller.querySelector('[data-history-content]')
    setPrefix(content ? offset(content) : 0)
    recordHistoryGeometry(state.sid, scroller.clientHeight, next)
  }, [offset, rows, scrollRef])

  const requestFill = useCallback(async (direction: FillDirection, initial = false) => {
    const scroller = scrollRef.current
    if (scroller) await runHistoryFill({ scroller, current, fills, heights, samples, opening, evaluateFill, measure }, direction, initial)
  }, [measure, scrollRef])

  /**
   * Every discarded fill goes through here: the promise is caught and the
   * rejection is reported, so a read that fails after showing its error and
   * Retry never leaves an unhandled rejection behind (D8: an action ends in a
   * visible recoverable error, never a stranded loading state).
   */
  const fill = useCallback((direction: FillDirection, initial = false) => {
    void requestFill(direction, initial).catch(error => logViewportReadRejection(direction, error))
  }, [requestFill])

  const cancelFill = useCallback(() => {
    for (const direction of ['older', 'newer'] as const) {
      const owner = fills.current[direction]
      fills.current[direction] = null
      if (owner?.frame !== null && owner?.frame !== undefined) cancelAnimationFrame(owner.frame)
      owner?.finishLayout?.()
      if (owner) { owner.frame = null; owner.finishLayout = null }
    }
  }, [])
  useLayoutEffect(() => {
    evaluateFill.current = (direction) => {
      const scroller = scrollRef.current
      const state = current.current
      if (!scroller || !state.sid || state.history.requests.refresh.status !== 'idle') return
      if (direction === 'older') {
        if (historyPixelTrigger(scroller.scrollTop, scroller.clientHeight, !document.hidden)) fill('older', opening.current)
      } else if (state.history.hasLater && historyPixelTrigger(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight, scroller.clientHeight, !document.hidden)) fill('newer')
    }
  }, [fill, scrollRef])
  const onScroll = useCallback(() => {
    const scroller = scrollRef.current
    const state = current.current
    if (!scroller || !state.sid || compensating.current) return
    // Leaving follow-latest is decided by actual displacement away from the
    // live end (scrollbar, Find, momentum, keys, wheel, programmatic), unless
    // the application placed the viewport itself. Content growth leaves
    // scrollTop unchanged and fires no scroll, so it never ends following.
    const owned = ownedTop.current !== null && Math.abs(scroller.scrollTop - ownedTop.current) < 1
    // The same at-bottom threshold the viewport already uses: one pixel of
    // slack, so a reader who reaches the real end counts as at the end. The
    // overflow test is the one reader intent already uses (316e7f309): a
    // conversation that does not overflow has no bottom to return from.
    const distanceToEnd = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight
    const awayFromEnd = distanceToEnd > 1
    const overflowing = scroller.scrollHeight > scroller.clientHeight
    // Our own correction's scroll event arrives a frame later, when a row
    // measurement may already have moved the content again: keep holding the
    // same anchor then. Any other position is genuine reader movement.
    const own = settledTop.current !== null && Math.abs(scroller.scrollTop - settledTop.current) < 0.5
    if (state.history.viewMode === 'latest' && !owned && awayFromEnd && (scroller.scrollTop < lastTop.current - 1 || dragging.current)) {
      useChatStore.getState().enterHistoryReading(state.sid)
    } else if (state.history.viewMode === 'historical' && leftFollowLatest.current && overflowing && !awayFromEnd && !own && !owned
      && (downward.current || dragging.current) && scroller.scrollTop > lastTop.current + 1) {
      // The reader scrolled back to the END of the loaded window by hand --
      // with their own input (downward wheel/keys/swipe or a scrollbar drag).
      // An application-owned or input-less scroll that lands at the end is
      // not the reader returning, and never triggers this read.
      downward.current = false
      // Re-enter follow-latest exactly as the Return-to-latest control does:
      // a fresh latest READ. It LOADS the true latest page, so it never
      // silently claims a window whose end is not the live end (a window with
      // newer pages beyond it is corrected by that same read). Passive page,
      // live and reconnect updates never reach this branch: they do not move
      // the reader downward, and our own anchor correction is excluded.
      const size = historyPageSizing(scroller.clientHeight, samples.current.average, 4)
      void useChatStore.getState().returnToLatestHistory(state.sid, size).catch(error => logViewportReadRejection('latest', error))
    }
    lastTop.current = scroller.scrollTop
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
    if (historyPixelTrigger(scroller.scrollTop, scroller.clientHeight, !document.hidden)) fill('older', opening.current)
    else if (historyPixelTrigger(distanceToEnd, scroller.clientHeight, !document.hidden) && state.history.hasLater) fill('newer')
  }, [capture, correct, fill, scrollRef])
  useLayoutEffect(() => {
    samples.current.reset(); heights.current.clear(); anchor.current = null; settledTop.current = null; ownedTop.current = null; opening.current = true
    priorFocus.current = null; priorWidth.current = 0; lastTop.current = 0; leftFollowLatest.current = false; downward.current = false; setAverage(80); setPrefix(0)
    return () => {
      cancelFill(); samples.current.reset(); heights.current.clear(); anchor.current = null; priorFocus.current = null
      current.current = { sid: null, history: emptyHistoryState(), messages: [], verbose, anchorOffset: undefined }
      if (sid) { forgetHistoryGeometry(sid); releaseHistoryProtection(sid) }
    }
  }, [sid, verbose, cancelFill])
  useLayoutEffect(() => {
    const scroller = scrollRef.current
    if (!scroller || !sid) return
    return bindReaderIntent(scroller, () => useChatStore.getState().enterHistoryReading(sid), dragging, downward)
  }, [sid, scrollRef])
  useLayoutEffect(() => {
    const visibility = () => {
      if (document.hidden) cancelFill()
      else { measure(); if (opening.current && current.current.history.viewMode === 'latest' && samples.current.count > 0) fill('older', true) }
    }
    document.addEventListener('visibilitychange', visibility)
    return () => document.removeEventListener('visibilitychange', visibility)
  }, [cancelFill, fill, measure])
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
      if (opening.current && samples.current.count > 0) fill('older', true)
    }
  })
  useLayoutEffect(() => {
    const scroller = scrollRef.current
    if (!scroller) return
    return observeHistoryLayout(scroller, rows, () => { correct(); measure() })
  }, [messages, correct, measure, rows, scrollRef])
  // TanStack Router (main.tsx scrollRestoration) writes back the saved
  // scrollTop of every element that scrolled on a location when it renders
  // that location again. For the conversation that write is application-owned
  // placement, never reader movement (D8 T21 clarification), and this hook is
  // the one coordinator of the position (D8; D9: returning opens the newest
  // page). Our listener runs after the router's restorer in the same emit.
  const router = useRouter({ warn: false })
  useLayoutEffect(() => {
    // No router (routerless render) or a partial router stand-in: nothing restores scroll.
    if (typeof router?.subscribe !== 'function') return
    return router.subscribe('onRendered', () => {
      const scroller = scrollRef.current
      if (!scroller || !current.current.sid) return
      if (current.current.history.viewMode === 'historical') { correct(); return }
      scroller.scrollTop = scroller.scrollHeight
      ownedTop.current = scroller.scrollTop
      lastTop.current = scroller.scrollTop
    })
  }, [router, correct, scrollRef])
  const busy = Object.values(history.requests).some(request => request.status === 'loading')
  /** Record a placement the list itself made (e.g. a virtualizer size adjustment). */
  const claimScroll = useCallback(() => {
    const scroller = scrollRef.current
    if (scroller) ownedTop.current = scroller.scrollTop
  }, [scrollRef])
  return { average, prefix, widthGeneration, onScroll, claimScroll, busy, historical: history.viewMode === 'historical' }
}
