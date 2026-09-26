// MailComposeDialog — manual send from the Mail panel (D9, US-5): To/CC/BCC
// (D26), Markdown-labeled body (A3), attachments (D28, MC-32 caps shown),
// field-level validation. The signature is appended server-side; it is not
// part of the composer. PROTOTYPE (D35): no send is wired.
import {
  File,
  PaperPlaneTilt,
  Plus,
  X,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { formatMailBytes } from './sampleMail'
import type { MailboxSample } from './sampleMail'

export interface MailComposeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  mailbox?: MailboxSample
  /** Presentational validation errors (the real build validates on send). */
  errors?: { to?: string; body?: string }
}

export function MailComposeDialog({ open, onOpenChange, mailbox, errors }: MailComposeDialogProps) {
  const attachments = [
    { id: 'c1', name: 'pricing-update.pdf', sizeBytes: 262_144 },
  ]
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-[var(--space-3)] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Compose message</DialogTitle>
          <DialogDescription>
            {mailbox
              ? `Sending from ${mailbox.label} (${mailbox.address}). The mailbox signature is appended automatically.`
              : 'Choose a mailbox to send from.'}
          </DialogDescription>
        </DialogHeader>
        <Field label="To" error={errors?.to} required>
          <Input placeholder="name@example.com" />
        </Field>
        <Field label="Cc">
          <Input placeholder="name@example.com" />
        </Field>
        <Field label="Bcc">
          <Input placeholder="name@example.com" />
        </Field>
        <Field label="Subject">
          <Input placeholder="Subject" />
        </Field>
        <Field
          label="Message (Markdown)"
          description="Sent as formatted HTML plus a plain-text copy, with the mailbox signature appended."
        >
          <Textarea rows={7} placeholder="Write your message in Markdown" />
        </Field>
        <div>
          <p className="text-[length:var(--type-caption-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
            Attachments (up to 10 files, 25 MiB total)
          </p>
          <ul className="mt-[var(--space-1)] flex flex-col gap-[var(--space-1)]">
            {attachments.map((attachment) => (
              <li
                key={attachment.id}
                className="flex items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2)] py-[var(--space-1)]"
              >
                <File size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
                <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
                  {attachment.name}
                </span>
                <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
                  {formatMailBytes(attachment.sizeBytes)}
                </span>
                <IconButton aria-label={`Remove ${attachment.name}`} size="sm" variant="ghost">
                  <X size={12} aria-hidden="true" />
                </IconButton>
              </li>
            ))}
          </ul>
          <Button variant="outline" size="sm" className="mt-[var(--space-2)] gap-[var(--space-1)]">
            <Plus size={14} aria-hidden="true" />
            Attach files
          </Button>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button className="gap-[var(--space-1)]">
            <PaperPlaneTilt size={14} aria-hidden="true" />
            Send
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
