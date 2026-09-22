// Shared empty-state / waiting-overlay body: honest error + Retry, or a spinner.
// The two call sites in BrowserLiveView used to duplicate this markup.

import { SpinnerGap, WarningCircle } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const RETRY_BTN =
  'h-auto mt-[var(--space-1)] rounded-full border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'

export function BrowserLiveRetryStatus({
  displayError,
  idleMessage,
  onRetry,
  retryTestId,
  retryButtonPointerEventsAuto,
}: {
  displayError: string | null
  idleMessage: string
  onRetry: () => void
  retryTestId: string
  // The overlay this renders inside (the "waiting for first frame" case) is
  // `pointer-events-none` so it doesn't intercept clicks meant for the video
  // underneath it — this opts the Retry button itself back in so it stays
  // clickable. See BrowserLiveView.tsx's waiting-overlay call site.
  retryButtonPointerEventsAuto?: boolean
}) {
  if (displayError) {
    return (
      <>
        <WarningCircle size={22} className="text-[var(--color-error)]" />
        <p className="max-w-full [overflow-wrap:anywhere] text-[var(--color-error)]">{displayError}</p>
        <Button
          variant="outline"
          onClick={onRetry}
          data-testid={retryTestId}
          className={cn(RETRY_BTN, retryButtonPointerEventsAuto && 'pointer-events-auto')}
        >
          Retry
        </Button>
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
