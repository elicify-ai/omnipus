// MailConnectionBanner — the explicit connection-failure / backoff state
// (D22, FR-018, D29/R2-8). Failures are never silent: the banner names the
// sanitized error class, shows the next automatic retry time ("Retrying at
// 14:32"), and offers a manual Retry that bypasses the backoff for that one
// request (R2-8). Prototype: the retry action only clears the banner.
import { ArrowClockwise, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'

export interface MailConnectionBannerProps {
  status: 'error' | 'backoff'
  /** Sanitized error class text — never a raw provider message (FR-018). */
  message?: string
  /** "Retrying at 14:32" — the backoff schedule is shown, not hidden. */
  nextRetryAt?: string
  onRetry?: () => void
}

export function MailConnectionBanner({
  status,
  message,
  nextRetryAt,
  onRetry,
}: MailConnectionBannerProps) {
  return (
    <div
      role="status"
      data-testid="mail-connection-banner"
      data-status={status}
      className="flex items-center gap-[var(--space-2)] border-b px-[var(--space-2-5)] py-[var(--space-2)] border-[var(--color-border)] bg-[var(--color-status-failed-background)] text-[var(--color-status-failed)]"
    >
      <Warning size={16} weight="fill" aria-hidden="true" className="shrink-0" />
      <div className="min-w-0 flex-1">
        <p className="text-[length:var(--type-body-compact-size)] font-[var(--font-weight-medium)]">
          {message ?? 'Mail server unreachable'}
        </p>
        {nextRetryAt && (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-text-tertiary)]">
            Retrying at {nextRetryAt}
          </p>
        )}
      </div>
      {onRetry && (
        <Button
          variant="outline"
          size="sm"
          onClick={onRetry}
          className="shrink-0 gap-[var(--space-1)]"
        >
          <ArrowClockwise size={14} aria-hidden="true" />
          Retry now
        </Button>
      )}
    </div>
  )
}

