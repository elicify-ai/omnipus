import { X } from '@phosphor-icons/react'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'

export interface MailRecipientValue {
  recipients: string[]
  draft: string
}

export interface MailRecipientInputProps {
  value: MailRecipientValue
  onChange(value: MailRecipientValue): void
  id?: string
  required?: boolean
  'aria-label': string
  'aria-describedby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
}

const EMAIL_ADDRESS = /^[^\s<>,;@]+@[^\s<>,;@]+\.[^\s<>,;@]+$/

export function isValidMailRecipient(value: string): boolean {
  return EMAIL_ADDRESS.test(value)
}

function normalizeRecipient(value: string): string {
  const trimmed = value.trim()
  const bracketedAddress = /<([^<>]+)>$/.exec(trimmed)
  return (bracketedAddress?.[1] ?? trimmed).trim()
}

export function splitMailRecipients(value: string): string[] {
  const parts: string[] = []
  let current = ''
  let quoted = false
  let escaped = false
  let angleDepth = 0

  for (const character of value) {
    if (escaped) {
      current += character
      escaped = false
      continue
    }
    if (character === '\\' && quoted) {
      current += character
      escaped = true
      continue
    }
    if (character === '"') quoted = !quoted
    if (!quoted && character === '<') angleDepth += 1
    if (!quoted && character === '>' && angleDepth > 0) angleDepth -= 1
    if (!quoted && angleDepth === 0 && (character === ',' || character === ';' || character === '\n')) {
      parts.push(current)
      current = ''
      continue
    }
    current += character
  }
  parts.push(current)

  return parts
    .map(normalizeRecipient)
    .filter(Boolean)
}

function appendRecipients(current: string[], incoming: string[]): string[] {
  const seen = new Set(current.map((recipient) => recipient.toLocaleLowerCase()))
  const next = [...current]
  for (const recipient of incoming) {
    const key = recipient.toLocaleLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    next.push(recipient)
  }
  return next
}

export function collectMailRecipients(value: MailRecipientValue): string[] {
  return appendRecipients(value.recipients, splitMailRecipients(value.draft))
}

export function MailRecipientInput({
  value,
  onChange,
  id,
  required,
  'aria-label': ariaLabel,
  'aria-describedby': ariaDescribedBy,
  'aria-invalid': ariaInvalid,
}: MailRecipientInputProps) {
  const commitDraft = () => {
    const recipients = splitMailRecipients(value.draft)
    if (recipients.length === 0) return
    onChange({ recipients: appendRecipients(value.recipients, recipients), draft: '' })
  }

  const removeRecipient = (index: number) => {
    onChange({ ...value, recipients: value.recipients.filter((_, candidate) => candidate !== index) })
  }

  return (
    <div
      className="mail-recipient-input"
      data-invalid={ariaInvalid ? 'true' : undefined}
    >
      {value.recipients.map((recipient, index) => {
        const valid = isValidMailRecipient(recipient)
        return (
          <span
            key={`${index}:${recipient}`}
            data-testid="recipient-chip"
            data-invalid={valid ? undefined : 'true'}
            className="mail-recipient-chip"
          >
            <span className="mail-recipient-address">{recipient}</span>
            {!valid && <span className="mail-recipient-invalid">Invalid</span>}
            <IconButton
              size="sm"
              variant="ghost"
              aria-label={`Remove recipient ${recipient}`}
              className="mail-recipient-remove"
              onClick={() => removeRecipient(index)}
            >
              <X size={12} aria-hidden="true" />
            </IconButton>
          </span>
        )
      })}
      <Input
        id={id}
        required={required}
        aria-label={ariaLabel}
        aria-describedby={ariaDescribedBy}
        aria-invalid={ariaInvalid}
        data-no-focus-ring
        value={value.draft}
        onChange={(event) => onChange({ ...value, draft: event.target.value })}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ',') {
            event.preventDefault()
            commitDraft()
            return
          }
          if (event.key === 'Backspace' && value.draft === '' && value.recipients.length > 0) {
            event.preventDefault()
            removeRecipient(value.recipients.length - 1)
          }
        }}
        onPaste={(event) => {
          const pasted = event.clipboardData.getData('text/plain')
          const recipients = splitMailRecipients(pasted)
          if (recipients.length < 2) return
          event.preventDefault()
          const typed = splitMailRecipients(value.draft)
          onChange({
            recipients: appendRecipients(value.recipients, [...typed, ...recipients]),
            draft: '',
          })
        }}
        placeholder={value.recipients.length === 0 ? 'name@example.com' : 'Add another'}
        className="mail-recipient-draft"
      />
    </div>
  )
}
