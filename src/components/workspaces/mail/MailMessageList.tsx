// MailMessageList — the list zone of the Mail panel (US-3): rows with
// unread dot, subject, sender or outgoing recipient, timestamp, and the US-6
// "Read by agent" tag (exactly once per flagged message — the tag marks the
// flag, not the read state). Invalid or zero dates have no timestamp.
//
// W3 additions (US-6 AS-1 / FR-W3-14; §2.1 file row): rows carry the
// PAPERCLIP indicator keyed on the generated `has_attachments` flag —
// never derived from an attachments array length (counterexample
// mutation 10) — with the accessible text "Has attachments"; false/absent
// renders no indicator at all. Each row is a stable focus-return target
// (`data-mail-message-row`, US-7 AS-2's middle fallback). The Load-more
// affordance sits below the list (US-3 AS-1) — the panel passes the
// paging props; the ceiling/search-ceiling states stay MailPanel's.
import { Paperclip } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { mailUidRef } from '@/lib/api/mail'
import { formatMailTime } from './mail-format'
import type { MailMessageSummary } from '@/lib/api'

export interface MailMessageListProps {
  messages: MailMessageSummary[]
  selectedRef: string | null
  onSelect(message: MailMessageSummary): void
  /** Load more (US-3): present below the list when more rows exist in this
   * view; absent entirely when the view is exhausted or at the ceiling. */
  onLoadMore?: () => void
  hasMore?: boolean
  loadingMore?: boolean
}

export function MailMessageList({ messages, selectedRef, onSelect, onLoadMore, hasMore, loadingMore }: MailMessageListProps) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
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
                // Stable focus-return identity for the handoff return path
                // (US-7 AS-2's message-row fallback).
                data-mail-message-row="true"
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
      {hasMore === true && onLoadMore !== undefined && (
        <div className="flex shrink-0 justify-center border-t border-[var(--color-border)] p-[var(--space-2)]">
          <Button
            variant="outline"
            size="sm"
            disabled={loadingMore === true}
            onClick={onLoadMore}
            data-testid="mail-load-more"
          >
            {loadingMore === true ? 'Loading…' : 'Load more'}
          </Button>
        </div>
      )}
    </div>
  )
}

/** Row top line: subject (bold when unread) + the US-6 read-by-agent tag +
 * the paperclip attachment indicator + the timestamp. */
function ListRowTop({ message }: { message: MailMessageSummary }) {
  const timestamp = formatMailTime(message.date)
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
        {message.has_attachments === true && (
          // Paperclip: keyed on the generated flag only (US-6 AS-1) —
          // accessible text pinned to "Has attachments".
          <span
            className="ml-[var(--space-1)] inline-flex items-center align-baseline text-[var(--color-muted)]"
            aria-label="Has attachments"
            role="img"
          >
            <Paperclip size={12} aria-hidden="true" />
          </span>
        )}
      </span>
      {timestamp !== '' && (
        <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
          {timestamp}
        </span>
      )}
    </span>
  )
}

/** Row bottom line: the sender in Inbox, recipients in Drafts and Sent. */
function ListRowBottom({ message }: { message: MailMessageSummary }) {
  const address = message.folder === 'sent' || message.folder === 'drafts'
    ? `To: ${message.to.length > 0 ? message.to.join(', ') : 'No recipient'}`
    : message.from
  return (
    <span className="mt-[var(--space-0-5)] flex items-baseline justify-between gap-[var(--space-2)]">
      <span className="min-w-0 truncate text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {address}
      </span>
    </span>
  )
}
