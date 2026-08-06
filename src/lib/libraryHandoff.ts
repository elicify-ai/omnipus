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
// surfaces can legitimately stay open side by side.
//
// UAT fix (Dana, live tester, re-verified v8 — "pop-out re-dock STILL does
// not restore the workspace"): the FIRST attempt at this hand-off only ever
// re-opened the docked panel when NOTHING was currently docked, and only
// announced the pop-out's workspace at `pagehide` time. Both halves of that
// were wrong:
//
//   1. The tester's exact repro left the docked panel OPEN the whole time
//      (pop out from "My Workspace", navigate the pop-out tab to "Dana
//      Workspace B", close it) — the old "only react when nothing is
//      docked" guard made the close a no-op by design in exactly that case,
//      so the fix could never have worked no matter how reliable the
//      message delivery was. Root cause: the guard, not the plumbing.
//   2. `pagehide` is also not a reliable moment to POST: BroadcastChannel
//      delivery is asynchronous even within a single JS realm, and a
//      browser tearing down a page for `pagehide` offers no guarantee the
//      message is flushed before the tab is gone — "a message posted during
//      unload may never be delivered."
//
// Fix: publish the pop-out's current workspace CONTINUOUSLY — on every
// in-tab navigation via `announceLibraryWorkspaceChanged`, not only at
// teardown — so the docked side already knows the latest workspace well
// before the tab ever closes. `popout-closed` (still posted at `pagehide` as
// a fallback/trigger signal) no longer needs to carry the payload that
// matters; LibraryPanel.tsx applies the last CONTINUOUSLY-known workspace,
// falling back to whatever `popout-closed` itself carries only if no
// continuous update was ever received. And the listener side now applies
// that workspace unconditionally — updating an already-docked panel, not
// only re-opening a closed one — since the docked and popped-out surfaces
// show the SAME Library, and the user's last action (navigate, then close)
// is what should be reflected back.
//
// Feature-detected: BroadcastChannel has shipped in every evergreen browser
// since Safari 15.4; a defensive no-op fallback (see browserLiveHandoff.ts's
// own note) keeps environments without it from throwing.

const CHANNEL_NAME = 'omnipus-library-handoff'

export type LibraryHandoffMessage = // not-wire-format: same-origin BroadcastChannel payload between the docked Library panel and its pop-out window — a browser-local lifecycle signal that never crosses the gateway/SPA boundary and is never persisted
  | { type: 'popout-closed'; workspaceId?: string }
  | { type: 'workspace-changed'; workspaceId?: string }

function openChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') return null
  try {
    return new BroadcastChannel(CHANNEL_NAME)
  } catch {
    return null
  }
}

function postMessage(message: LibraryHandoffMessage): void {
  const channel = openChannel()
  if (!channel) return
  try {
    channel.postMessage(message)
  } finally {
    channel.close()
  }
}

/**
 * Fire-and-forget: tell any listening tab that the Library pop-out (scoped to
 * `workspaceId`, or the virtual root when omitted) just closed. Safe to call
 * more than once for the same close (an explicit Close button AND a
 * `pagehide` fallback for a native tab-close both announce) — the listener
 * side treats re-docking as idempotent. Its `workspaceId` payload is now only
 * a fallback for when no `workspace-changed` broadcast was ever received (see
 * module doc) — the continuous broadcast is the primary source of truth.
 */
export function announceLibraryPopoutClosed(workspaceId?: string): void {
  postMessage({ type: 'popout-closed', workspaceId })
}

/**
 * Fire-and-forget: tell any listening tab which workspace the Library
 * pop-out is CURRENTLY viewing. Call this on every in-tab navigation
 * (including the initial mount), not only at teardown — see the module doc
 * for why `pagehide` alone is not a reliable moment to post this.
 */
export function announceLibraryWorkspaceChanged(workspaceId?: string): void {
  postMessage({ type: 'workspace-changed', workspaceId })
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

/**
 * Subscribes to continuous Library pop-out workspace-changed events (see
 * module doc — published on every in-tab navigation, not only at close).
 * Returns an unsubscribe function that also closes the channel. No-ops when
 * BroadcastChannel isn't available.
 */
export function onLibraryWorkspaceChanged(callback: (workspaceId?: string) => void): () => void {
  const channel = openChannel()
  if (!channel) return () => {}
  const handler = (event: MessageEvent<LibraryHandoffMessage>) => {
    const data = event.data
    if (data && data.type === 'workspace-changed') {
      callback(data.workspaceId)
    }
  }
  channel.addEventListener('message', handler)
  return () => {
    channel.removeEventListener('message', handler)
    channel.close()
  }
}
