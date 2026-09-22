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
// Once forceLogout() begins, its synchronous flag suppresses actionable
// error UI during the redirect. The global 401 handler first awaits a fresh
// session-validity check, so this adapter does not suppress query errors
// during that preceding validation window.
import { isForceLoggingOut } from '@/lib/authLogout'
import {
  QueryErrorState as QueryErrorStatePresentation,
  type QueryErrorStateProps,
} from '@/components/ui/query-error-state'

export type { QueryErrorStateProps }

export function QueryErrorState({
  message,
  onRetry,
  layout = 'absolute',
  testId,
  className,
}: QueryErrorStateProps) {
  // Skip painting while a confirmed forced logout is in flight.
  if (isForceLoggingOut()) return null

  return <QueryErrorStatePresentation message={message} onRetry={onRetry} layout={layout} testId={testId} className={className} />
}
