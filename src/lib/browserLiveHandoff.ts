// browserLiveHandoff — same-origin cross-tab signal for the pop-out↔docked-
// panel hand-off (BUG 2 fix, live UAT re-run 2026-07-28).
//
// BrowserLivePanel.tsx's "Pop out" button closes the docked panel outright
// when opening the fullscreen `/#/browser-live` tab (see that file's own doc
// comment — the live-view control lock is first-come/no-preempt, so only one
// viewer may be mounted at a time: popping out HANDS OVER the view). Nothing
// previously restored the docked panel once that pop-out tab later closed —
// the user was left with a bare "Open browser" button, and clicking it again
// re-opened against whatever agent/session happens to be globally active in
// chat right now (see ChatControls.tsx's handleOpenBrowser), which is very
// often NOT the agent that actually owns the live session the pop-out was
// showing.
//
// The two surfaces are genuinely separate `window.open` documents opened
// with `noopener,noreferrer` — `window.open()` itself returns `null` per
// spec with `noopener` set (BrowserLivePanel.tsx's own comment), and the
// child's `window.opener` is severed — so there is no direct JS reference
// either side can use to learn "the other one just closed."
// `BroadcastChannel` is the one same-origin primitive that needs no such
// reference: every same-origin tab/window can post to and listen on a named
// channel. Used here ONLY to carry a single fire-and-forget lifecycle event
// ("the pop-out for (sessionId, agentId) just went away") — never frame data
// or drive input, which stay on the dedicated per-session live WS.
//
// Feature-detected: BroadcastChannel has shipped in every evergreen browser
// since Safari 15.4, but a defensive no-op fallback keeps a pre-15.4 Safari
// (or any environment without it) from throwing — the pop-out still works
// exactly as before, it just can't auto-restore the docked panel on close.
//
// Known scoping limit (documented, not fixed here): BroadcastChannel is
// origin-wide, not "this main tab + its one popup" — if a user has genuinely
// TWO separate app tabs open, each with its own pop-out, closing one could
// re-dock the panel in the OTHER app tab. The existing `useUiStore` design
// already assumes a single app-instance mental model (it's an in-memory,
// per-tab Zustand store with no cross-tab sync of its own), so this doesn't
// regress anything that worked today — it's called out here so a future
// multi-tab hardening pass knows where to look.

const CHANNEL_NAME = 'omnipus-browser-live-handoff'

/** not-wire-format: local same-origin BroadcastChannel payload — a same-tab
 * lifecycle signal, never serialized across the gateway/SPA boundary. */
export interface BrowserLiveHandoffMessage {
  type: 'popout-closed'
  sessionId: string
  agentId: string
}

function openChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') return null
  try {
    return new BroadcastChannel(CHANNEL_NAME)
  } catch {
    return null
  }
}

/**
 * Fire-and-forget: tell any listening tab that the pop-out for
 * (sessionId, agentId) just closed (or navigated away from the live view).
 * Safe to call more than once for the same close (e.g. once synchronously
 * from an explicit Close button AND once from a `pagehide` fallback for a
 * native tab-close that never runs any in-app handler) — the listener side
 * (`onPopoutClosed` callers) treats re-docking as idempotent.
 */
export function announcePopoutClosed(sessionId: string, agentId: string): void {
  const channel = openChannel()
  if (!channel) return
  try {
    const message: BrowserLiveHandoffMessage = { type: 'popout-closed', sessionId, agentId }
    channel.postMessage(message)
  } finally {
    channel.close()
  }
}

/**
 * Subscribes to pop-out-closed events. Returns an unsubscribe function that
 * also closes the channel — always call it on unmount, mirroring every other
 * subscription-cleanup pattern in this codebase. No-ops (returns a no-op
 * unsubscribe) when BroadcastChannel isn't available in this environment.
 */
export function onPopoutClosed(callback: (sessionId: string, agentId: string) => void): () => void {
  const channel = openChannel()
  if (!channel) return () => {}
  const handler = (event: MessageEvent<BrowserLiveHandoffMessage>) => {
    const data = event.data
    if (data && data.type === 'popout-closed') {
      callback(data.sessionId, data.agentId)
    }
  }
  channel.addEventListener('message', handler)
  return () => {
    channel.removeEventListener('message', handler)
    channel.close()
  }
}
