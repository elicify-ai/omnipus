// #1428 step 2 (spec C r9, 4.5): the session binding of the composer.
//
// The draft store owns the session composer's text and files. The
// assistant-ui composer runtime is only a mirror of the open draft:
// - rule 1/6: a key change writes the new key's text with one synchronous
//   setText inside the store transition (mirrorOpenDraft 'key-transition');
//   a fresh runtime after a remount is synced once ('mount-sync');
// - rule 2: drop, paste and the picker capture the owning key at the event;
// - rule 4: Send is eligible when the store has text or files;
// - rule 5: every submit entry calls submitDraft, never composer.send;
// - rule 6: the slash menu gets an adapter that writes the store first and
//   then mirrors; its send() is submitDraft.

import { useEffect, useMemo } from 'react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { flushTapSync } from '@assistant-ui/tap'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { logError } from '@/lib/telemetry'
import { rejectAttachment } from '@/lib/attachment-adapter'
import { submitDraft, writeOpenDraftText } from '@/lib/bound-send'
import {
  NO_CHAT_KEY,
  addDraftFiles,
  getDraft,
  mirrorOpenDraft,
  openDraftKey,
  removeDraftFile,
  setDraftMirror,
  setDraftText,
  useDrafts,
  type DraftFile,
} from '@/store/chat/drafts'
import type { SlashComposer } from '@/hooks/useSlashMenu'
import type { FileSink } from '@/hooks/useFileUpload'

const NO_FILES: DraftFile[] = []

export interface SessionComposerBinding { // not-wire-format: React wiring of the session binding
  /** The open draft key (canonical). */
  openKey: string
  files: DraftFile[]
  /** Spec 4.5 rule 4: store text or store files. */
  canSend: boolean
  /** The composer's typed text, written to the open draft. */
  onTyped: (text: string) => void
  /** Every submit entry (spec 4.5 rule 5). */
  submit: () => void
  removeFile: (id: string) => void
  fileSink: FileSink
  slashComposer: SlashComposer
  /** The current-tab not-stopped notice is shown for the open chat. */
  notStoppedOpen: boolean
}

/** The open draft key, reactive to navigation and to the K -> S alias. */
export function useOpenDraftKey(): string {
  const sid = useSessionStore((s) => s.activeSessionId)
  const newChatKey = useSessionStore((s) => s.newChatKey)
  const raw = sid && sid !== '__pending' ? sid : newChatKey
  const alias = useDrafts((s) => (raw ? s.aliasByKey[raw] : undefined))
  return raw ? (alias ?? raw) : NO_CHAT_KEY
}

/** Attach routes write validated files into the key captured at their event. */
function commitToKey(key: string, files: File[]): void {
  const accepted: File[] = []
  for (const file of files) {
    const rejected = rejectAttachment(file)
    if (rejected === null) {
      accepted.push(file)
      continue
    }
    useUiStore.getState().addToast({ message: rejected, variant: 'error' })
    logError({ event: 'attachmentUploadFailed', fileName: file.name, message: rejected })
  }
  if (key !== NO_CHAT_KEY) addDraftFiles(key, accepted)
}

export function useSessionDraftBinding(runtime: ComposerRuntime): SessionComposerBinding {
  const openKey = useOpenDraftKey()
  const files = useDrafts((s) => s.drafts[openKey]?.files ?? NO_FILES)
  const hasText = useDrafts((s) => (s.drafts[openKey]?.text ?? '').trim().length > 0)
  const notStoppedOpen = useDrafts((s) => !!s.notStopped[openKey])

  // Rule 6: the one runtime text writer, plus the key-transition and mount sync.
  useEffect(() => {
    // assistant-ui publishes composer state on a macrotask; flushTapSync makes
    // the box show the new key's text in the same step (as ComposerPrimitive.Input
    // itself does for typed text), so no frame of the old key's text survives.
    setDraftMirror((text) => flushTapSync(() => runtime.setText(text)))
    let last = openDraftKey()
    if ((runtime.getState().text ?? '') !== getDraft(last).text) mirrorOpenDraft('mount-sync')
    const onChange = () => {
      const key = openDraftKey()
      if (key === last) return
      last = key
      mirrorOpenDraft('key-transition')
    }
    const offSession = useSessionStore.subscribe(onChange)
    const offChat = useChatStore.subscribe(onChange)
    return () => {
      offSession()
      offChat()
      setDraftMirror(null)
    }
  }, [runtime])

  const slashComposer = useMemo<SlashComposer>(() => ({
    getState: () => ({ text: getDraft(openDraftKey()).text }),
    setText: (text: string) => writeOpenDraftText(text),
    send: () => { submitDraft('Composer Send') },
    subscribe: (callback: () => void) => {
      const offChat = useChatStore.subscribe(callback)
      const offSession = useSessionStore.subscribe(callback)
      return () => {
        offChat()
        offSession()
      }
    },
  }), [])

  const fileSink = useMemo<FileSink>(() => ({ capture: () => openDraftKey(), commit: commitToKey }), [])

  return {
    openKey,
    files,
    canSend: hasText || files.length > 0,
    onTyped: (text: string) => {
      const key = openDraftKey()
      if (key !== NO_CHAT_KEY) setDraftText(key, text)
    },
    submit: () => { submitDraft('Composer Send') },
    removeFile: (id: string) => removeDraftFile(openKey, id),
    fileSink,
    slashComposer,
    notStoppedOpen,
  }
}
