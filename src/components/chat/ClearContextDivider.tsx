// ClearContextDivider.tsx — the /clear marker row's render and the quiet
// "earlier messages" line above it (founder ruling 2026-10-11, interim
// until #1266).
//
// The /clear marker entry a successful clear appends server-side
// ("Conversation context cleared", type system, role system,
// view_membership chat — pkg/agent/clear_session.go) renders as a quiet
// divider: hairline rules (the catalogued Separator primitive) flanking the
// marker text, muted type, no bubble. The live SystemMessage path and the
// virtualizer path in ChatScreen both route the marker here so the two
// surfaces cannot drift.
//
// When the session's chat view starts at that marker and earlier transcript
// entries exist before it (has_earlier_messages, read into chat state from
// every carrier — src/store/chat/has-earlier.ts), ONE quiet non-interactive
// line sits directly above the divider. The wording lives in exactly one
// place (EarlierMessagesNote below) so the founder can adjust it in one
// place. It is a statement, never a control: scroll-back of the earlier
// messages is #1266 and deliberately not built here.

import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { Separator } from '@/components/ui/separator'

/** The ONE place the line's wording lives (founder ruling 2026-10-11: exact text confirmed by the founder). */
const EARLIER_MESSAGES_NOTE_TEXT = 'Earlier messages are saved. The agent can still recall them.'

/**
 * The quiet line above the clear divider, shown ONLY when the server said
 * the view starts at the last /clear marker with earlier entries before it.
 * Non-interactive by design: no link, no button, no pager (#1266).
 */
function EarlierMessagesNote() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const hasEarlierMessages = useChatStore((s) =>
    activeSessionId ? s.sessionsById[activeSessionId]?.hasEarlierMessages === true : false,
  )
  if (!hasEarlierMessages) return null
  return (
    <div data-testid="earlier-messages-note" className="flex justify-center px-[var(--space-3)] pb-[var(--space-1)]">
      <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{EARLIER_MESSAGES_NOTE_TEXT}</span>
    </div>
  )
}

/**
 * FR-030/031 (U10b): the quiet divider the chat view shows for the ONE
 * system entry a successful /clear appends server-side. It reads as a
 * separation in the conversation, never as a message: hairline rules
 * flanking the marker text, muted type, no bubble — with the quiet
 * "Earlier messages are saved." line directly above it when there is
 * earlier history.
 */
export function ClearContextDivider({ messageId, content }: { messageId: string; content: string }) {
  return (
    <div data-message-role="system" data-message-id={messageId}>
      <EarlierMessagesNote />
      <div
        data-testid="clear-context-divider"
        className="flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)]"
      >
        <div className="min-w-0 flex-1">
          <Separator />
        </div>
        <span className="whitespace-nowrap text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{content}</span>
        <div className="min-w-0 flex-1">
          <Separator />
        </div>
      </div>
    </div>
  )
}
