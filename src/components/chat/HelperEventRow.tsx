import { useEffect, useState } from 'react'
import type { ChatMessage } from '@/store/chat/types'
import { OutcomeDisclosure } from './OutcomeDisclosure'

type HelperEvent = NonNullable<ChatMessage['helperEvent']>

// A live row can move between the live tail and the virtualized history. Keep
// its announcement receipt across that remount without retaining old payloads.
// Replay dedup preserves this payload object; cold/rebuilt history has no cue.
const announcedEvents = new WeakSet<HelperEvent>()

export function HelperEventRow({ event, announce = false, pending = false }: {
  event: HelperEvent
  announce?: boolean
  pending?: boolean
}) {
  const [live] = useState(() => announce && !announcedEvents.has(event))
  const [text, setText] = useState(live ? '' : event.text)
  useEffect(() => {
    if (!live) return
    announcedEvents.add(event)
    // Mount the empty live region before changing its content. The visible
    // sentence IS the announcement, not a second hidden copy of the sentence.
    setText(event.text)
  }, [event, live])

  return (
    <OutcomeDisclosure
      label={event.text}
      data-testid="helper-event-line"
      data-helper-event-kind={event.kind}
      data-repair-pending={pending || undefined}
    >
      <span
        aria-live={live ? 'polite' : undefined}
        aria-atomic={live ? 'true' : undefined}
        className="block truncate text-[var(--color-muted)] group-open:whitespace-pre-wrap group-open:break-words"
      >
        {text}
      </span>
    </OutcomeDisclosure>
  )
}
