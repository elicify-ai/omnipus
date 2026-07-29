// LibraryErrorBanner — the ONE error-presentation pattern for Library
// mutations that don't already have a persistent status surface of their own
// (Rename / Move / Copy / Upload). Deliberately mirrors the app-wide "form
// mutation failed" banner already used elsewhere (CreateAgentWizard.tsx,
// ExecutorSelector.tsx's runner-test-request-error, AgentProfile.tsx's
// locked-banner): a bordered, tinted block with an icon and the server's own
// message, `role="alert"` so it's announced immediately — NOT a second,
// invented error style. A failed action must leave the user with a message
// they can act on (the server's actual reason where available), never a
// silent revert.

import { WarningCircle, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export interface LibraryErrorBannerProps {
  message: string
  /** Present only when the banner should be user-dismissible (e.g. the
   * toolbar-level upload banner, which isn't tied to a dialog's own
   * open/close lifecycle). Dialog-hosted banners omit this — closing or
   * retrying the dialog is how those clear. */
  onDismiss?: () => void
  testId?: string
  className?: string
}

export function LibraryErrorBanner({ message, onDismiss, testId, className }: LibraryErrorBannerProps) {
  return (
    <div
      role="alert"
      data-testid={testId}
      className={cn(
        'flex items-start gap-2 rounded-md border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-3 py-2',
        className,
      )}
    >
      <WarningCircle size={14} className="mt-0.5 shrink-0 text-[var(--color-error)]" weight="fill" />
      <p className="flex-1 text-xs leading-snug text-[var(--color-error)]">{message}</p>
      {onDismiss && (
        <button
          type="button"
          tabIndex={0}
          onClick={onDismiss}
          aria-label="Dismiss error"
          className="shrink-0 text-[var(--color-error)]/70 transition-colors hover:text-[var(--color-error)]"
        >
          <X size={12} />
        </button>
      )}
    </div>
  )
}
