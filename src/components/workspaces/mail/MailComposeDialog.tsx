// MailComposeDialog — manual send from the Mail panel (US-5): To/Cc/Bcc
// (D26), Markdown-labeled body (A3), attachments (D28, MC-32 client caps),
// field-level validation on send. The signature is appended server-side and
// is not part of the composer. Reply prefill (US-5 AS-3). Contract-tested by
// MailCompose.validation.test.tsx.
import { useEffect, useRef, useState } from 'react'
import { File, PaperPlaneTilt, Plus, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { formatMailBytes } from './mail-format'

/** The compose send payload, as handed to `onSend`. Cc/Bcc ride the wire
 * too (D26) — the oracle pins the required fields via objectContaining. */
export interface MailComposeBody {
  to: string[]
  cc: string[]
  bcc: string[]
  subject: string
  body_markdown: string
  in_reply_to: string | null
  attachments: File[]
}

export interface MailComposeDialogProps {
  open: boolean
  mode: 'new' | 'reply'
  replyTo?: { from: string; subject: string; messageId: string }
  onSend(body: MailComposeBody): void
  onClose(): void
}

const MAX_ATTACHMENTS = 10
const MAX_TOTAL_BYTES = 25 * 1024 * 1024

interface ComposeValues {
  to: string
  cc: string
  bcc: string
  subject: string
  body: string
}

function splitRecipients(raw: string): string[] {
  return raw
    .split(',')
    .map((entry) => entry.trim())
    .filter(Boolean)
}

export function MailComposeDialog({ open, mode, replyTo, onSend, onClose }: MailComposeDialogProps) {
  const [values, setValues] = useState<ComposeValues>({ to: '', cc: '', bcc: '', subject: '', body: '' })
  const [attachments, setAttachments] = useState<File[]>([])
  const [errors, setErrors] = useState<{ to?: string; body?: string }>({})
  const [attachError, setAttachError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Fresh fields on every open; reply prefills To, the Re: subject and the
  // In-Reply-To header carried through to sendMailMessage.
  useEffect(() => {
    if (!open) return
    const reply = mode === 'reply' && replyTo
    setValues({
      to: reply ? replyTo.from : '',
      cc: '',
      bcc: '',
      subject: reply ? `Re: ${replyTo.subject}` : '',
      body: '',
    })
    setAttachments([])
    setErrors({})
    setAttachError(null)
  }, [open, mode, replyTo])

  const set = (key: keyof ComposeValues) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setValues((prev) => ({ ...prev, [key]: e.target.value }))

  const handleFiles = (files: FileList | null) => {
    if (!files || files.length === 0) return
    const incoming = Array.from(files)
    if (attachments.length + incoming.length > MAX_ATTACHMENTS) {
      setAttachError(`Up to ${MAX_ATTACHMENTS} attachments are allowed.`)
      return
    }
    const total = [...attachments, ...incoming].reduce((sum, f) => sum + f.size, 0)
    if (total > MAX_TOTAL_BYTES) {
      setAttachError('Attachments are limited to 25 MB in total.')
      return
    }
    setAttachError(null)
    setAttachments((prev) => [...prev, ...incoming])
  }

  const removeAttachment = (index: number) => {
    setAttachments((prev) => prev.filter((_, i) => i !== index))
    setAttachError(null)
  }

  const handleSend = () => {
    const to = splitRecipients(values.to)
    const nextErrors: { to?: string; body?: string } = {}
    if (to.length === 0) nextErrors.to = 'Add at least one recipient.'
    if (values.body.trim() === '') nextErrors.body = 'Write a message before sending.'
    if (Object.keys(nextErrors).length > 0 || attachError) {
      setErrors(nextErrors)
      return
    }
    onSend({
      to,
      cc: splitRecipients(values.cc),
      bcc: splitRecipients(values.bcc),
      subject: values.subject,
      body_markdown: values.body,
      in_reply_to: mode === 'reply' && replyTo ? replyTo.messageId : null,
      attachments,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-[var(--space-3)] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{mode === 'reply' ? 'Reply' : 'Compose message'}</DialogTitle>
          <DialogDescription>
            Sent as formatted HTML plus a plain-text copy, with the mailbox signature appended.
          </DialogDescription>
        </DialogHeader>
        <Field label="To" error={errors.to} required>
          <Input value={values.to} onChange={set('to')} placeholder="name@example.com" />
        </Field>
        <Field label="Cc">
          <Input value={values.cc} onChange={set('cc')} placeholder="name@example.com" />
        </Field>
        <Field label="Bcc">
          <Input value={values.bcc} onChange={set('bcc')} placeholder="name@example.com" />
        </Field>
        <Field label="Subject">
          <Input value={values.subject} onChange={set('subject')} placeholder="Subject" />
        </Field>
        <Field label="Message (Markdown)" error={errors.body}>
          <Textarea rows={7} value={values.body} onChange={set('body')} placeholder="Write your message in Markdown" />
        </Field>
        <div>
          <p className="text-[length:var(--type-caption-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
            Attachments (up to 10 files, 25 MB total)
          </p>
          {attachments.length > 0 && (
            <ul className="mt-[var(--space-1)] flex flex-col gap-[var(--space-1)]">
              {attachments.map((attachment, index) => (
                <li
                  key={`${attachment.name}-${index}`}
                  className="flex items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2)] py-[var(--space-1)]"
                >
                  <File size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
                  <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
                    {attachment.name}
                  </span>
                  <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
                    {formatMailBytes(attachment.size)}
                  </span>
                  <IconButton aria-label={`Remove ${attachment.name}`} size="sm" variant="ghost" onClick={() => removeAttachment(index)}>
                    <X size={12} aria-hidden="true" />
                  </IconButton>
                </li>
              ))}
            </ul>
          )}
          {attachError && (
            <p role="alert" className="mt-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
              {attachError}
            </p>
          )}
          {/* The real input stays visually hidden; the button is its visible
              trigger. The label association above keeps the input reachable
              by name for assistive tech and tests. */}
          <input
            ref={fileInputRef}
            type="file"
            tabIndex={0}
            multiple
            aria-label="Attach files"
            className="sr-only"
            onChange={(e) => {
              handleFiles(e.target.files)
              e.target.value = ''
            }}
          />
          <Button variant="outline" size="sm" className="mt-[var(--space-2)] gap-[var(--space-1)]" onClick={() => fileInputRef.current?.click()}>
            <Plus size={14} aria-hidden="true" />
            Attach files
          </Button>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button className="gap-[var(--space-1)]" onClick={handleSend}>
            <PaperPlaneTilt size={14} aria-hidden="true" />
            Send
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
