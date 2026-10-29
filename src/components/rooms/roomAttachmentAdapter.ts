// roomAttachmentAdapter — assistant-ui attachments for a group chat's message
// box (#1449 US-3; ADR-20261028 A2.3, C-I1).
//
// Unlike the normal chat's adapter (src/lib/attachment-adapter.ts), which
// creates a chat session before it uploads, this one never touches a session
// (FR-199, S-23): it uploads into the ROOM's workspace library with
// `uploadWorkspaceFiles`, as soon as the file is added. The chip shows the
// upload running and, on failure, an explicit error (US-3 AS 3.3, S-22); the
// message box refuses to send while any chip is uploading or failed, so a
// failed file is never silently dropped from a message. At most 16 files go
// on one message (S-24): the 17th is refused when it is added.

import type { AttachmentAdapter, CompleteAttachment, PendingAttachment } from '@assistant-ui/react'
import { uploadWorkspaceFiles, getErrorMessage } from '@/lib/api'
import { MAX_UPLOAD_BYTES, isAcceptedFile } from '@/lib/attachment-adapter'
import { mediaRefURL } from '@/lib/library-attachment'
import { isImageAttachment } from '@/components/chat/AttachmentCard'
import { useUiStore } from '@/store/ui'

/** RoomMessageRequest.media `maxItems` (contract): files on one group message. */
export const MAX_ROOM_ATTACHMENTS = 16

/** One uploaded group attachment: the workspace media ref and its display metadata. */
export interface RoomUpload { // not-wire-format: in-browser upload result (the ref crosses the wire as a plain string)
  ref: string
  url: string
  filename: string
  contentType: string
}

export interface RoomAttachmentAdapter extends AttachmentAdapter {
  /** The upload of a completed or pending attachment, by attachment id. */
  uploadOf(attachmentId: string): RoomUpload | undefined
  /** Forget uploads after their message was accepted. */
  release(attachmentIds: readonly string[]): void
}

let counter = 0

/**
 * `workspaceId` is read at upload time (the room record may load after the
 * box mounts); `attachedCount` returns how many attachments the box holds.
 */
export function createRoomAttachmentAdapter(workspaceId: () => string | undefined, attachedCount: () => number): RoomAttachmentAdapter {
  const uploads = new Map<string, RoomUpload>()
  return {
    accept: 'image/*,application/pdf,.pdf,.docx,.pptx,.xlsx,.txt,.md,.csv,.json,.log,.yaml,.yml',
    async *add({ file }): AsyncGenerator<PendingAttachment, void> {
      if (attachedCount() >= MAX_ROOM_ATTACHMENTS) {
        const message = `A group message can carry at most ${MAX_ROOM_ATTACHMENTS} files.`
        useUiStore.getState().addToast({ message, variant: 'error' })
        throw new Error(message)
      }
      if (file.size > MAX_UPLOAD_BYTES) {
        const message = `"${file.name}" is too large (max 100 MB).`
        useUiStore.getState().addToast({ message, variant: 'error' })
        throw new Error(message)
      }
      if (!isAcceptedFile(file.name, file.type)) {
        throw new Error(`Can't attach "${file.name}": file type not supported`)
      }
      const pending: PendingAttachment = {
        id: `room-att-${Date.now()}-${counter++}-${file.name}`,
        type: isImageAttachment(file.name, file.type) ? 'image' : 'file',
        name: file.name,
        contentType: file.type || 'application/octet-stream',
        file,
        status: { type: 'running', reason: 'uploading', progress: 0 },
      }
      yield pending
      const workspace = workspaceId()
      try {
        if (!workspace) throw new Error('the group is still loading')
        const result = await uploadWorkspaceFiles(workspace, [file])
        const uploaded = result.files[0]
        const ref = typeof uploaded?.ref === 'string' ? uploaded.ref : undefined
        if (!uploaded || !ref) throw new Error('the file could not be registered')
        uploads.set(pending.id, { ref, url: mediaRefURL(ref), filename: file.name, contentType: uploaded.content_type || pending.contentType || 'application/octet-stream' })
        yield { ...pending, status: { type: 'requires-action', reason: 'composer-send' } }
      } catch (error) {
        // US-3 AS 3.3 / S-22: the error stays ON the chip; nothing falls back to a session.
        yield { ...pending, status: { type: 'incomplete', reason: 'error', message: getErrorMessage(error) } }
      }
    },
    async send(attachment): Promise<CompleteAttachment> {
      // The box only sends once every chip uploaded (sendBlocked); the content
      // is inert here — the ref travels as RoomMessageRequest.media.
      return { ...attachment, status: { type: 'complete' }, content: [] }
    },
    async remove(attachment): Promise<void> {
      uploads.delete(attachment.id)
    },
    uploadOf: (attachmentId) => uploads.get(attachmentId),
    release: (attachmentIds) => { for (const id of attachmentIds) uploads.delete(id) },
  }
}
