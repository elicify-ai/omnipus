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
import { IconButton } from '@/components/ui/icon-button'

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
        'flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-[var(--space-2-5)] py-[var(--space-2)]',
        className,
      )}
    >
      <WarningCircle size={14} className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-error)]" weight="fill" />
      <p className="flex-1 text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-error)]">{message}</p>
      {onDismiss && (
        <IconButton
          onClick={onDismiss}
          aria-label="Dismiss error"
          className="h-auto w-auto shrink-0 p-0 text-[var(--color-error)]/70 hover:bg-transparent hover:text-[var(--color-error)]"
        >
          <X size={12} />
        </IconButton>
      )}
    </div>
  )
}
