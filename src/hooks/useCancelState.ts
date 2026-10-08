// useCancelState — Stop/cancel button state machine for the chat composer
// (EC-15 / FR-21 / T23 / T25), plus the ADR-20260928 D9 Stop-all
// confirmation window. Extracted out of OmnipusComposer's ~862-line body
// (Wave 3 structural refactor); the D9 window is added on top.
//
// Owns the Stop button's label progression:
//   'stop'     — idle, button shows the Stop icon.
//   'stopping' — user asked to cancel; button shows "Stopping..." (set
//                synchronously, no network round-trip) until the turn
//                actually finishes.
//
// Owns the D9 Stop-all scoping on top of the plain cancel:
//
//   First Stop press / Esc / `/stop` → single-session cancel (NO scope key)
//     and a 3-second confirmation window. The same Stop button stays visible.
//   Second activation on the SAME session inside that window → confirmed
//     Stop all: one `cancelStream(undefined, 'tree')` frame, down only.
//   Expiry after 3 s, window blur and session switch disarm the window —
//     the next activation is a fresh first press.
//   `/cancel` is itself the confirmation: one action → one tree frame.
//   No separate Stop-all buttons (founder 2026-10-06).
//
// The arming decision reads a ref (`stopAllArmedRef`), not the state
// mirror, so the global document-level Escape handler can never act on a
// stale closure; the state keeps the same Stop button reachable while armed.
//
// Three call sites need to trigger a cancel, and they are NOT equivalent —
// see `cancelIfStreaming` vs `cancelUnconditional` below. This hook exposes
// both because collapsing them into one would change behavior at the Stop
// button (see that function's doc comment).
//
// Also owns the T23 global (document-level) Escape handler: the composer's
// own onKeyDown only fires when the textarea has focus, so a document
// listener is needed to cancel a turn when the user clicked elsewhere on
// the page.

import { useEffect, useRef, useState, useCallback } from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { isBucketGoalRunning } from '@/lib/goalActivity'
import type { CancelFrame } from '@/lib/api/generated/asyncapi-types'

export type StopLabel = 'stop' | 'stopping'

export interface UseCancelStateResult {
  stopLabel: StopLabel
  /**
   * True while the Stop-all confirmation window is open — the composer
   * keeps the same Stop button reachable for a second activation. Arms on
   * the first Stop/Esc or `/stop`, expires after 3 s, window blur or session
   * switch. There is no separate Stop-all button.
   */
  stopAllArmed: boolean
  /**
   * Sets the button to 'stopping' ONLY if a turn is actively streaming
   * (checked against the `isStreaming` value passed into the hook), then
   * always calls `cancelStream()`. Used by the composer's local
   * (focused-input) Escape handler — it checks "is a turn actually running
   * right now" before flipping the visual state, since Escape can also
   * fire when the button isn't even showing (nothing to cancel;
   * `cancelStream()` then sends nothing and marks nothing: the last message
   * is marked interrupted only while a turn is actually streaming).
   *
   * D9: this is the FIRST activation surface — when the confirmation
   * window is already armed it confirms instead (one tree frame), and
   * otherwise it sends the single-session cancel and arms the window.
   */
  cancelIfStreaming: () => void
  /**
   * Unconditionally sets the button to 'stopping' before calling
   * `cancelStream()` — used by the Stop button and `/stop`. Do NOT guard
   * this with `isStreaming`: `cancelStream()` handles the server-send gate
   * internally, and the first press must still arm the Stop-all window and
   * show "Stopping..." when the turn raced to completion between render (when
   * the button became clickable) and the click. In that race nothing is marked
   * "(interrupted)": the answer did finish, and `cancelStream()` marks only
   * while a turn is actually streaming.
   *
   * First activation = single-session cancel + the 3 s window; a second
   * activation inside that window confirms (tree frame).
   */
  cancelUnconditional: () => void
  /**
   * The self-confirming Stop all (D9): one call → one `cancelStream
   * (undefined, 'tree')` frame, no double activation. Used by `/cancel`
   * (via useSlashMenu) and by the confirmed second Stop/Esc activation.
   * Also closes any open confirmation window without sending anything extra.
   */
  cancelAllTreeScoped: () => void
}

/** Minimum time the "Stopping..." label stays visible once shown (T25). */
const MIN_STOPPING_DISPLAY_MS = 1000

/**
 * Escape is treated as a cancel intent if the stream started within this
 * many ms of the keypress — covers the race window where a fast LLM
 * delivers the full response (clearing isStreaming) before the global
 * Escape handler runs (T23).
 */
const CANCEL_RACE_WINDOW_MS = 8_000

/**
 * How long the first Stop/Esc or `/stop` activation's confirmation window
 * stays open. A second activation on the same session confirms a tree stop;
 * past it (or on focus/session change) the next is a fresh first press.
 */
export const STOP_ALL_CONFIRM_WINDOW_MS = 3_000

/**
 * ADR-20260928 D9 Stop-all confirmation window, extracted verbatim out of
 * `useCancelState` (function-size budget; no behavior change): the armed
 * ref+state pair, the 3 s expiry timer, the blur and session-switch disarm
 * effects, and the unmount timer cleanup.
 *
 * `stopAllArmed` mirrors the ref for rendering only; every behavioral read
 * goes through the ref so the global Escape listener (registered with its
 * own closure) can never act on stale arm state.
 *
 * `useCancelState` calls this at the same position in its body where this
 * block was originally declared, so React registers these effects in the
 * same order as before the extraction.
 */
function useStopAllConfirmWindow() {
  const [stopAllArmed, setStopAllArmed] = useState(false)
  const stopAllArmedRef = useRef(false)
  const stopAllTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // The session the window was armed for — a session switch disarms (D9).
  const stopAllArmedForSidRef = useRef<string | null>(null)

  const disarmStopAll = useCallback(() => {
    stopAllArmedRef.current = false
    setStopAllArmed(false)
    if (stopAllTimerRef.current !== null) {
      clearTimeout(stopAllTimerRef.current)
      stopAllTimerRef.current = null
    }
  }, [])

  const armStopAll = useCallback(() => {
    stopAllArmedRef.current = true
    setStopAllArmed(true)
    stopAllArmedForSidRef.current = useSessionStore.getState().activeSessionId
    if (stopAllTimerRef.current !== null) clearTimeout(stopAllTimerRef.current)
    stopAllTimerRef.current = setTimeout(disarmStopAll, STOP_ALL_CONFIRM_WINDOW_MS)
  }, [disarmStopAll])

  // D9: focus change closes the confirmation window.
  useEffect(() => {
    if (!stopAllArmed) {
      return undefined
    }
    const onWindowBlur = () => disarmStopAll()
    window.addEventListener('blur', onWindowBlur)
    return () => window.removeEventListener('blur', onWindowBlur)
  }, [stopAllArmed, disarmStopAll])

  // D9: switching sessions closes the confirmation window (and no frame may ever
  // target the NEW session from a window armed on the old one).
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  useEffect(() => {
    if (stopAllArmed && stopAllArmedForSidRef.current !== activeSessionId) {
      disarmStopAll()
    }
  }, [stopAllArmed, activeSessionId, disarmStopAll])

  // Clear a live expiry timer on unmount.
  useEffect(() => {
    return () => {
      if (stopAllTimerRef.current !== null) {
        clearTimeout(stopAllTimerRef.current)
        stopAllTimerRef.current = null
      }
    }
  }, [])

  return { stopAllArmed, stopAllArmedRef, armStopAll, disarmStopAll }
}

export function useCancelState(
  isStreaming: boolean,
  // The store action's full signature — the D9 tree paths call
  // `cancelStream(undefined, 'tree')` (ADR-20260928 MAJ-002); every other
  // path keeps the bare single-session call.
  //
  // It returns `false` only when the cancel could not be handed to the socket
  // (a visible error toast was already shown). Such a press sent nothing, so
  // it neither shows "Stopping..." nor arms the confirmation window (W2).
  cancelStream: (sessionId?: string, scope?: CancelFrame['scope']) => boolean,
): UseCancelStateResult {
  const [stopLabel, setStopLabel] = useState<StopLabel>('stop')

  // T25: track when stopLabel last transitioned to 'stopping' so the reset
  // effect below can enforce a minimum display duration. Without this, a
  // very fast LLM response causes the done frame to arrive within
  // milliseconds of the click, immediately triggering the effect and making
  // "Stopping..." vanish before any assertion (or user eye) can catch it.
  const stoppingStartedAt = useRef<number>(0)

  // T23: track when streaming last started. Used by the global Escape
  // handler to decide whether Escape should still trigger a cancel in the
  // race window where isStreaming just went false (done frame arrived) but
  // the user pressed Escape intending to cancel a turn they just observed
  // streaming.
  const streamingStartedAt = useRef<number>(0)

  // ── D9 Stop-all confirmation window ────────────────────────────────────
  // `stopAllArmed` mirrors the ref for rendering only; every behavioral
  // read goes through the ref so the global Escape listener (registered
  // with its own closure) can never act on stale arm state. Extracted
  // verbatim into `useStopAllConfirmWindow` (function-size budget, no
  // behavior change); called at the same position in the hook body, so its
  // effects still register in the original order.
  const { stopAllArmed, stopAllArmedRef, armStopAll, disarmStopAll } =
    useStopAllConfirmWindow()

  // `/cancel` and a confirmed second Stop/Esc activation land here — one
  // call, one scope:"tree" frame, window closed, nothing else sent.
  const cancelAllTreeScoped = useCallback(() => {
    disarmStopAll()
    stoppingStartedAt.current = Date.now()
    if (cancelStream(undefined, 'tree') === false) return
    setStopLabel('stopping')
  }, [cancelStream, disarmStopAll])

  // The single first-activation path (button, `/stop`, local and global
  // Escape): session-scoped cancel, then "Stopping..." (when asked for) and
  // the 3 s window — but only if the cancel was actually handed to the
  // socket; an undelivered press leaves the next press a first press (W2).
  const activateFirstPress = useCallback((showStopping: boolean) => {
    stoppingStartedAt.current = Date.now()
    if (cancelStream() === false) return
    if (showStopping) setStopLabel('stopping')
    armStopAll()
  }, [cancelStream, armStopAll])

  useEffect(() => {
    if (isStreaming) {
      streamingStartedAt.current = Date.now()
    }
  }, [isStreaming])

  // EC-15: reset the stop label back to 'stop' whenever streaming ends so
  // the button is fresh for the next turn.
  // T25: enforce a minimum 1000ms display of "Stopping..." before resetting.
  // W1: also re-evaluate when the label itself changes — a second activation
  // or `/stop` after the stream already ended sets 'stopping' with
  // isStreaming already false, so [isStreaming] alone never ran again and
  // the button stuck on "Stopping...".
  useEffect(() => {
    if (!isStreaming) {
      const elapsed = Date.now() - stoppingStartedAt.current
      const remaining = MIN_STOPPING_DISPLAY_MS - elapsed
      if (stopLabel === 'stopping' && remaining > 0) {
        const timer = setTimeout(() => setStopLabel('stop'), remaining)
        return () => clearTimeout(timer)
      }
      setStopLabel('stop')
    }
    // No cleanup needed on this path (isStreaming, or the min-display
    // window already elapsed) — explicit return so every path is typed
    // consistently as `void | (() => void)` under noImplicitReturns.
    return undefined
  }, [isStreaming, stopLabel])

  const cancelIfStreaming = useCallback(() => {
    // D9: an activation while the window is open is the CONFIRMED tree
    // stop (the same Stop button's keyboard twin). Otherwise this is a first
    // activation: single-session cancel, then arm the window.
    if (stopAllArmedRef.current) {
      cancelAllTreeScoped()
      return
    }
    activateFirstPress(isStreaming)
  }, [isStreaming, cancelAllTreeScoped, activateFirstPress])

  const cancelUnconditional = useCallback(() => {
    // D9: same contract as cancelIfStreaming, minus the streaming guard —
    // the Stop button keeps its always-morphs semantics; first press sends
    // the session-scoped frame and arms the window; a press inside that
    // window confirms the tree stop.
    if (stopAllArmedRef.current) {
      cancelAllTreeScoped()
      return
    }
    activateFirstPress(true)
  }, [cancelAllTreeScoped, activateFirstPress])

  // US-1.4 / FR-23: Global Escape key handler — cancels a turn even when
  // the input does not have focus (e.g. user clicked somewhere else on the
  // page). The composer's own onKeyDown covers the focused-input case; this
  // effect covers the unfocused case (T23).
  //
  // T23 FIX: two problems existed with the previous implementation:
  //
  //   AssistantUI's cancelOnEscape: ComposerPrimitive.Input has
  //   cancelOnEscape defaulting to true, which consumed the Escape keydown
  //   before our React onKeyDown handler saw it. Fixed by passing
  //   cancelOnEscape={false} to that component.
  //
  //   Problem 2 — Guard vs. race window: a Playwright test calls
  //   page.keyboard.press('Escape') immediately after a long streaming turn
  //   starts (stop button first visible). A fast LLM can complete the turn
  //   and deliver the done frame in <1s, so by the time Escape fires:
  //   isStreaming=false, stopLabel='stop' (the reset effect above already
  //   ran), and a naive guard `if (!isStreaming && stopLabel !== 'stopping')`
  //   would silently no-op — the Stop window never arms for a Stop the user
  //   pressed on a turn they just watched stream.
  //
  //   Fix: read through to the Zustand store to check if the last assistant
  //   message is in a cancellable state: either actively streaming
  //   (isStreaming:true) or very recently completed (within the race
  //   window). This is a snapshot read — it bypasses the React closure's
  //   stale isStreaming value entirely.
  //
  // cancelStream() internally gates the WS send (and the "(interrupted)" mark)
  // on a turn actually streaming, so calling it when the turn is already done
  // is safe: it sends nothing and marks nothing, because the answer finished.
  //
  // bugfixes3 Fix 3: this listener is intentionally NOT menu-aware — it has
  // no idea whether the composer's "/" or "@" menu is open. That's handled
  // upstream instead: ChatScreen.handleKeyDown (composer's onKeyDown, a
  // React synthetic handler) checks `slashOpen && shouldShowSlash` BEFORE
  // its own cancel-Escape branch, and when the menu is open it calls
  // `e.stopPropagation()` after closing the menu. Because this listener is
  // plain BUBBLE-phase on `document` (no `capture: true` below) and this app
  // mounts via `createRoot(#root)` (src/main.tsx) — React delegates its
  // synthetic events at that root container, an ancestor of which is
  // `document` — stopPropagation there prevents the native keydown from ever
  // reaching this handler. So: menu open → this function never runs for that
  // keypress; menu closed (including "closed by the Escape that just ran") →
  // this function runs normally, e.g. on the immediately-following Escape.
  //
  // bugfixes3 deferred Fix (item 5): the menu-close branch above is the only
  // one that stops propagation — ChatScreen.handleKeyDown's OWN cancel-Escape
  // branch (the `isStreaming || stopLabel === 'stopping'` guard) calls
  // `e.preventDefault()` and then `cancelState.cancelIfStreaming()` directly,
  // but does NOT stopPropagation. That keydown therefore still bubbles all
  // the way to `document`, where — before this `defaultPrevented` check
  // existed — this handler ran its OWN `shouldCancel` check (unconditioned
  // on how the event got here), found `liveState.isStreaming` still true, and
  // called `cancelStream()` a SECOND time for the exact same keypress. A
  // focused-textarea Escape mid-stream was thus double-dispatching: once via
  // the React synthetic handler, once via this native listener. Skipping
  // whenever `e.defaultPrevented` fixes this for that specific case (the
  // synthetic handler already called preventDefault before this listener
  // ever sees the event) while leaving the FR-23/T23 unfocused-Escape case
  // untouched — with no textarea focused, no React onKeyDown runs at all, so
  // `defaultPrevented` is still false by the time this listener fires and
  // cancellation proceeds exactly as before. Verified no OTHER Escape
  // producer in the app relies on this listener ALSO firing after its own
  // preventDefault: grep for `key === 'Escape'` across src/ turns up
  // Sidebar.tsx, image-lightbox.tsx, WorkspaceHeader.tsx, SearchModal.tsx,
  // and BrowserLiveView.tsx — none of them intend to also cancel an
  // unrelated, possibly-backgrounded chat stream as a side effect of their
  // own Escape handling; the ones that DO call preventDefault (SearchModal's
  // title-edit-cancel, BrowserLiveView's driving-mode release) were
  // incidentally reaching this listener too and could double-fire a stream
  // cancel that had nothing to do with what the user was doing — this check
  // removes that unintended coupling as a side effect, not just the
  // documented double-dispatch.
  useEffect(() => {
    function handleGlobalEscape(e: KeyboardEvent) {
      if (e.key !== 'Escape') return
      if (e.defaultPrevented) return
      // Read live state from the store — bypasses the stale React closure
      // value for isStreaming.
      const liveState = useChatStore.getState()
      const withinRaceWindow =
        streamingStartedAt.current > 0 &&
        Date.now() - streamingStartedAt.current < CANCEL_RACE_WINDOW_MS
      // ADR-20260928 D9: an armed Stop-all window is a live confirmation
      // surface — the second (unfocused) Esc inside it confirms, even
      // though the first activation already ended the turn locally.
      const shouldCancel =
        liveState.isStreaming || isBucketGoalRunning(liveState.sessionsById[useSessionStore.getState().activeSessionId ?? '']) || withinRaceWindow || stopLabel === 'stopping' || stopAllArmedRef.current
      if (!shouldCancel) return
      e.preventDefault()
      // D9: an Escape inside the confirmation window is the second
      // activation — the confirmed tree stop. Otherwise a first
      // activation: session-scoped cancel, then arm the window.
      if (stopAllArmedRef.current) {
        cancelAllTreeScoped()
        return
      }
      activateFirstPress(liveState.isStreaming)
    }
    document.addEventListener('keydown', handleGlobalEscape)
    return () => document.removeEventListener('keydown', handleGlobalEscape)
    // stopLabel is included so the effect re-registers when the label
    // changes, ensuring the closure capture of stopLabel is fresh for the
    // 'stopping' guard.
  }, [stopLabel, cancelAllTreeScoped, activateFirstPress])

  return { stopLabel, stopAllArmed, cancelIfStreaming, cancelUnconditional, cancelAllTreeScoped }
}
