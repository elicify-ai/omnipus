// MailMessageList — the folder's message list (LibraryEntryRow's visual
// language: rows with icon, name, caption metadata). Rows are activation
// targets only in this prototype; the real feature wires them to the folder
// read + seen endpoints (FR-020) and refetches the 30s poll (D25).
import {
  Paperclip,
  Plus,
  Robot,
  Tray,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { Skeleton } from '@/components/ui/skeleton'
import { formatMailTime } from './sampleMail'
import type { MessageDetail } from './sampleMail'

export interface MailMessageListProps {
  folderLabel: string
  messages: MessageDetail[]
  selectedId?: string
  loading?: boolean
  onSelect: (message: MessageDetail) => void
  onCompose: () => void
  className?: string
}

export function MailMessageList({
  folderLabel,
  messages,
  selectedId,
  loading,
  onSelect,
  onCompose,
  className,
}: MailMessageListProps) {
  const unreadCount = messages.filter((m) => m.unread).length
  return (
    <section
      aria-label={`${folderLabel} messages`}
      className={cn('flex min-w-0 flex-1 flex-col', className)}
    >
      <header className="flex h-chrome-header shrink-0 items-center justify-between gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)]">
        <h3 className="truncate text-[length:var(--type-body-compact-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
          {folderLabel}
          {unreadCount > 0 && ` (${unreadCount} unread)`}
        </h3>
        <Button size="sm" variant="outline" onClick={onCompose} className="shrink-0 gap-[var(--space-1)]">
          <Plus size={14} aria-hidden="true" />
          Compose
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto p-[var(--space-1)]">
        {loading ? (
          <LoadingRows />
        ) : messages.length === 0 ? (
          <EmptyState
            icon={<Tray size={40} aria-hidden="true" />}
            message="Nothing in this folder"
          />
        ) : (
          <ul className="flex flex-col gap-[var(--space-0-5)]">
            {messages.map((message) => (
              <li key={message.id}>
                <MailMessageRow
                  message={message}
                  selected={message.id === selectedId}
                  onSelect={() => onSelect(message)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  )
}

interface MailMessageRowProps {
  message: MessageDetail
  selected: boolean
  onSelect: () => void
}

/**
 * One message row. Unread: accent dot + medium-weight text (the mail-client
 * convention). D38: a small "Read by agent" tag on messages the agent read
 * via read_message, so the human still sees what it handled.
 */
function MailMessageRow({ message, selected, onSelect }: MailMessageRowProps) {
  const sender = message.folder === 'sent' ? `To: ${message.toNames.join(', ')}` : message.fromName
  return (
    <Button
      variant="ghost"
      onClick={onSelect}
      aria-current={selected ? 'true' : undefined}
      className={cn(
        'h-auto w-full justify-start gap-[var(--space-2)] rounded-md border border-transparent px-[var(--space-2-5)] py-[var(--space-2)] text-left font-[var(--font-weight-regular)]',
        selected
          ? 'border-[var(--color-accent)]/40 bg-[var(--color-surface-2)]'
          : 'hover:bg-[var(--color-surface-2)]',
      )}
    >
      <span aria-hidden="true" className="flex w-2 shrink-0 justify-center">
        {message.unread && <span className="h-1.5 w-1.5 rounded-full bg-[var(--color-accent)]" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-[var(--space-1)]">
          <span
            className={cn(
              'truncate text-[length:var(--type-body-compact-size)]',
              message.unread
                ? 'font-[var(--font-weight-medium)] text-[var(--color-secondary)]'
                : 'text-[var(--color-muted)]',
            )}
          >
            {sender}
          </span>
        </span>
        <span className="flex items-center gap-[var(--space-1)]">
          <span className="truncate text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            {message.subject}
          </span>
          {message.hasAttachments && <Paperclip size={12} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />}
        </span>
        <span className="flex items-center gap-[var(--space-1)]">
          <span className="truncate text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
            {message.preview}
          </span>
          {message.readByAgent && (
            <Badge variant="secondary" className="shrink-0 gap-[var(--space-1)]">
              <Robot size={10} aria-hidden="true" /> Read by agent
            </Badge>
          )}
        </span>
      </span>
      <span className="shrink-0 self-start text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {formatMailTime(message.date)}
      </span>
    </Button>
  )
}

/** Skeleton rows for the loading state (H1: always show system status). */
function LoadingRows() {
  return (
    <div aria-hidden="true" className="flex flex-col gap-[var(--space-2)] p-[var(--space-2)]">
      {[0, 1, 2, 3, 4].map((i) => (
        <div key={i} className="flex items-center gap-[var(--space-2)]">
          <Skeleton className="h-8 w-8 rounded-full" />
          <div className="flex flex-1 flex-col gap-[var(--space-1)]">
            <Skeleton className="h-3 w-2/5" />
            <Skeleton className="h-3 w-4/5" />
          </div>
        </div>
      ))}
    </div>
  )
}
