// MailPanel — the docked workspace Mail panel (D4/D11), in the Library's
// hosting shape: a plain flex <aside> sibling of the chat column with a
// chrome-header toolbar, mailbox picker, pop-out and close controls. The
// fullscreen pop-out tab and the ui-store wiring land with the real build
// (spec §16); this prototype is presentational, sample-data driven (D35).
import { useState } from 'react'
import {
  ArrowSquareOut,
  Tray,
  X,
} from '@phosphor-icons/react'
import { IconButton } from '@/components/ui/icon-button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { MailConnectionBanner } from './MailConnectionBanner'
import { MailExplorer } from './MailExplorer'
import { MailComposeDialog } from './MailComposeDialog'
import type { MailSampleData } from './sampleMail'

export interface MailPanelProps {
  data: MailSampleData
  loading?: boolean
  /** Render the compose dialog open (compose story). */
  composeOpen?: boolean
  /** Initial folder (empty-folder story). */
  initialFolder?: 'inbox' | 'sent' | 'drafts'
  /** Initial selection (reading states). */
  initialSelectedId?: string
  onPopOut?: () => void
  onClose?: () => void
  className?: string
}

export function MailPanel({
  data,
  loading,
  composeOpen = false,
  initialFolder,
  initialSelectedId,
  onPopOut,
  onClose,
  className,
}: MailPanelProps) {
  const [mailboxId, setMailboxId] = useState(data.activeMailboxId)
  const [compose, setCompose] = useState(composeOpen)
  const activeMailbox = data.mailboxes.find((m) => m.id === mailboxId)
  const connection = data.connection
  return (
    <aside
      data-testid="mail-panel"
      aria-label="Mail panel"
      className={cn(
        'flex h-full w-full flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]',
        className,
      )}
    >
      {/* Toolbar — the LibraryPanel header's shape (chrome-header row on
          surface-1, icon actions right). */}
      <header className="flex h-chrome-header shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)]">
        <Tray size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
        <h2 className="shrink-0 text-[length:var(--type-body-compact-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
          Mail
        </h2>
        <div className="min-w-0 flex-1">
          <MailboxPicker
            mailboxes={data.mailboxes}
            value={mailboxId}
            onChange={setMailboxId}
          />
        </div>
        {onPopOut && (
          <IconButton aria-label="Open Mail in fullscreen" size="sm" onClick={onPopOut}>
            <ArrowSquareOut size={14} aria-hidden="true" />
          </IconButton>
        )}
        {onClose && (
          <IconButton aria-label="Close Mail panel" size="sm" onClick={onClose}>
            <X size={14} aria-hidden="true" />
          </IconButton>
        )}
      </header>
      {/* Last-checked line — the watcher's honest "last checked" state
          (FR-033/D32: it logs nothing on success, so the panel shows it). */}
      <p className="shrink-0 border-b border-[var(--color-border)] bg-[var(--color-surface-0)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {activeMailbox ? `${activeMailbox.address} · ` : ''}
        Last checked {new Date(data.lastChecked).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
      </p>
      {connection.status !== 'ok' && connection.message && (
        <MailConnectionBanner
          status={connection.status}
          message={connection.message}
          nextRetryAt={connection.nextRetryAt}
          onRetry={() => { /* prototype: no fetch to retry */ }}
        />
      )}
      <MailExplorer data={data} loading={loading} initialFolder={initialFolder} initialSelectedId={initialSelectedId} />
      <MailComposeDialog
        open={compose}
        onOpenChange={setCompose}
        mailbox={activeMailbox}
      />
    </aside>
  )
}

interface MailboxPickerProps {
  mailboxes: MailSampleData['mailboxes']
  value: string
  onChange: (id: string) => void
}

/** FR-010: workspace mailbox picker, shown when a workspace has mailboxes. */
export function MailboxPicker({ mailboxes, value, onChange }: MailboxPickerProps) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger
        aria-label="Choose mailbox"
        className="h-7 w-full min-w-0 max-w-[240px] justify-between gap-[var(--space-1)]"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {mailboxes.map((mailbox) => (
          <SelectItem key={mailbox.id} value={mailbox.id}>
            {mailbox.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
