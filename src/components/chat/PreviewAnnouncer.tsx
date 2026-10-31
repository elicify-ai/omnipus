// PreviewAnnouncer — one polite screen-reader announcement per office-preview
// thumbnail group (office-preview spec §5.4): "Preview of report.docx: 4
// pages" when a group's pages arrive, "Page previews unavailable" when its
// thumbnails could not be made. Mounted once per chat screen, outside the
// virtualized rows, so a row remount never re-announces.
//
// Groups already present when a session is opened, or delivered while the
// session replays its history (reload), are history — they are recorded as
// seen and never announced. A later state change of the same group (e.g. a
// Retry turning "unavailable" into pages) is announced once more.

import { useEffect, useRef, useState } from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { ChatMessage } from '@/store/chat'
import { partitionMedia } from './previewGroups'

interface Announcement {
  key: string
  text: string
}

function collectAnnouncements(messages: readonly ChatMessage[]): Announcement[] {
  const out: Announcement[] = []
  for (const message of messages) {
    if (!message.media?.length) continue
    for (const block of partitionMedia(message.media)) {
      if (block.kind !== 'group') continue
      const { group } = block
      // tool_call_id is unique per session; it survives a message id swap.
      const id = group.toolCallId ?? `${message.id}|${group.key}`
      if (group.unavailable) {
        out.push({ key: `${id}|unavailable`, text: 'Page previews unavailable' })
      } else if (group.pages.length > 0) {
        const name = group.preview?.source_name ?? group.pages[0]?.filename ?? 'document'
        const n = group.pages.length
        out.push({ key: `${id}|ready`, text: `Preview of ${name}: ${n} page${n === 1 ? '' : 's'}` })
      }
    }
  }
  return out
}

export function PreviewAnnouncer() {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const messages = useChatStore((s) => s.messages)
  const isReplaying = useChatStore((s) => s.isReplaying)
  const seenRef = useRef<{ sessionId: string | null; keys: Set<string> } | null>(null)
  const [text, setText] = useState('')

  useEffect(() => {
    const entries = collectAnnouncements(messages)
    const seen = seenRef.current
    if (!seen || seen.sessionId !== sessionId) {
      seenRef.current = { sessionId, keys: new Set(entries.map((e) => e.key)) }
      setText('')
      return
    }
    const fresh = entries.filter((e) => !seen.keys.has(e.key))
    for (const e of fresh) seen.keys.add(e.key)
    if (isReplaying || fresh.length === 0) return
    setText(fresh.map((e) => e.text).join('. '))
  }, [messages, isReplaying, sessionId])

  return (
    <div role="status" aria-live="polite" aria-atomic="true" className="sr-only" data-testid="preview-announcement">
      {text}
    </div>
  )
}
