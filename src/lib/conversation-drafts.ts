// Drafts live for this runtime mount, keyed by conversation, never by the
// app-wide composer. AssistantUI's threadList adapter creates a fresh core
// on a key change; an asynchronous send keeps the old core and its onNew.
import { useEffect, useRef } from 'react'
import type { AssistantRuntime } from '@assistant-ui/react'
import { useUiStore } from '@/store/ui'

type Draft = ReturnType<AssistantRuntime['thread']['composer']['getState']>

function restoreAttachments(runtime: AssistantRuntime, attachments: Draft['attachments']): void {
  for (const attachment of attachments) {
    const input = 'file' in attachment && attachment.file instanceof File ? attachment.file : {
      id: attachment.id, name: attachment.name, type: attachment.type,
      contentType: attachment.contentType, content: attachment.content ?? [],
    }
    void runtime.thread.composer.addAttachment(input).catch(error => {
      console.error('[conversation-drafts] Could not restore attachment', error)
      useUiStore.getState().addToast({ message: `Could not restore attachment "${attachment.name}". Reattach it before sending.`, variant: 'error' })
    })
  }
}

export function useConversationDrafts(runtime: AssistantRuntime, key: string, adoptFrom?: string) {
  const drafts = useRef(new Map<string, Draft>())
  const currentKey = useRef(key)
  useEffect(() => {
    currentKey.current = key
    const composer = runtime.thread.composer
    const draft = drafts.current.get(key) ?? (adoptFrom ? drafts.current.get(adoptFrom) : undefined)
    if (draft) {
      composer.setText(draft.text)
      composer.setRunConfig(draft.runConfig)
      composer.setQuote(draft.quote)
      restoreAttachments(runtime, draft.attachments)
    }
    const save = () => { drafts.current.set(key, composer.getState()) }
    // Refresh AssistantUI's lazy snapshot before reconnecting its subscription.
    // A disconnected thread switch otherwise leaves the old cached text visible.
    save()
    const unsubscribe = composer.subscribe(save)
    return unsubscribe
  }, [runtime, key, adoptFrom])

  return (ownerKey: string, text: string, attachments: Draft['attachments'] = []) => {
    const draft = drafts.current.get(ownerKey)
    // Preserve anything typed while the accepted send was awaiting uploads.
    const restoredText = draft?.text ? `${text}\n${draft.text}` : text
    if (draft) drafts.current.set(ownerKey, { ...draft, text: restoredText, attachments: [...draft.attachments, ...attachments] })
    // A refused A send must never put its text into the now-visible B composer.
    if (currentKey.current === ownerKey) {
      runtime.thread.composer.setText(restoredText)
      restoreAttachments(runtime, attachments)
    }
  }
}
