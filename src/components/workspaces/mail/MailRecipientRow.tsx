import { Field } from '@/components/ui/field'
import { MailRecipientInput, type MailRecipientValue } from './MailRecipientInput'

/** Mail-only recipient row shared by Compose and draft editing; the reusable
 * catalogued Field retains its label, error and accessible control association. */
export function MailRecipientRow({ label, value, onChange, error, padded = false }: {
  label: 'To' | 'Cc' | 'Bcc'
  value: MailRecipientValue
  onChange(value: MailRecipientValue): void
  error?: string
  padded?: boolean
}) {
  return (
    <Field
      label={label}
      error={error}
      required={label === 'To'}
      data-compose-header-row
      className={padded
        ? 'mail-compose-header-row grid grid-cols-[var(--space-8)_minmax(0,1fr)] items-center gap-x-[var(--space-2)] space-y-0 px-[var(--space-3)] py-[var(--space-0-5)] [&>[role=alert]]:col-start-2 [&>[role=alert]]:pb-[var(--space-1)]'
        : 'mail-compose-header-row grid grid-cols-[var(--space-8)_minmax(0,1fr)] items-center gap-x-[var(--space-2)] space-y-0 py-[var(--space-0-5)] [&>[role=alert]]:col-start-2 [&>[role=alert]]:pb-[var(--space-1)]'}
    >
      {(controlProps) => (
        <MailRecipientInput
          {...controlProps}
          aria-label={label}
          value={value}
          onChange={onChange}
        />
      )}
    </Field>
  )
}
