// ADR-051 Rev 4 (Slice H) — pending workspace-library attachments for the
// composer.
//
// The native AssistantUI attachment adapter (attachment-adapter.ts) is built
// around local File objects: add() registers a pending file and send() uploads
// it to /api/v1/uploads, yielding a fresh `media://` ref. A workspace LIBRARY
// attachment is different — the bytes already persist in the library, so there
// is nothing to upload. The user picks an existing MediaLibraryEntry from the
// composer picker and it must flow into the outgoing message frame as a
// `media://workspace/<workspace_id>/<media_id>` ref (FR-022) WITHOUT going
// through the File-based adapter.
//
// This module is the non-File analogue of attachment-adapter.ts's
// `resolvedUploads` map: a small module-level store of pending library refs.
// omnipus-runtime.ts::onNew drains it (alongside the File-based
// takeResolvedUpload) so a send carries both freshly-uploaded files and reused
// library entries in one `media` array. The composer renders the pending set as
// chips via the `useLibraryAttachments()` hook (useSyncExternalStore), so add /
// remove updates the chip row synchronously without prop-drilling.

// #1428 step 2 (spec 4.4.3): the pending set is no longer a module-level
// list shared by every chat. It is the open chat's draft `libraryPicks` in
// the draft store, so a pick made in one chat never rides another chat's
// message. The functions below keep their names and act on the open draft.

import { useSessionStore } from '@/store/session'
import {
  NO_CHAT_KEY,
  addLibraryPick,
  clearLibraryPicks,
  getDraft,
  openDraftKey,
  removeLibraryPick,
  useDrafts,
  type LibraryPick,
} from '@/store/chat/drafts'

/** A staged workspace-library entry: the open draft's library pick. */
export type LibraryAttachment = LibraryPick

const NO_PICKS: LibraryAttachment[] = []

/** Discriminator prefix for workspace-library media refs — mirrors the Go
 * side's `media.WorkspaceRefPrefix` (pkg/media/resolve.go). */
const WORKSPACE_REF_PREFIX = 'media://workspace/'

/**
 * Build the canonical workspace-library ref shape. Centralised so the composer
 * picker, the runtime, and any future caller agree on the exact string the
 * backend resolver expects (FR-028a matches the workspace segment against the
 * entry's workspace_id).
 */
export function buildWorkspaceMediaRef(workspaceId: string, mediaId: string): string {
  return `${WORKSPACE_REF_PREFIX}${workspaceId}/${mediaId}`
}

/**
 * Convert a `media://` ref into its GET preview URL. Mirrors
 * pkg/gateway/webchat_channel.go::mediaRefURL exactly (see the Go-side
 * TestWebchatChannel_MediaRefURL / TestReplay_MediaRefURL cases) — a
 * workspace-scoped ref (`media://workspace/<ws>/<id>`) resolves through
 * `/api/v1/media/workspace/<ws>/<id>` (HandleMediaByRef, rest.go:9148);
 * every other `media://<id>` ref resolves through the legacy
 * `/api/v1/media/<id>` (HandleMedia, rest.go:9131). HandleMedia does a bare
 * map lookup keyed by the FULL ref and rejects any id containing "/", so a
 * workspace-scoped ref can only ever resolve via the `/workspace/` route —
 * do not invent a different shape, and keep this in lockstep with the Go
 * implementation if it changes.
 */
export function mediaRefURL(ref: string): string {
  if (ref.startsWith(WORKSPACE_REF_PREFIX)) {
    return `/api/v1/media/workspace/${ref.slice(WORKSPACE_REF_PREFIX.length)}`
  }
  return `/api/v1/media/${ref.replace(/^media:\/\//, '')}`
}

/** Stage a library entry on the open chat's draft. Returns its id. */
export function addLibraryAttachment(
  entry: Omit<LibraryAttachment, 'id'>,
): string {
  return addLibraryPick(openDraftKey(), entry)
}

/** Remove a staged entry from the open chat's draft. */
export function removeLibraryAttachment(id: string): void {
  removeLibraryPick(openDraftKey(), id)
}

/** Clear the open chat's staged entries. */
export function clearLibraryAttachments(): void {
  clearLibraryPicks(openDraftKey())
}

/** Drain the open chat's staged entries (a send that carries them). */
export function takeLibraryAttachments(): LibraryAttachment[] {
  const key = openDraftKey()
  const drained = getDraft(key).libraryPicks
  if (drained.length > 0) clearLibraryPicks(key)
  return drained
}

/** React subscription to the open chat's staged entries. */
export function useLibraryAttachments(): LibraryAttachment[] {
  const sid = useSessionStore((s) => s.activeSessionId)
  const newChatKey = useSessionStore((s) => s.newChatKey)
  const raw = sid && sid !== '__pending' ? sid : newChatKey
  return useDrafts((s) => {
    const key = raw ? (s.aliasByKey[raw] ?? raw) : NO_CHAT_KEY
    return s.drafts[key]?.libraryPicks ?? NO_PICKS
  })
}

/** The open chat's staged entries, outside React. */
export function getLibraryAttachments(): LibraryAttachment[] {
  return getDraft(openDraftKey()).libraryPicks
}
