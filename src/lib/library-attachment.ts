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

import { useSyncExternalStore } from 'react'

/** One pending library-ref attachment, drained by onNew on send. */
export interface LibraryAttachment { // not-wire-format: client-side UI store for pending composer attachments; never serialized over the wire
  /** Stable client-side id (used as the React key + remove target). */
  id: string
  /** The media-library entry id (UUID) within the workspace. */
  mediaId: string
  /** Fully-qualified ref threaded into the WS `media` field. */
  ref: string
  /** Display filename (from the manifest, not a local File). */
  filename: string
  /** MIME sniffed from the stored bytes. */
  contentType: string
}

let attachments: LibraryAttachment[] = []
const listeners = new Set<() => void>()

function emit(): void {
  for (const l of listeners) l()
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function getSnapshot(): LibraryAttachment[] {
  return attachments
}

let counter = 0

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

/** Add a pending library attachment. Returns the new attachment's id. */
export function addLibraryAttachment(
  entry: Omit<LibraryAttachment, 'id'>,
): string {
  const id = `lib-${Date.now()}-${counter++}`
  attachments = [...attachments, { id, ...entry }]
  emit()
  return id
}

/** Remove a pending library attachment by its client-side id. */
export function removeLibraryAttachment(id: string): void {
  if (!attachments.some((a) => a.id === id)) return
  attachments = attachments.filter((a) => a.id !== id)
  emit()
}

/** Drop every pending library attachment (e.g. on session/workspace change). */
export function clearLibraryAttachments(): void {
  if (attachments.length === 0) return
  attachments = []
  emit()
}

/**
 * Drain (return and clear) every pending library attachment. Called by
 * omnipus-runtime.ts::onNew when the composer sends, mirroring
 * takeResolvedUpload for the File-based path. Draining (not just reading) on
 * send guarantees a single send cannot re-thread the same ref into a later
 * message if the send itself fails downstream.
 */
export function takeLibraryAttachments(): LibraryAttachment[] {
  const drained = attachments
  if (drained.length > 0) {
    attachments = []
    emit()
  }
  return drained
}

/** Reactive read of the pending set for the composer chip row. */
export function useLibraryAttachments(): LibraryAttachment[] {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

/** Non-reactive read of the pending set (for inspection outside React). */
export function getLibraryAttachments(): LibraryAttachment[] {
  return attachments
}
