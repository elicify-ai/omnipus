// MailDraftPanel — the draft approval surface opened from a chat link
// (D8/D12: View + Edit + Send + Discard; D23: To/subject/body editable;
// D24: foreign drafts fully editable with the formatting-loss statement).
// Panel Send IS the approval (D12); Discard deletes the draft (US-7).
// PROTOTYPE (D35): Edit, Discard and Send stay local (handlers when the
// demo supplies them). No endpoint is called.
import { useState } from 'react'
import {
  ChatCircleDots,
  File,
  PaperPlaneTilt,
  Trash,
  X,
} from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
import { MailHtmlFrame } from './MailHtmlFrame'
import { formatMailBytes } from './sampleMail'
import type { DraftSample } from './sampleMail'

/** The draft panel's field values, as passed to `onSend`/`onDiscard`. */
export interface DraftValues {
  to: string[]
  cc: string[]
  bcc: string[]
  subject: string
  body: string
}

export interface MailDraftPanelProps {
  draft: DraftSample
  /** Start in edit mode (the foreign-draft edit story). */
  initialMode?: 'view' | 'edit'
  onClose?: () => void
  /** Interactive demo/feature hook: Discard removes the draft. */
  onDiscard?: (values: DraftValues) => void
  /** Interactive demo/feature hook: Send approves and sends the draft. */
  onSend?: (values: DraftValues) => void
  className?: string
}

export function MailDraftPanel({ draft, initialMode = 'view', onClose, onDiscard, onSend, className }: MailDraftPanelProps) {
  const [mode, setMode] = useState<'view' | 'edit'>(initialMode)
  const [to, setTo] = useState(draft.to.join(', '))
  const [cc, setCc] = useState(draft.cc.join(', '))
  const [bcc, setBcc] = useState(draft.bcc.join(', '))
  const [subject, setSubject] = useState(draft.subject)
  const [body, setBody] = useState(draft.markdownBody)
  const splitList = (value: string): string[] =>
    value.split(',').map((entry) => entry.trim()).filter(Boolean)
  const values: DraftValues = { to: splitList(to), cc: splitList(cc), bcc: splitList(bcc), subject, body }
  return (
    <aside
      data-testid="mail-draft-panel"
      aria-label="Draft panel"
      className={cn(
        'flex h-full w-full flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]',
        className,
      )}
    >
      <header className="flex h-chrome-header shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)]">
        <span className="shrink-0 text-[length:var(--type-body-compact-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
          Draft
        </span>
        <Badge variant={draft.origin === 'agent' ? 'default' : 'outline'} className="shrink-0">
          {draft.origin === 'agent' ? 'Created by agent' : 'From your mail program'}
        </Badge>
        <div className="min-w-0 flex-1" />
        {onClose && (
          <IconButton aria-label="Close draft panel" size="sm" onClick={onClose}>
            <X size={14} aria-hidden="true" />
          </IconButton>
        )}
      </header>
      {draft.chatContext && (
        <p className="flex shrink-0 items-center gap-[var(--space-1)] border-b border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
          <ChatCircleDots size={12} aria-hidden="true" />
          {draft.chatContext}
        </p>
      )}
      {mode === 'view' ? (
        <DraftView
          draft={draft}
          onEdit={() => setMode('edit')}
          onDiscard={() => onDiscard?.(values)}
          onSend={() => onSend?.(values)}
        />
      ) : (
        <DraftEdit
          draft={draft}
          to={to} cc={cc} bcc={bcc}
          subject={subject} body={body}
          onTo={setTo} onCc={setCc} onBcc={setBcc}
          onSubject={setSubject} onBody={setBody}
          onCancel={() => setMode('view')}
          onDiscard={() => onDiscard?.(values)}
          onSend={() => onSend?.(values)}
        />
      )}
    </aside>
  )
}

interface DraftViewProps {
  draft: DraftSample
  onEdit: () => void
  onDiscard: () => void
  onSend: () => void
}

function DraftView({ draft, onEdit, onDiscard, onSend }: DraftViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="shrink-0 border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2-5)]">
        <h3 className="text-[length:var(--type-section-title-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
          {draft.subject}
        </h3>
        <dl className="mt-[var(--space-1)] flex flex-col gap-[var(--space-0-5)]">
          <ParticipantLine label="To" value={draft.to.join(', ')} />
          {draft.cc.length > 0 && <ParticipantLine label="Cc" value={draft.cc.join(', ')} />}
          {draft.bcc.length > 0 && <ParticipantLine label="Bcc" value={draft.bcc.join(', ')} />}
        </dl>
        <div className="mt-[var(--space-2)] flex items-center gap-[var(--space-1)]">
          <Button variant="secondary" size="sm" onClick={onEdit}>
            Edit
          </Button>
          <div className="min-w-0 flex-1" />
          <Button variant="ghost" size="sm" onClick={onDiscard} className="gap-[var(--space-1)] text-[var(--color-error)]">
            <Trash size={14} aria-hidden="true" />
            Discard
          </Button>
          <Button size="sm" onClick={onSend} className="gap-[var(--space-1)]">
            <PaperPlaneTilt size={14} aria-hidden="true" />
            Send
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <MailHtmlFrame title="Draft body preview" html={draft.renderedHtml} className="min-h-[380px]" />
      </div>
      {draft.attachments.length > 0 && (
        <ul className="shrink-0 border-t border-[var(--color-border)] p-[var(--space-2)] flex flex-col gap-[var(--space-1)]">
          {draft.attachments.map((attachment) => (
            <li key={attachment.id} className="flex items-center gap-[var(--space-2)]">
              <File size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
              <span className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
                {attachment.name}
              </span>
              <span className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
                {formatMailBytes(attachment.sizeBytes)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function ParticipantLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 gap-[var(--space-2)]">
      <dt className="w-10 shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">{label}</dt>
      <dd className="min-w-0 flex-1 truncate text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">{value}</dd>
    </div>
  )
}

interface DraftEditProps {
  draft: DraftSample
  to: string
  cc: string
  bcc: string
  subject: string
  body: string
  onTo: (v: string) => void
  onCc: (v: string) => void
  onBcc: (v: string) => void
  onSubject: (v: string) => void
  onBody: (v: string) => void
  onCancel: () => void
  onDiscard: () => void
  onSend: () => void
}

/**
 * D23/D24: everything is editable — To/CC/BCC, subject and the Markdown
 * body. A foreign draft shows the formatting-loss statement above the
 * editor, and its carried-over attachments are listed with explicit removal
 * (nothing disappears silently, FR-035/US-8).
 */
function DraftEdit(props: DraftEditProps) {
  const { draft, to, cc, bcc, subject, body, onTo, onCc, onBcc, onSubject, onBody, onCancel, onDiscard, onSend } = props
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      <div className="flex flex-col gap-[var(--space-3)] p-[var(--space-3)]">
        {draft.origin === 'external' && draft.formatLossNotice && (
          <div
            role="note"
            className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-status-blocked-background)] p-[var(--space-2-5)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
          >
            <File size={16} aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-blocked)]" />
            {draft.formatLossNotice}
          </div>
        )}
        <Field label="To">
          <Input value={to} onChange={(e) => onTo(e.target.value)} placeholder="name@example.com" />
        </Field>
        <Field label="Cc">
          <Input value={cc} onChange={(e) => onCc(e.target.value)} placeholder="name@example.com" />
        </Field>
        <Field label="Bcc">
          <Input value={bcc} onChange={(e) => onBcc(e.target.value)} placeholder="name@example.com" />
        </Field>
        <Field label="Subject">
          <Input value={subject} onChange={(e) => onSubject(e.target.value)} />
        </Field>
        <Field
          label="Message (Markdown)"
          description="Sent as formatted HTML plus a plain-text copy, with the mailbox signature appended."
        >
          <Textarea rows={9} value={body} onChange={(e) => onBody(e.target.value)} />
        </Field>
        {draft.attachments.length > 0 && (
          <div>
            <p className="text-[length:var(--type-caption-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
              Attached (carried over)
            </p>
            <ul className="mt-[var(--space-1)] flex flex-col gap-[var(--space-1)]">
              {draft.attachments.map((attachment) => (
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
          </div>
        )}
        <div className="flex items-center gap-[var(--space-1)]">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Back to preview
          </Button>
          <div className="min-w-0 flex-1" />
          <Button variant="ghost" size="sm" onClick={onDiscard} className="gap-[var(--space-1)] text-[var(--color-error)]">
            <Trash size={14} aria-hidden="true" />
            Discard
          </Button>
          <Button size="sm" onClick={onSend} className="gap-[var(--space-1)]">
            <PaperPlaneTilt size={14} aria-hidden="true" />
            Send
          </Button>
        </div>
      </div>
    </div>
  )
}
