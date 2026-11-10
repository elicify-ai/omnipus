// MailComposeDialog — manual send from the Mail panel (US-5): To/Cc/Bcc
// (D26), Markdown-labeled body (A3), attachments (D28, MC-32 client caps),
// field-level validation on send. The signature is appended server-side and
// is not part of the composer. Reply prefill (US-5 AS-3). Contract-tested by
// MailCompose.validation.test.tsx.
import { useEffect, useRef, useState } from 'react'
import { PaperPlaneTilt } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { MailAttachmentsEditor } from './MailAttachmentsEditor'
import { MailMarkdownEditor } from './MailMarkdownEditor'
import { MailRecipientRow } from './MailRecipientRow'
import { MailSenderRow, MailSignaturePreview } from './MailSignaturePreview'
import {
  collectMailRecipients,
  isValidMailRecipient,
  type MailRecipientValue,
} from './MailRecipientInput'
import { setMailEditorDirty } from './mailUnsavedGuard'
import type { MailReplyContextResponse } from '@/lib/api/generated/openapi-types'

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
  mode: 'new' | 'reply' | 'reply_all'
  replyTo?: { from: string; subject: string; messageId: string }
  /**
   * The generated reply-context response (ADR-20261001 F5; W3 spec §2.4)
   * fetched by the panel for the message being replied to. When present it
   * prefills To/Cc (reply_all), the once-prefixed subject and the escaped,
   * editable quoted body — replacing the legacy from/subject-only prefill.
   * A stale context for a different message is discarded by the panel; the
   * dialog applies whatever it is last given.
   */
  replyContext?: MailReplyContextResponse
  senderName?: string
  senderAddress?: string
  signatureHtml?: string
  onSend(body: MailComposeBody): void
  onClose(): void
}

interface ComposeValues {
  to: MailRecipientValue
  cc: MailRecipientValue
  bcc: MailRecipientValue
  subject: string
  body: string
}

type ComposeErrors = Partial<Record<'to' | 'cc' | 'bcc' | 'body', string>>

const EMPTY_RECIPIENTS: MailRecipientValue = { recipients: [], draft: '' }

function recipientError(recipients: string[]): string | undefined {
  return recipients.some((recipient) => !isValidMailRecipient(recipient))
    ? 'Correct the invalid email address.'
    : undefined
}

export function MailComposeDialog({ open, mode, replyTo, replyContext, senderName, senderAddress, signatureHtml, onSend, onClose }: MailComposeDialogProps) {
  const [values, setValues] = useState<ComposeValues>({
    to: EMPTY_RECIPIENTS,
    cc: EMPTY_RECIPIENTS,
    bcc: EMPTY_RECIPIENTS,
    subject: '',
    body: '',
  })
  const [attachments, setAttachments] = useState<File[]>([])
  const [errors, setErrors] = useState<ComposeErrors>({})
  const [attachError, setAttachError] = useState<string | null>(null)
  const bodyRef = useRef('')

  // Fresh fields on every open; reply prefills To, the Re: subject and the
  // In-Reply-To header carried through to sendMailMessage. When the panel
  // supplied the generated reply-context response (F5), it prefills To/Cc
  // (reply_all), the subject and the escaped, editable quoted body — the
  // attribution inside the quote is server-built, "No date" included (F6).
  useEffect(() => {
    if (!open) return
    const reply = mode !== 'new' && replyTo
    const context = mode !== 'new' && replyContext
    setValues({
      // Legacy prefill keeps the reply address in the input until
      // Enter/comma/send so the longstanding accessible textbox contract
      // remains intact; structured context recipients seed the chip list.
      to: context
        ? { recipients: [...replyContext.to], draft: '' }
        : { recipients: [], draft: reply ? replyTo.from : '' },
      cc: context && mode === 'reply_all'
        ? { recipients: [...replyContext.cc], draft: '' }
        : EMPTY_RECIPIENTS,
      bcc: EMPTY_RECIPIENTS,
      subject: context
        ? replyContext.subject
        : reply
          ? `Re: ${replyTo.subject}`
          : '',
      body: context ? replyContext.body_markdown : '',
    })
    if (context) bodyRef.current = replyContext.body_markdown
    setAttachments([])
    setErrors({})
    setAttachError(null)
    if (!context) bodyRef.current = ''
  }, [open, mode, replyTo?.from, replyTo?.messageId, replyTo?.subject, replyContext])

  // CRIT-001: report unsaved compose text to the shared Mail leave guard
  // (mailUnsavedGuard.ts) — dirty only while open with a non-empty message,
  // clearing on close so a later, clean open never inherits a stale flag.
  useEffect(() => {
    setMailEditorDirty('compose', open && values.body.trim() !== '')
    return () => setMailEditorDirty('compose', false)
  }, [open, values.body])

  const setSubject = (event: React.ChangeEvent<HTMLInputElement>) =>
    setValues((previous) => ({ ...previous, subject: event.target.value }))

  const setRecipients = (key: 'to' | 'cc' | 'bcc') => (value: MailRecipientValue) => {
    setValues((previous) => ({ ...previous, [key]: value }))
    setErrors((previous) => ({ ...previous, [key]: undefined }))
  }

  const handleSend = () => {
    const to = collectMailRecipients(values.to)
    const cc = collectMailRecipients(values.cc)
    const bcc = collectMailRecipients(values.bcc)
    const nextErrors: ComposeErrors = {}
    if (to.length === 0) nextErrors.to = 'Add at least one recipient.'
    nextErrors.to ??= recipientError(to)
    nextErrors.cc = recipientError(cc)
    nextErrors.bcc = recipientError(bcc)
    if (bodyRef.current.trim() === '') nextErrors.body = 'Write a message before sending.'
    if (Object.values(nextErrors).some(Boolean) || attachError) {
      setErrors(nextErrors)
      return
    }
    onSend({
      to,
      cc,
      bcc,
      subject: values.subject,
      body_markdown: bodyRef.current,
      in_reply_to: mode === 'reply' && replyTo ? replyTo.messageId : null,
      attachments,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent className="mail-compose-dialog flex h-[calc(100dvh-var(--space-5))] max-h-[calc(100dvh-var(--space-5))] w-[calc(100%-var(--space-5))] max-w-5xl flex-col gap-[var(--space-2)] overflow-y-auto p-[var(--space-3)]">
        <DialogHeader className="shrink-0 pr-[var(--space-6)]">
          <DialogTitle>{mode === 'reply' ? 'Reply' : mode === 'reply_all' ? 'Reply all' : 'Compose message'}</DialogTitle>
          <DialogDescription>
            Sent as formatted HTML plus a plain-text copy, with the mailbox signature appended.
          </DialogDescription>
        </DialogHeader>
        <div className="shrink-0 divide-y divide-[var(--color-border)] border-y border-[var(--color-border)]">
          {senderName && senderAddress && <MailSenderRow name={senderName} address={senderAddress} />}
          <MailRecipientRow label="To" value={values.to} onChange={setRecipients('to')} error={errors.to} />
          <MailRecipientRow label="Cc" value={values.cc} onChange={setRecipients('cc')} error={errors.cc} />
          <MailRecipientRow label="Bcc" value={values.bcc} onChange={setRecipients('bcc')} error={errors.bcc} />
          <Field
            label="Subject"
            data-compose-header-row
            className="mail-compose-header-row grid grid-cols-[var(--space-8)_minmax(0,1fr)] items-center gap-x-[var(--space-2)] space-y-0 py-[var(--space-0-5)]"
          >
            <Input className="rounded-none border-0 bg-transparent px-0" value={values.subject} onChange={setSubject} placeholder="Subject" />
          </Field>
        </div>
        <Field
          label="Message"
          error={errors.body}
          required
          data-compose-message-region
          className="flex min-h-[calc(var(--space-8)+var(--space-4))] flex-1 flex-col gap-[var(--space-1)] space-y-0"
        >
          {(controlProps) => (
            <MailMarkdownEditor
              {...controlProps}
              markdown={values.body}
              onMarkdownChange={(body) => {
                bodyRef.current = body
                setValues((previous) => ({ ...previous, body }))
                setErrors((previous) => ({ ...previous, body: undefined }))
              }}
            />
          )}
        </Field>
        <MailSignaturePreview html={signatureHtml} />
        <MailAttachmentsEditor files={attachments} onFilesChange={setAttachments} error={attachError} onErrorChange={setAttachError} />
        <DialogFooter className="sticky bottom-0 z-10 shrink-0 bg-[var(--color-surface-1)] pt-[var(--space-2)]">
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
