// Shared empty-state / waiting-overlay body: honest error + Retry, or a spinner.
// The two call sites in BrowserLiveView used to duplicate this markup.

import { SpinnerGap, WarningCircle } from '@phosphor-icons/react'

const RETRY_BTN =
  'mt-1 rounded-full border border-[var(--color-border)] px-3 py-1 text-xs font-medium text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]'

export function BrowserLiveRetryStatus({
  displayError,
  idleMessage,
  onRetry,
  retryTestId,
  retryButtonClassName,
}: {
  displayError: string | null
  idleMessage: string
  onRetry: () => void
  retryTestId: string
  retryButtonClassName?: string
}) {
  if (displayError) {
    return (
      <>
        <WarningCircle size={22} className="text-[var(--color-error)]" />
        <p className="max-w-full [overflow-wrap:anywhere] text-[var(--color-error)]">{displayError}</p>
        <button
          type="button"
          tabIndex={0}
          onClick={onRetry}
          data-testid={retryTestId}
          className={retryButtonClassName ?? RETRY_BTN}
        >
          Retry
        </button>
      </>
    )
  }
  return (
    <>
      <SpinnerGap size={20} className="animate-spin" />
      <p>{idleMessage}</p>
    </>
  )
}
