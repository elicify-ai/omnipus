// MailMessageList — the list zone of the Mail panel (US-3): rows with
// unread dot, subject, sender, timestamp, and the US-6 "Read by agent" tag
// (exactly once per flagged message — the tag marks the flag, not the read
// state). Timestamps render via formatMailTime; absent dates render as ''
// (the panel tolerates partial payloads in tests).
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { mailUidRef } from '@/lib/api/mail'
import { formatMailTime } from './mail-format'
import type { MailMessageSummary } from '@/lib/api'

export interface MailMessageListProps {
  messages: MailMessageSummary[]
  selectedRef: string | null
  onSelect(message: MailMessageSummary): void
}

export function MailMessageList({ messages, selectedRef, onSelect }: MailMessageListProps) {
  return (
    <ul data-testid="mail-message-list" aria-label="Messages" className="min-w-0 flex-1 list-none overflow-y-auto">
      {messages.length === 0 && (
        <li className="p-[var(--space-4)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          No messages
        </li>
      )}
      {messages.map((message) => {
        const ref = mailUidRef(message.uidvalidity, message.uid)
        const selected = selectedRef === ref
        return (
          <li key={ref}>
            <Button
              variant="ghost"
              onClick={() => onSelect(message)}
              aria-current={selected ? 'true' : undefined}
              className={cn(
                'h-auto w-full items-start gap-[var(--space-2)] whitespace-normal rounded-none border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-2)] text-left',
                selected ? 'bg-[var(--color-surface-2)]' : 'hover:bg-[var(--color-surface-1)]',
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  'mt-[var(--space-1)] h-2 w-2 shrink-0 rounded-full',
                  message.seen === false ? 'bg-[var(--color-accent)]' : 'bg-transparent',
                )}
              />
              <span className="min-w-0 flex-1">
                <ListRowTop message={message} />
                <ListRowBottom message={message} />
              </span>
            </Button>
          </li>
        )
      })}
    </ul>
  )
}

/** Row top line: subject (bold when unread) + the US-6 read-by-agent tag +
 * the timestamp. */
function ListRowTop({ message }: { message: MailMessageSummary }) {
  return (
    <span className="flex items-baseline justify-between gap-[var(--space-2)]">
      <span
        className={cn(
          'min-w-0 truncate text-[length:var(--type-body-compact-size)]',
          message.seen === false
            ? 'font-[var(--font-weight-medium)] text-[var(--color-secondary)]'
            : 'text-[var(--color-secondary)]',
        )}
      >
        {message.subject}
        {message.read_by_agent === true && (
          <span className="ml-[var(--space-2)] whitespace-nowrap rounded-full bg-[var(--color-surface-3)] px-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            Read by agent
          </span>
        )}
      </span>
      <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {formatMailTime(message.date)}
      </span>
    </span>
  )
}

/** Row bottom line: the sender. */
function ListRowBottom({ message }: { message: MailMessageSummary }) {
  return (
    <span className="mt-[var(--space-0-5)] flex items-baseline justify-between gap-[var(--space-2)]">
      <span className="min-w-0 truncate text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {message.from}
      </span>
    </span>
  )
}
