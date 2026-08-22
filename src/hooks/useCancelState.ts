// useCancelState — Stop/cancel button state machine for the chat composer
// (EC-15 / FR-21 / T23 / T25). Extracted out of OmnipusComposer's
// ~862-line body (Wave 3 structural refactor) — behavior is unchanged, only
// ownership moved.
//
// Owns the Stop button's label progression:
//   'stop'     — idle, button shows the Stop icon.
//   'stopping' — user asked to cancel; button shows "Stopping..." (set
//                synchronously, no network round-trip) until the turn
//                actually finishes.
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

export type StopLabel = 'stop' | 'stopping'

export interface UseCancelStateResult {
  stopLabel: StopLabel
  /**
   * Sets the button to 'stopping' ONLY if a turn is actively streaming
   * (checked against the `isStreaming` value passed into the hook), then
   * always calls `cancelStream()`. Used by the `/cancel` slash command and
   * the composer's local (focused-input) Escape handler — both check
   * "is a turn actually running right now" before flipping the visual
   * state, since Escape/`/cancel` can also fire when the button isn't even
   * showing (nothing to cancel; `cancelStream()` still safely no-ops the
   * network send but still marks the last message interrupted).
   */
  cancelIfStreaming: () => void
  /**
   * Unconditionally sets the button to 'stopping' before calling
   * `cancelStream()` — used ONLY by the Stop button's onClick. Do NOT guard
   * this with `isStreaming`: `cancelStream()` handles the server-send gate
   * internally, and guarding here would silently no-op when the turn races
   * to completion between render (when the button became clickable) and
   * the click itself, preventing the "(interrupted)" label from appearing.
   */
  cancelUnconditional: () => void
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

export function useCancelState(isStreaming: boolean, cancelStream: () => void): UseCancelStateResult {
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

  useEffect(() => {
    if (isStreaming) {
      streamingStartedAt.current = Date.now()
    }
  }, [isStreaming])

  // EC-15: reset the stop label back to 'stop' whenever streaming ends so
  // the button is fresh for the next turn.
  // T25: enforce a minimum 1000ms display of "Stopping..." before resetting.
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
  }, [isStreaming])

  const cancelIfStreaming = useCallback(() => {
    if (isStreaming) {
      stoppingStartedAt.current = Date.now()
      setStopLabel('stopping')
    }
    cancelStream()
  }, [isStreaming, cancelStream])

  const cancelUnconditional = useCallback(() => {
    stoppingStartedAt.current = Date.now()
    setStopLabel('stopping')
    cancelStream()
  }, [cancelStream])

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
  //   would silently no-op — cancellation never happens and "(interrupted)"
  //   never appears.
  //
  //   Fix: read through to the Zustand store to check if the last assistant
  //   message is in a cancellable state: either actively streaming
  //   (isStreaming:true) or very recently completed (within the race
  //   window). This is a snapshot read — it bypasses the React closure's
  //   stale isStreaming value entirely.
  //
  // cancelStream() internally gates the WS send on isStreaming, so calling
  // it when the turn is already done is safe: it just marks the last
  // message interrupted, which is correct and desired here.
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
      const shouldCancel = liveState.isStreaming || withinRaceWindow || stopLabel === 'stopping'
      if (!shouldCancel) return
      e.preventDefault()
      if (liveState.isStreaming) {
        stoppingStartedAt.current = Date.now()
        setStopLabel('stopping')
      }
      cancelStream()
    }
    document.addEventListener('keydown', handleGlobalEscape)
    return () => document.removeEventListener('keydown', handleGlobalEscape)
    // stopLabel is included so the effect re-registers when the label
    // changes, ensuring the closure capture of stopLabel is fresh for the
    // 'stopping' guard.
  }, [stopLabel, cancelStream])

  return { stopLabel, cancelIfStreaming, cancelUnconditional }
}
