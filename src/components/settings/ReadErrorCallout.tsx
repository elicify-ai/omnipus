// ReadErrorCallout — one group of a Settings section whose read failed
// (Settings redesign #1519, FR-066). It names what failed, quotes the
// server's cause, gives one thing to do and offers Retry, which re-sends only
// that read. The group it sits in is blocked from saving; other groups keep
// their drafts and still save.

import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { getErrorMessage } from '@/lib/api'

export interface ReadErrorCalloutProps { // not-wire-format: React props
  /** Names what failed: "Agents and goals could not be loaded". */
  title: string
  /** The failed read's error; its message is quoted. */
  error: unknown
  /** Re-sends the failed read only. */
  onRetry: () => void
  /** The one thing to do; defaults to pressing Retry. */
  action?: string
  /** True while the read is running again. */
  retrying?: boolean
  /** False when the group can still save without this read. */
  blocksSaving?: boolean
  'data-testid'?: string
  retryTestId?: string
}

export function ReadErrorCallout({
  title,
  error,
  onRetry,
  action = 'Press Retry to load it again.',
  retrying = false,
  blocksSaving = true,
  'data-testid': testId,
  retryTestId,
}: ReadErrorCalloutProps) {
  return (
    <Callout variant="error" data-testid={testId}>
      <CalloutTitle>{title}</CalloutTitle>
      <CalloutDescription>
        {getErrorMessage(error, 'The gateway did not answer.')} {action}
        {blocksSaving ? ' Nothing here can be saved until it loads.' : ''}
      </CalloutDescription>
      <Button variant="outline" size="sm" data-testid={retryTestId} onClick={onRetry} disabled={retrying}>
        Retry
      </Button>
    </Callout>
  )
}
