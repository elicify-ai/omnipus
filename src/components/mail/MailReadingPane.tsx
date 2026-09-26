// MailReadingPane — the message reading surface: header block (subject,
// participants, date), the D17 "Load images" posture for HTML bodies, plain
// text bodies, and the D28 attachment list with per-file download actions.
import {
  DownloadSimple,
  File,
  Robot,
  ShieldWarning,
  X,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useState } from 'react'
import { MailHtmlFrame } from './MailHtmlFrame'
import { formatMailBytes } from './sampleMail'
import type { MessageDetail } from './sampleMail'

export interface MailReadingPaneProps {
  message: MessageDetail
  onReply: () => void
  onClose?: () => void
  /**
   * Fired when the human clears the D17 image block (Load images). The
   * parent owns the body swap — the prototype re-mints the sample body
   * client-side (see composeSampleBodyHtml), the real feature re-mints its
   * token server-side (FR-019).
   */
  onLoadImages?: () => void
  className?: string
}

export function MailReadingPane({ message, onReply, onClose, onLoadImages, className }: MailReadingPaneProps) {
  // D17: remote images ship blocked; loading them is an explicit,
  // per-message human action. Prototype: local swap of the sample body.
  const [imagesLoaded, setImagesLoaded] = useState(false)
  const isHtml = message.bodyKind === 'html'
  return (
    <section
      aria-label="Message"
      className={cn('flex min-w-0 flex-1 flex-col bg-[var(--color-surface-0)]', className)}
    >
      <header className="shrink-0 border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2-5)]">
        <div className="flex items-start justify-between gap-[var(--space-2)]">
          <h3 className="min-w-0 flex-1 text-[length:var(--type-section-title-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
            {message.subject}
          </h3>
          {onClose && (
            <IconButton aria-label="Close message" size="sm" onClick={onClose}>
              <X size={14} aria-hidden="true" />
            </IconButton>
          )}
        </div>
        <dl className="mt-[var(--space-1)] flex flex-col gap-[var(--space-0-5)]">
          <ParticipantRow label="From" value={`${message.fromName} <${message.fromAddress}>`} />
          <ParticipantRow label="To" value={message.toNames.join(', ')} />
          {message.ccNames.length > 0 && <ParticipantRow label="Cc" value={message.ccNames.join(', ')} />}
          <ParticipantRow label="Date" value={new Date(message.date).toLocaleString()} />
        </dl>
        <div className="mt-[var(--space-2)] flex items-center gap-[var(--space-1)]">
          {message.readByAgent && (
            <Badge variant="secondary" className="gap-[var(--space-1)]">
              <Robot size={10} aria-hidden="true" /> Read by agent
            </Badge>
          )}
          <Button variant="secondary" size="sm" onClick={onReply} className="ml-auto gap-[var(--space-1)]">
            Reply
          </Button>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {isHtml && message.remoteImagesBlocked && !imagesLoaded && (
          <div className="flex items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)]">
            <ShieldWarning size={16} aria-hidden="true" className="shrink-0 text-[var(--color-warning)]" />
            <p className="min-w-0 flex-1 text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
              Images from outside Omnipus are blocked.
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setImagesLoaded(true)
                onLoadImages?.()
              }}
              className="shrink-0"
            >
              Load images
            </Button>
          </div>
        )}
        <div className={cn('mx-auto w-full', isHtml ? '' : 'p-[var(--space-3)]')}>
          {isHtml ? (
            <MailHtmlFrame title={`Message body: ${message.subject}`} html={message.bodyHtml} className="min-h-[420px]" />
          ) : (
            <p className="whitespace-pre-wrap text-[length:var(--type-body-size)] leading-[var(--font-line-height-body)] text-[var(--color-secondary)]">
              {message.bodyText}
            </p>
          )}
        </div>
        {message.attachments.length > 0 && (
          <AttachmentsList attachments={message.attachments} />
        )}
      </div>
    </section>
  )
}

interface ParticipantRowProps {
  label: string
  value: string
}

function ParticipantRow({ label, value }: ParticipantRowProps) {
  return (
    <div className="flex min-w-0 gap-[var(--space-2)]">
      <dt className="w-10 shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {label}
      </dt>
      <dd className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
        {value}
      </dd>
    </div>
  )
}

interface AttachmentsListProps {
  attachments: MessageDetail['attachments']
}

/** D28: every attachment is listed with name, size, type and a download action. */
function AttachmentsList({ attachments }: AttachmentsListProps) {
  return (
    <div className="border-t border-[var(--color-border)] p-[var(--space-3)]">
      <p className="text-[length:var(--type-caption-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
        Attachments ({attachments.length})
      </p>
      <ul className="mt-[var(--space-2)] flex flex-col gap-[var(--space-1)]">
        {attachments.map((attachment) => (
          <li
            key={attachment.id}
            className="flex items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2)] py-[var(--space-1)]"
          >
            <File size={18} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
                {attachment.name}
              </span>
              <span className="block text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
                {formatMailBytes(attachment.sizeBytes)} · {attachment.contentType}
              </span>
            </span>
            <Button variant="outline" size="sm" className="shrink-0 gap-[var(--space-1)]">
              <DownloadSimple size={14} aria-hidden="true" />
              Download
            </Button>
          </li>
        ))}
      </ul>
    </div>
  )
}
