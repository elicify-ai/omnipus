// ImportPreviewStep — what the bundle will add (spec "Import · Preview"):
// the agent with the name it will get (and why it was renamed), skipped
// items, warnings, secrets to enter again, and until when the upload can be
// imported. Cancel discards the upload (the wizard sends the one cancel and
// reads its answer); Continue goes to Confirm.
import type { PackageImportPreview } from '@/lib/api/packages'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { BusyNotice, StepHeading } from './shared'

const sectionTitle = 'text-[length:var(--type-label-size)] font-semibold text-[var(--color-secondary)]'
const bodyText = 'text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]'

/** The expiry in the person's local date and time (never the wire string). */
export function localTime(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString()
}

/** Messages and items the server sent, each in its own text (Warnings). */
export function MessageList({ rows }: { rows: Array<{ code: string; message: string; items?: string[] }> }) {
  return (
    <ul className="flex list-disc flex-col gap-[var(--space-2)] pl-[var(--space-4)]">
      {rows.map((row, index) => (
        <li key={`${row.code}-${index}`} className={bodyText}>
          <span>{row.message}</span>
          {row.items && row.items.length > 0 && (
            <ul className="mt-[var(--space-1)] flex flex-wrap gap-[var(--space-2)]">
              {row.items.map((item) => (
                <li key={item} className="text-[var(--color-muted)]">
                  {item}
                </li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  )
}

/** Secret names to enter again after import (names only, never values). */
export function SecretList({ secrets }: { secrets: Array<{ name: string; used_by: string }> }) {
  if (secrets.length === 0) return null
  return (
    <div className="flex flex-col gap-[var(--space-2)]">
      <h3 className={sectionTitle}>Enter again after import</h3>
      <ul className="flex list-disc flex-col gap-[var(--space-1)] pl-[var(--space-4)]">
        {secrets.map((secret) => (
          <li key={`${secret.name}-${secret.used_by}`} className={bodyText}>
            <code className="font-mono text-[length:var(--type-code-size)]">{secret.name}</code>
            <span className="text-[var(--color-muted)]">{` · ${secret.used_by}`}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

interface ImportPreviewStepProps {
  preview: PackageImportPreview
  /** The text after a cancel the session refused ("Select Cancel again."). */
  cancelError?: string | null
  cancelling?: boolean
  onCancel: () => void
  onContinue: () => void
}

export function ImportPreviewStep({ preview, cancelError, cancelling, onCancel, onContinue }: ImportPreviewStepProps) {
  return (
    <section className="flex flex-col gap-[var(--space-4)]">
      <StepHeading>Preview</StepHeading>

      <div className="flex flex-col gap-[var(--space-2)]">
        <h3 className={sectionTitle}>Will be added</h3>
        <ul className="flex flex-col gap-[var(--space-2)]">
          {preview.will_add.map((item) => (
            <li key={item.source_id} className={bodyText}>
              {item.renamed ? (
                <span className="flex flex-col gap-[var(--space-1)]">
                  <span className="flex flex-wrap items-center gap-[var(--space-2)]">
                    <span>{item.source_name}</span>
                    <span aria-hidden="true">→</span>
                    <span className="font-semibold">{item.proposed_name}</span>
                  </span>
                  <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                    renamed: an agent with this name exists; it is not changed
                  </span>
                </span>
              ) : (
                <span className="font-semibold">{item.proposed_name}</span>
              )}
            </li>
          ))}
        </ul>
      </div>

      {preview.skipped_elements.length > 0 && (
        <div className="flex flex-col gap-[var(--space-2)]">
          <h3 className={sectionTitle}>Skipped</h3>
          <ul className="flex list-disc flex-col gap-[var(--space-1)] pl-[var(--space-4)]">
            {preview.skipped_elements.map((item) => (
              <li key={`${item.type}-${item.source_id}`} className={bodyText}>
                {item.name}
              </li>
            ))}
          </ul>
        </div>
      )}

      {preview.warnings.length > 0 && (
        <div className="flex flex-col gap-[var(--space-2)]">
          <h3 className={sectionTitle}>Warnings</h3>
          <MessageList rows={preview.warnings} />
        </div>
      )}

      <SecretList secrets={preview.needs_secrets} />

      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {`Can be imported until ${localTime(preview.expires_at)}`}
      </p>

      {cancelError && (
        <Callout variant="warning">
          <CalloutDescription>{cancelError}</CalloutDescription>
        </Callout>
      )}
      {cancelling && <BusyNotice>Cancelling the upload…</BusyNotice>}
      <div className="flex flex-wrap gap-[var(--space-2)]">
        <Button variant="outline" disabled={cancelling} onClick={onCancel}>
          Cancel
        </Button>
        <Button disabled={cancelling} onClick={onContinue}>
          Continue
        </Button>
      </div>
    </section>
  )
}
