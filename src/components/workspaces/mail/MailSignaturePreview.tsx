import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { mintMailSignaturePreviewToken } from '@/lib/api/mail'
import { MailHtmlFrame } from './MailHtmlFrame'

/** Read-only identity for the mailbox selected in the Mail panel. */
export function MailSenderRow({ name, address, padded = false }: { name: string; address: string; padded?: boolean }) {
  return (
    <div
      data-compose-header-row
      className={padded
        ? 'mail-compose-header-row grid grid-cols-[var(--space-8)_minmax(0,1fr)] items-center gap-x-[var(--space-2)] px-[var(--space-3)] py-[var(--space-0-5)]'
        : 'mail-compose-header-row grid grid-cols-[var(--space-8)_minmax(0,1fr)] items-center gap-x-[var(--space-2)] py-[var(--space-0-5)]'}
    >
      <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">From</span>
      <span className="min-w-0 truncate text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
        {name} · {address}
      </span>
    </div>
  )
}

/** The signature is sanitized by the gateway and shown only in its sandboxed, token-scoped frame. */
export function MailSignaturePreview({ html, placement = 'compose' }: { html?: string; placement?: 'compose' | 'editor' | 'preview' }) {
  const [tokenUrl, setTokenUrl] = useState<string | null>(null)
  const [error, setError] = useState(false)
  const [retry, setRetry] = useState(0)

  useEffect(() => {
    if (!html?.trim()) return
    let cancelled = false
    let refreshTimer: ReturnType<typeof setTimeout> | undefined
    setTokenUrl(null)
    setError(false)
    mintMailSignaturePreviewToken({ signature_html: html })
      .then(({ token, expires_in_seconds }) => {
        if (cancelled) return
        setTokenUrl(`/mail-preview/html/${token}`)
        setError(false)
        // The signature token expires while a long draft is open. Refresh
        // before expiry so the review surface remains current until Send.
        refreshTimer = setTimeout(() => setRetry((previous) => previous + 1), expires_in_seconds * 500)
      })
      .catch(() => {
        if (cancelled) return
        setTokenUrl(null)
        setError(true)
      })
    return () => {
      cancelled = true
      if (refreshTimer !== undefined) clearTimeout(refreshTimer)
    }
  }, [html, retry])

  if (!html?.trim()) return null

  return (
    <div className={placement === 'editor'
      ? 'shrink-0 px-[var(--space-3)] py-[var(--space-2)]'
      : placement === 'preview'
        ? 'shrink-0 mt-[var(--space-3)] px-0'
        : 'shrink-0 px-0'}>
      <p className="mb-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Signature</p>
      {error && (
        <div role="alert" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">
          Signature preview unavailable. Check it before sending.
          <Button variant="link" size="sm" onClick={() => setRetry((previous) => previous + 1)}>Retry</Button>
        </div>
      )}
      {tokenUrl !== null && (
        <div className="h-[calc(var(--space-8)+var(--space-7))] overflow-hidden rounded-md border border-[var(--color-border)]">
          <MailHtmlFrame
            tokenUrl={tokenUrl}
            title="Mailbox signature preview"
            showLoadImages={false}
            onLoadImages={() => undefined}
          />
        </div>
      )}
    </div>
  )
}
