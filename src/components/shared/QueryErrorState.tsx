// QueryErrorState — the ONE shared blocking error state for a failed query
// with no usable cached data (D9).
//
// Before this component existed, every screen hand-rolled its own "isError"
// branch — some had a Retry action (Graph, Team), some didn't (Board/List,
// Media), and Calendar had no blocking UI at all (a non-blocking toast, with
// the grid quietly rendering as if empty). This centralizes the
// Graph/Team visual pattern so a failed query renders IDENTICALLY everywhere
// it appears: same icon, same copy style, same optional Retry affordance.
//
// D9's second half — the deterministic-race fix: every one of these screens
// is ALSO subscribed to the global forced-logout handler (queryClient.ts's
// 401 subscriber). On a 401, both fire from the SAME event: the query's
// isError flips true (this component would paint) AND forceLogout() redirects
// to /login. Historically that was a race — whichever happened to flush
// first determined whether the user saw a flash of "Retry" before the bounce.
// isForceLoggingOut() (authLogout.ts) is set SYNCHRONOUSLY the instant
// forceLogout() runs, and the global 401 subscriber (registered at
// queryClient.ts module-init, before any screen mounts) fires before a
// screen's own hook-driven re-render is flushed — so by the time THIS
// component's render body executes, the flag is already accurate. Rendering
// null in that window means no screen ever paints a stale actionable error
// for the redirect to interrupt.
import { WarningCircle } from '@phosphor-icons/react'
import { isForceLoggingOut } from '@/lib/authLogout'

export interface QueryErrorStateProps {
  /** User-facing explanation of what failed. Keep it screen-specific — this
   *  component only owns the chrome, not the copy. */
  message: string
  /** Omit for a failure with no meaningful retry action. */
  onRetry?: () => void
  /**
   * 'absolute' fills the nearest positioned ancestor (matches the Graph/Team
   * full-bleed tab pattern — use when this IS the entire tab body).
   * 'fill' participates in a flex/block layout instead, filling whatever
   * space its parent already allocated (use when siblings like a toolbar or
   * banner render above/around it, e.g. WorkspaceTasksTab's toolbar row).
   * Defaults to 'absolute' since that is the more common full-tab case.
   */
  layout?: 'absolute' | 'fill'
  testId?: string
}

export function QueryErrorState({
  message,
  onRetry,
  layout = 'absolute',
  testId,
}: QueryErrorStateProps) {
  // See the deterministic-race note above — skip painting entirely while a
  // forced logout is already in flight.
  if (isForceLoggingOut()) return null

  return (
    <div
      className={
        layout === 'absolute'
          ? 'absolute inset-0 flex flex-col items-center justify-center gap-3 p-8 text-center'
          : 'flex flex-1 h-full flex-col items-center justify-center gap-3 p-8 text-center'
      }
      data-testid={testId ?? 'query-error-state'}
    >
      <WarningCircle size={24} className="text-[var(--color-error)]" aria-hidden="true" />
      <p className="text-sm text-[var(--color-muted)]">{message}</p>
      {onRetry && (
        <button
          tabIndex={0}
          type="button"
          onClick={onRetry}
          className="text-xs text-[var(--color-accent)] underline underline-offset-2"
        >
          Retry
        </button>
      )}
    </div>
  )
}
