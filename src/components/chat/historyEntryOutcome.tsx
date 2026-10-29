// #1452 D2 A2.4 "Expansion outcomes": what a shortened tool row or text part
// shows while its full content is read, and after a read fails. Shared by the
// tool rows (HistoryPreviewToolCall) and the text control (HistoryShowRest) so
// both follow the same rules. The saved preview always stays visible next to
// this outcome.
import type { UseQueryResult } from '@tanstack/react-query'
import { ErrorState } from '@/components/ui/error-state'
import { isApiError } from '@/lib/api-error'

type ReadFailure = 'recoverable' | 'expired' | 'gone' | 'auth' // not-wire-format: client-side outcome class

/**
 * 410 is permanent (expired), 404 is permanent (no longer available), 401 is
 * handed to the existing sign-in handling. Everything else — network error,
 * timeout, 500 and malformed or schema-invalid responses — is recoverable.
 */
export function classifyReadFailure(error: unknown): ReadFailure {
  if (isApiError(error)) {
    if (error.status === 410) return 'expired'
    if (error.status === 404) return 'gone'
    if (error.status === 401) return 'auth'
  }
  return 'recoverable'
}

export function HistoryEntryReadOutcome({ read, incomplete = false }: {
  read: Pick<UseQueryResult<unknown, Error>, 'isError' | 'error' | 'fetchStatus' | 'refetch' | 'isSuccess'>
  /** The read succeeded but did not carry the expected content: recoverable. */
  incomplete?: boolean
}) {
  const retry = () => { void read.refetch() }
  if (read.fetchStatus === 'fetching') {
    return <p role="status" className="font-body text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Loading the full content…</p>
  }
  if (read.isSuccess && incomplete) {
    return <ErrorState message="The full content could not be shown." onRetry={retry} className="items-start py-[var(--space-1)]" />
  }
  if (!read.isError) return null
  switch (classifyReadFailure(read.error)) {
    case 'expired':
      return <ErrorState message="Full content expired — showing the saved preview." className="items-start py-[var(--space-1)]" />
    case 'gone':
      return <ErrorState message="This saved item is no longer available." className="items-start py-[var(--space-1)]" />
    case 'auth':
      // The existing session-expired handling (queryClient's auth recheck)
      // takes over; nothing is retried here.
      return null
    default:
      return <ErrorState message="Couldn't load the full content." onRetry={retry} className="items-start py-[var(--space-1)]" />
  }
}
