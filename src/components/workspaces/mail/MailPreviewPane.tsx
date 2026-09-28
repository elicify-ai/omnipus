// MailPreviewPane — the draft preview/edit/send surface (D12: view + edit +
// send + discard; D23/D24). Panel Send IS the approval (D12); Discard deletes
// the draft (US-7). A foreign draft (created outside Omnipus) stays fully
// editable but carries the D24 formatting-loss statement. Contract-tested by
// MailDraft.actions.test.tsx.
import { useEffect, useRef, useState } from 'react'
import { File, PaperPlaneTilt, Trash } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { HistoricalMessageMarkdown } from '@/components/chat/historical-markdown'
import { MailMarkdownEditor } from './MailMarkdownEditor'
import { setMailEditorDirty } from './mailUnsavedGuard'

export type MailPreviewState = 'draft' | 'missing' | 'sent' | 'foreign'

export interface MailPreviewPaneProps {
  state: MailPreviewState
  subject: string
  bodyMarkdown: string
  to: string
  /** Shown in the 'sent' state (US-4 AS-4). */
  sentOn?: string
  onSave(next: { to: string; subject: string; bodyMarkdown: string }): void
  onSend(): void
  onDiscard(): void
}

export function MailPreviewPane({ state, subject, bodyMarkdown, to, sentOn, onSave, onSend, onDiscard }: MailPreviewPaneProps) {
  const [editing, setEditing] = useState(false)
  const [editTo, setEditTo] = useState(to)
  const [editSubject, setEditSubject] = useState(subject)
  const [editBody, setEditBody] = useState(bodyMarkdown)
  const editBodyRef = useRef(bodyMarkdown)

  // CRIT-001: report unsaved draft-editor text to the shared Mail leave
  // guard (mailUnsavedGuard.ts) — dirty only while editing with a change
  // from the saved draft, clearing on "Back to preview" / Save / unmount.
  useEffect(() => {
    const changed = editing && (editTo !== to || editSubject !== subject || editBody !== bodyMarkdown)
    setMailEditorDirty('draft', changed)
    return () => setMailEditorDirty('draft', false)
  }, [editing, editTo, editSubject, editBody, to, subject, bodyMarkdown])

  if (state === 'missing') {
    return (
      <section aria-label="Draft" className="flex min-w-0 flex-1 flex-col items-center justify-center bg-[var(--color-surface-0)] p-[var(--space-4)]">
        <File size={40} aria-hidden="true" className="text-[var(--color-border)]" />
        <p className="mt-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          This draft no longer exists.
        </p>
      </section>
    )
  }

  if (state === 'sent') {
    return (
      <section aria-label="Sent message" className="flex min-w-0 flex-1 flex-col bg-[var(--color-surface-0)]">
        <div className="flex items-center gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2-5)]">
          <PaperPlaneTilt size={16} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          <p className="min-w-0 text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
            Sent on {sentOn}
          </p>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-[var(--space-3)] py-[var(--space-2-5)]">
          <h3 className="text-[length:var(--type-section-title-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
            {subject}
          </h3>
          <p className="mt-[var(--space-1)] truncate text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
            To: {to}
          </p>
          <div className="mt-[var(--space-3)] text-[length:var(--type-body-size)] text-[var(--color-secondary)]">
            <HistoricalMessageMarkdown content={bodyMarkdown} />
          </div>
          <p className="mt-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
            Sent copies are read-only.
          </p>
        </div>
      </section>
    )
  }

  if (editing) {
    return (
      <section aria-label="Draft" className="flex min-w-0 flex-1 flex-col overflow-y-auto bg-[var(--color-surface-0)]">
        <div className="flex flex-col gap-[var(--space-3)] p-[var(--space-3)]">
          <Field label="To" required>
            <Input value={editTo} onChange={(e) => setEditTo(e.target.value)} placeholder="name@example.com" />
          </Field>
          <Field label="Subject">
            <Input value={editSubject} onChange={(e) => setEditSubject(e.target.value)} />
          </Field>
          <Field
            label="Message"
            description="Sent as formatted HTML plus a plain-text copy, with the mailbox signature appended."
          >
            {(controlProps) => (
              <MailMarkdownEditor
                {...controlProps}
                markdown={editBody}
                onMarkdownChange={(nextBody) => {
                  editBodyRef.current = nextBody
                  setEditBody(nextBody)
                }}
              />
            )}
          </Field>
          <div className="flex items-center gap-[var(--space-1)]">
            <Button variant="ghost" size="sm" onClick={() => setEditing(false)}>
              Back to preview
            </Button>
            <div className="min-w-0 flex-1" />
            <Button
              size="sm"
              onClick={() => {
                onSave({ to: editTo, subject: editSubject, bodyMarkdown: editBodyRef.current })
                setEditing(false)
              }}
            >
              Save
            </Button>
          </div>
        </div>
      </section>
    )
  }

  // 'draft' | 'foreign' — view mode. A foreign draft adds the D24 statement.
  return (
    <section aria-label="Draft" className="flex min-w-0 flex-1 flex-col bg-[var(--color-surface-0)]">
      {state === 'foreign' && (
        <div
          role="note"
          className="flex items-start gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-status-blocked-background)] px-[var(--space-3)] py-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
        >
          <File size={16} aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-blocked)]" />
          <p className="min-w-0">
            This draft was written outside Omnipus, so its formatting (styling, images, table layout) may be lost.
            Only the text content, headings, lists, links and attachments are kept.
          </p>
        </div>
      )}
      <div className="flex shrink-0 items-start gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2-5)]">
        <div className="min-w-0 flex-1">
          <h3 className="text-[length:var(--type-section-title-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
            {subject}
          </h3>
          <p className="mt-[var(--space-1)] truncate text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
            To: {to}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-[var(--space-1)]">
          <Button variant="secondary" size="sm" onClick={() => {
            setEditTo(to)
            setEditSubject(subject)
            setEditBody(bodyMarkdown)
            editBodyRef.current = bodyMarkdown
            setEditing(true)
          }}>
            Edit
          </Button>
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
      <div className="min-h-0 flex-1 overflow-y-auto px-[var(--space-3)] py-[var(--space-2-5)]">
        <div className="text-[length:var(--type-body-size)] text-[var(--color-secondary)]">
          <HistoricalMessageMarkdown content={bodyMarkdown} />
        </div>
      </div>
    </section>
  )
}
