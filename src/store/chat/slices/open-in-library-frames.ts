// open-in-library-frames.ts — the live `open_in_library` frame (chat-links
// spec "What the screen does", FR-017, FR-018). Own pre-switch handler
// (mail-panel-observer-frames.ts pattern): the main switch in
// slices/frames.ts::handleFrame is a grandfathered budget entry, so this
// frame family does not grow it.
//
// The frame is unsequenced and never replayed by the server; this handler is
// the client-side defence in depth. It reads the tab's state at the moment
// the frame arrives, asks decideLibraryOpen once, and then either opens the
// docked Library (focus untouched, polite announcement), shows the session's
// one "Open in Library" notice, or does nothing at all. A frame for a session
// this tab is not showing is "not watching": its notice is kept for that
// session and appears when the user returns to that chat.

import { useSessionStore } from '@/store/session'
import { isChatViewMounted } from '@/store/chatView'
import {
  isLeaveDecisionPending,
  openLibraryAt,
  outgoingPanelNeedsLeaveConfirmation,
} from '@/components/library/openLibraryAt'
import { decideLibraryOpen } from '../decideLibraryOpen'
import {
  announceLibraryOpen,
  libraryFileName,
  rememberLibraryOpenRequest,
  showLibraryOpenNotice,
  wasLibraryOpenRequestHandled,
} from '../libraryOpen'
import type { ChatStore } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

/**
 * Watching, as this tab can see it (spec "What watching means"): the document
 * is visible, `sessionId` is the active session, and a chat view for it is
 * mounted. The same three facts `useChatPresence` reports to the gateway.
 */
export function isTabWatchingSession(sessionId: string): boolean {
  if (typeof document === 'undefined' || document.visibilityState !== 'visible') return false
  return useSessionStore.getState().activeSessionId === sessionId && isChatViewMounted(sessionId)
}

/** Returns true when the frame was an `open_in_library` frame and has been consumed. */
export function handleOpenInLibraryFrame(frame: Frame, get: () => ChatStore): boolean {
  if (frame.type !== 'open_in_library') return false
  const sessionId = frame.session_id
  const bucket = get().sessionsById[sessionId]
  const decision = decideLibraryOpen({
    tabWatching: isTabWatchingSession(sessionId),
    guardPending: isLeaveDecisionPending(),
    libraryDirty: outgoingPanelNeedsLeaveConfirmation(),
    replaying: bucket?.isReplaying === true,
    awaitingCatchUp: bucket?.awaitingCatchUp === true,
    hasSeq: 'seq' in (frame as object),
    requestId: frame.request_id,
    duplicate: wasLibraryOpenRequestHandled(sessionId, frame.request_id),
  })
  if (decision === 'drop') return true

  rememberLibraryOpenRequest(sessionId, frame.request_id)
  const target = { workspaceId: frame.workspace_id, path: frame.path }
  const name = libraryFileName(frame.path)
  if (decision === 'open') {
    openLibraryAt(target)
    announceLibraryOpen(`Opened ${name} in Library`)
  } else {
    showLibraryOpenNotice(sessionId, { ...target, requestId: frame.request_id })
    // Announce only in the chat it belongs to; another chat's notice waits
    // silently until the user returns there.
    if (useSessionStore.getState().activeSessionId === sessionId) {
      announceLibraryOpen(`The agent wants to show you ${name}`)
    }
  }
  return true
}
