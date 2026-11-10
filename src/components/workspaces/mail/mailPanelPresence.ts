// mailPanelPresence.ts — the Mail panel's presence adapter (W3 spec §3.1
// "SPA presence lifecycle hooks" — W3 is the interface's single publisher,
// landing-order register row 20; US-5 / FR-W3-12). It observes the panel's
// visibility lifecycle and emits the generated `mail_panel_observer` frames
// on the authenticated socket:
//
//   open   — a Mail panel became visible with a resolved workspace
//            (mount-driven; the shell unmounts panel content when the panel
//            closes, so mount/unmount IS the visibility lifecycle).
//   close  — panel close (unmount), workspace switch (close old + open new
//            with fresh ids, spec Q4/US-5 AS-8), best-effort `pagehide`
//            (correctness never depends on it — the gateway reaps the
//            observer by socket loss, US-5 AS-3), and logout (the existing
//            socket teardown removes observers; the adapter sends nothing
//            on a dead connection, U9).
//
// Frame shape is the generated MailPanelObserverFrame — exactly the four
// keys { type, action, observer_id, workspace_id } (MC-W3-5): identity comes
// from the authenticated connection; no user id, session id or mailbox id
// travels. `observer_id` is a fresh opaque value per panel instance per
// connection (A-2: crypto.randomUUID()) — a reconnect re-opens with a fresh
// id and the stale id is never reused (U8).
//
// Presence is an optimization signal, never an authorization or correctness
// dependency (US-5 AS-7): every send is best-effort — an unacknowledged or
// unsendable frame changes nothing about how reads behave.

import type { MailPanelObserverFrame } from '@/lib/api/generated/asyncapi-types'

/** The one frame constructor (MC-W3-5): exactly the generated frame's four
 * keys, nothing else. Kept exported so the shape pin has a single seam. */
export function mailPanelObserverFrame(
  action: MailPanelObserverFrame['action'],
  observerId: string,
  workspaceId: string,
): MailPanelObserverFrame {
  return {
    type: 'mail_panel_observer',
    action,
    observer_id: observerId,
    workspace_id: workspaceId,
  }
}

/**
 * The socket handle the adapter sends through, typed against the GENERATED
 * MailPanelObserverFrame. The live WsConnection satisfies it (its
 * `send(frame: ClientFrame)` method parameter is bivariant against this
 * narrower frame type); the frame itself rides `WsConnection.send`'s
 * JSON.stringify path at runtime.
 */
export type MailPresenceSender = { send(frame: MailPanelObserverFrame): boolean }

export interface MailPanelPresenceDeps {
  /** The authenticated socket, or null while disconnected / logged out. */
  getSender(): MailPresenceSender | null
  /** Whether the socket is currently connected and authenticated. */
  isConnected(): boolean
  /** Notifies on connection-state changes (reconnect → re-open). */
  subscribeConnection(listener: () => void): () => void
}

export interface MailPanelPresence {
  /** The panel became visible on this workspace (idempotent while the
   * workspace is unchanged; a workspace switch closes the old observer and
   * opens one for the new workspace — each open carries a fresh id). */
  open(workspaceId: string): void
  /** The panel is no longer visible — sends the close frame for the
   * current observer (best-effort) and forgets it. */
  close(): void
  /** Best-effort close on `pagehide` — same frame as close(); correctness
   * never depends on it arriving. */
  handlePagehide(): void
  /** The observer id issued for the CURRENT connection's open frame, or
   * null while closed/unbound — the id REST reads (observer_id query param)
   * and mint/save requests carry so their panel semantics ride the same
   * observer as the frames (register row 6). */
  currentObserverId(): string | null
  /** Stops listening; called on panel unmount after the close frame. */
  dispose(): void
}

function freshObserverId(): string {
  const random = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`
  return `obs-${random}`
}

export function createMailPanelPresence(deps: MailPanelPresenceDeps): MailPanelPresence {
  /** The workspace the panel is currently open on, or null when closed. */
  let openWorkspaceId: string | null = null
  /** The observer id issued for the CURRENT connection's open frame. A
   * reconnect mints a fresh one; the stale id is never reused (U8). */
  let observerId: string | null = null
  let lastConnected = deps.isConnected()

  function sendOpen(): void {
    if (openWorkspaceId === null) return
    const sender = deps.getSender()
    if (sender === null) return
    observerId = freshObserverId()
    // Best-effort: a false return (socket closing) is fine — reads stay
    // request-scoped until the next reconnect re-opens (US-5 AS-7).
    sender.send(mailPanelObserverFrame('open', observerId, openWorkspaceId))
  }

  function sendClose(): void {
    if (observerId === null || openWorkspaceId === null) return
    const sender = deps.getSender()
    if (sender !== null) {
      sender.send(mailPanelObserverFrame('close', observerId, openWorkspaceId))
    }
  }

  const unsubscribe = deps.subscribeConnection(() => {
    const connected = deps.isConnected()
    const wasConnected = lastConnected
    lastConnected = connected
    if (openWorkspaceId === null) return
    if (connected && !wasConnected) {
      // Socket re-established while the panel is still open: re-open with a
      // FRESH observer id (U8 / §7 scenario 5.5) — the gateway never saw
      // this connection's observer.
      sendOpen()
    }
    // A disconnect sends nothing (the socket is gone); the gateway reaps the
    // observer by socket loss, and logout tears down with the socket (U9).
  })

  return {
    open(workspaceId: string): void {
      if (openWorkspaceId === workspaceId && observerId !== null) return
      if (openWorkspaceId !== null) {
        // Workspace switch while the panel stays mounted (US-5 AS-8): close
        // the old workspace's observer, then open the new one.
        sendClose()
      }
      openWorkspaceId = workspaceId
      observerId = null
      sendOpen()
    },
    close(): void {
      sendClose()
      openWorkspaceId = null
      observerId = null
    },
    handlePagehide(): void {
      sendClose()
    },
    currentObserverId(): string | null {
      return observerId
    },
    dispose(): void {
      unsubscribe()
    },
  }
}
