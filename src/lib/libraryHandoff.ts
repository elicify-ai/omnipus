// libraryHandoff — same-origin cross-tab signal for the Library panel's
// pop-out↔docked hand-off (library-spec.md D-4). Mirrors
// src/lib/browserLiveHandoff.ts's mechanism and caveats, with its OWN
// BroadcastChannel name (never share a channel across independent features —
// a stray listener on the wrong channel would silently misfire).
//
// UNLIKE the browser-live panel, popping out the Library does NOT force-close
// the docked panel (LibraryPanel.tsx's handlePopOut) — there is no exclusive
// "control lock" here (browsing/editing files two ways at once is harmless;
// watching+driving the SAME live browser session twice is not), so both
// surfaces can legitimately stay open side by side. That makes this handoff
// narrower in practice than the browser-live one: it exists as a SAFETY NET,
// not a hand-over. LibraryPanel only acts on a `popout-closed` broadcast when
// nothing is *currently* docked (the exact same "guarded on null at delivery
// time" idiom BrowserLivePanel uses) — e.g. the user closed the docked panel
// manually while the pop-out tab was still open, then later closed that
// pop-out too. Without this, that sequence would leave the user with no
// Library surface open anywhere and no obvious way back in. When the docked
// panel is still open, the broadcast is a no-op by design (both stay open).
//
// Feature-detected: BroadcastChannel has shipped in every evergreen browser
// since Safari 15.4; a defensive no-op fallback (see browserLiveHandoff.ts's
// own note) keeps environments without it from throwing.

const CHANNEL_NAME = 'omnipus-library-handoff'

export interface LibraryHandoffMessage { // not-wire-format: same-origin BroadcastChannel payload between the docked Library panel and its pop-out window — a browser-local lifecycle signal that never crosses the gateway/SPA boundary and is never persisted
  type: 'popout-closed'
  /** Undefined = the pop-out was showing the virtual root (no workspace selected yet). */
  workspaceId?: string
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
 * Fire-and-forget: tell any listening tab that the Library pop-out (scoped to
 * `workspaceId`, or the virtual root when omitted) just closed. Safe to call
 * more than once for the same close (an explicit Close button AND a
 * `pagehide` fallback for a native tab-close both announce) — the listener
 * side treats re-docking as idempotent.
 */
export function announceLibraryPopoutClosed(workspaceId?: string): void {
  const channel = openChannel()
  if (!channel) return
  try {
    const message: LibraryHandoffMessage = { type: 'popout-closed', workspaceId }
    channel.postMessage(message)
  } finally {
    channel.close()
  }
}

/**
 * Subscribes to Library pop-out-closed events. Returns an unsubscribe
 * function that also closes the channel — always call it on unmount. No-ops
 * (returns a no-op unsubscribe) when BroadcastChannel isn't available.
 */
export function onLibraryPopoutClosed(callback: (workspaceId?: string) => void): () => void {
  const channel = openChannel()
  if (!channel) return () => {}
  const handler = (event: MessageEvent<LibraryHandoffMessage>) => {
    const data = event.data
    if (data && data.type === 'popout-closed') {
      callback(data.workspaceId)
    }
  }
  channel.addEventListener('message', handler)
  return () => {
    channel.removeEventListener('message', handler)
    channel.close()
  }
}
