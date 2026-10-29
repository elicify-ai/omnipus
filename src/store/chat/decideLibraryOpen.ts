// decideLibraryOpen.ts — what one tab does with one live `open_in_library`
// frame (chat-links spec "Replay guarantee" step 3, "What the screen does",
// FR-017, FR-018). Pure: every input is read by the caller at the moment the
// frame arrives, and the decision is made exactly once — a tab that becomes
// visible later does not re-decide (a late return gets the notice, never a
// late open).
//
//   drop   — the frame must not act at all, and nothing is shown: it carries a
//            `seq` (only unsequenced live delivery may act), the session's
//            bucket is replaying or awaiting catch-up, the frame is for a
//            session other than the one on screen, its request_id is not 32
//            lowercase hex, or that request_id was already handled.
//   open   — this tab is watching the chat, no leave decision is pending, and
//            replacing the open panel would not ask the user (no unsaved edits).
//   notice — every other arrival: a quiet "Open in Library" notice in the chat.
//
// Deliberately NOT inputs (founder decision 2026-10-15 superseded the phone
// gate): panel row width and the phone-takeover history entry. A phone the
// user is watching opens exactly like a wide window.

export type LibraryOpenDecision = 'open' | 'notice' | 'drop'

export interface LibraryOpenDecisionInput { // not-wire-format: SPA-local decision inputs, read when the frame arrives
  /** The tab is visible, the chat view for this session is mounted, and it is the active session. */
  tabWatching: boolean
  /** A panel leave decision (discard dialog) is already pending. */
  guardPending: boolean
  /** Replacing the open panel would ask first (unsaved Library — or Mail — edits). */
  libraryDirty: boolean
  /** The frame's session is the session this tab has on screen. */
  sessionOnScreen: boolean
  replaying: boolean
  awaitingCatchUp: boolean
  /** The raw frame carried a `seq` key. */
  hasSeq: boolean
  requestId: string
  /** `session_id` + `request_id` is already in the handled-set. */
  duplicate: boolean
}

const REQUEST_ID_RE = /^[0-9a-f]{32}$/

export function decideLibraryOpen(input: LibraryOpenDecisionInput): LibraryOpenDecision {
  if (input.hasSeq || input.replaying || input.awaitingCatchUp) return 'drop'
  if (!input.sessionOnScreen || input.duplicate) return 'drop'
  if (!REQUEST_ID_RE.test(input.requestId)) return 'drop'
  if (input.tabWatching && !input.guardPending && !input.libraryDirty) return 'open'
  return 'notice'
}
