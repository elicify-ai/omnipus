// useRoomRuntime — the assistant-ui runtime a group chat's message box runs
// under (#1449 ADR-20261028 A2.3, C-I4; spike T-26: nested runtime, option C1).
//
// RoomScreen mounts it in its own AssistantRuntimeProvider, nested inside the
// app-wide one, so ComposerPrimitive and useComposerRuntime() below it talk to
// THIS runtime: its send posts to the group (useRoomSend) and nothing else —
// no chat session is created, sent to, cancelled or navigated (FR-015, FR-199).
// The runtime holds no messages (the shared list reads the room source) and
// is never running: the group has no Stop and no streaming (D5, D21).
//
// Sending: text plus the room members picked with "@" (`mentions`, bound to
// agent ids, FR-011) and the uploaded attachments (`media`, workspace refs).
// The composer clears its text on
// send; when the group refuses the message the text and the attachments are
// put back, so a refused message is never lost (the old box kept the text the
// same way). An uncertain save keeps the cleared box and offers Retry with the
// identical request (FR-216, useRoomSend).

import { useMemo, useRef } from 'react'
import { useExternalStoreRuntime } from '@assistant-ui/react'
import type { AppendMessage, ThreadMessageLike } from '@assistant-ui/react'
import { useUiStore } from '@/store/ui'
import { createRoomAttachmentAdapter } from './roomAttachmentAdapter'
import type { RoomSend } from './useRoomSend'

/** What the message box knows at send time that the runtime does not. */
export interface RoomSendBridge { // not-wire-format: in-browser hand-off between the box and the runtime
  /** Member ids explicitly picked with "@" whose token is still in `text`; cleared on success. */
  mentionsFor: (text: string) => string[]
  onAccepted: () => void
}

const NO_MESSAGES: never[] = []
const convertMessage = (): ThreadMessageLike => ({ role: 'user', content: [] })

export function useRoomRuntime(send: RoomSend, workspaceId: () => string | undefined, bridge: { current: RoomSendBridge | null }) {
  const runtimeRef = useRef<ReturnType<typeof useExternalStoreRuntime> | null>(null)
  const adapter = useMemo(
    () => createRoomAttachmentAdapter(workspaceId, () => runtimeRef.current?.thread.composer.getState().attachments.length ?? 0),
    // The workspace getter reads the current record each time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  )
  const runtime = useExternalStoreRuntime({
    messages: NO_MESSAGES,
    isRunning: false,
    convertMessage,
    adapters: { attachments: adapter },
    onNew: async (message: AppendMessage) => {
      const text = message.content
        .filter((part): part is { type: 'text'; text: string } => part.type === 'text')
        .map((part) => part.text)
        .join('\n')
      const attachments = message.attachments ?? []
      const uploads = attachments.map((attachment) => adapter.uploadOf(attachment.id))
      if (uploads.some((upload) => upload === undefined)) {
        // The box blocks sending until every chip uploaded; a missing upload
        // here is an internal fault, refused visibly with the text kept.
        useUiStore.getState().addToast({ message: 'Could not send — an attachment did not finish uploading.', variant: 'error' })
        restore(text, attachments)
        return
      }
      const media = uploads.map((upload) => upload!.ref)
      const trimmed = text.trim()
      const leaves = await send.send(trimmed, bridge.current?.mentionsFor(trimmed) ?? [], media)
      if (leaves) {
        adapter.release(attachments.map((attachment) => attachment.id))
        bridge.current?.onAccepted()
        return
      }
      restore(text, attachments)
    },
  })
  runtimeRef.current = runtime

  function restore(text: string, attachments: AppendMessage['attachments']) {
    const composer = runtimeRef.current?.thread.composer
    if (!composer) return
    composer.setText(text)
    for (const attachment of attachments ?? []) {
      // Already uploaded: re-add as a complete attachment, keeping its id so
      // its upload is found again on the next send.
      void composer.addAttachment({
        id: attachment.id,
        type: attachment.type,
        name: attachment.name,
        contentType: attachment.contentType,
        content: attachment.content ?? [],
      }).catch(() => {
        useUiStore.getState().addToast({ message: `"${attachment.name}" was removed from the message — attach it again.`, variant: 'error' })
      })
    }
  }

  return runtime
}
