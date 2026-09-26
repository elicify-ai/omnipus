// MailSignatureEditor — the per-mailbox HTML signature section (US-1, D2)
// with the live preview (FR-002) and the MC-1 character bound shown to the
// operator. The stored value is the SANITIZED form (FR-003); agents never
// write the signature (D3). PROTOTYPE (D35): presentational, no save wired.
// The real build hosts this section inside EmailMailboxPanel (spec §16).
import { useMemo, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import { MailHtmlFrame } from './MailHtmlFrame'
import { cn } from '@/lib/utils'

export interface MailSignatureEditorProps {
  initialHtml: string
  maxChars: number
  className?: string
}

export function MailSignatureEditor({ initialHtml, maxChars, className }: MailSignatureEditorProps) {
  const [html, setHtml] = useState(initialHtml)
  const charCount = html.length
  const over = charCount > maxChars
  // Live preview (FR-002): rendered in the same sandboxed, script-free
  // frame posture as the reading pane.
  const previewHtml = useMemo(
    () => `<body style="margin:0">${html}</body>`,
    [html],
  )
  return (
    <section
      aria-label="Signature"
      data-testid="mail-signature-editor"
      className={cn(
        'flex w-full flex-col gap-[var(--space-3)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-0)] p-[var(--space-3)]',
        className,
      )}
    >
      <div>
        <h3 className="text-[length:var(--type-section-title-size)] font-[var(--font-weight-medium)] text-[var(--color-secondary)]">
          Signature
        </h3>
        <p className="mt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
          Appended to every message sent from this mailbox — agent sends, manual sends and approved drafts.
          Stored sanitized: scripts and event handlers are stripped.
        </p>
      </div>
      <Field
        label="HTML"
        description="Inline styles, tables and https images are kept. Script, forms and iframes are removed on save."
        error={over ? `Signature is over the ${maxChars.toLocaleString()} character limit.` : undefined}
      >
        <Textarea rows={7} value={html} onChange={(e) => setHtml(e.target.value)} className="font-mono" />
      </Field>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
        {charCount.toLocaleString()} / {maxChars.toLocaleString()} characters
      </p>
      <div className="flex flex-col gap-[var(--space-1)]">
        <p className="text-[length:var(--type-caption-size)] font-[var(--font-weight-medium)] text-[var(--color-muted)]">
          Preview
        </p>
        <div className="h-[120px] overflow-hidden rounded-md border border-[var(--color-border)]">
          <MailHtmlFrame title="Signature preview" html={previewHtml} />
        </div>
      </div>
      <div className="flex items-center gap-[var(--space-1)]">
        <div className="min-w-0 flex-1" />
        <Button variant="ghost" size="sm">Remove signature</Button>
        <Button size="sm">Save signature</Button>
      </div>
    </section>
  )
}
