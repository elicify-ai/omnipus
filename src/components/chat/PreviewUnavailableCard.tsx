// PreviewUnavailableCard — the persistent "Page previews unavailable" card of
// an office-preview thumbnail group (office-preview spec §4.3, §5.3; FQ2 r2 A).
//
// Shown when the latest outcome of a preview_document call's thumbnails is
// `unavailable` — renderer missing/damaged/failed, storage failed, including
// the native-model partial success where the model got the PDF but no pages
// were rendered. It never claims pages were rendered.
//
// Retry calls POST /sessions/{session_id}/previews/{tool_call_id}/retry
// (generated route). 202 only means "started": the result arrives later as a
// new media frame with the same tool_call_id, which replaces this card (the
// store drops the old marker). Retry is hidden for `renderer_missing`, which
// shows how to install the renderer instead.

import { useState } from 'react'
import { ArrowsClockwise, FileX } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { retrySessionPreview } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import type { MediaAttachment } from '@/store/chat'
import type { MediaPreview } from '@/lib/api/generated/asyncapi-types'
import { unavailableReasonText } from './previewGroups'

type RetryState =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | { kind: 'started' }
  | { kind: 'source_changed' }
  | { kind: 'error'; message: string; canRetry: boolean }

export interface PreviewUnavailableCardProps {
  sessionId: string | null
  toolCallId?: string
  sourceName: string
  reason: MediaPreview['unavailable_reason'] | null
  /** The store's state marker; a new object (new frame) resets the Retry state. */
  marker: MediaAttachment
}

function retryErrorState(err: unknown): RetryState {
  if (isApiError(err)) {
    if (err.status === 409 && err.code === 'preview_source_changed') return { kind: 'source_changed' }
    if (err.status === 503 && err.code === 'preview_renderer_unavailable') {
      return { kind: 'error', message: 'Retry failed: the PDF renderer is not available.', canRetry: true }
    }
    // 403 no access, 404 unknown preview: retrying again cannot succeed.
    const permanent = err.status === 403 || err.status === 404
    return { kind: 'error', message: `Retry failed: ${err.userMessage}`, canRetry: !permanent }
  }
  return { kind: 'error', message: `Retry failed: ${err instanceof Error ? err.message : String(err)}`, canRetry: true }
}

export function PreviewUnavailableCard({ sessionId, toolCallId, sourceName, reason, marker }: PreviewUnavailableCardProps) {
  // Reset when a new marker arrives (derive-from-props pattern, as ChatImage).
  const [retry, setRetry] = useState<{ marker: MediaAttachment; state: RetryState }>({ marker, state: { kind: 'idle' } })
  if (retry.marker !== marker) setRetry({ marker, state: { kind: 'idle' } })
  const state = retry.marker === marker ? retry.state : { kind: 'idle' as const }

  const reasonText = unavailableReasonText(reason)
  const headline = reasonText ? `Page previews unavailable — ${reasonText}` : 'Page previews unavailable'
  const retryable =
    reason !== 'renderer_missing' &&
    !!toolCallId &&
    !!sessionId &&
    state.kind !== 'source_changed' &&
    !(state.kind === 'error' && !state.canRetry)

  const runRetry = async () => {
    if (!sessionId || !toolCallId) return
    setRetry({ marker, state: { kind: 'pending' } })
    try {
      await retrySessionPreview(sessionId, toolCallId)
      setRetry((cur) => (cur.marker === marker ? { marker, state: { kind: 'started' } } : cur))
    } catch (err) {
      console.warn('[PreviewUnavailableCard] preview retry failed', { toolCallId, err })
      setRetry((cur) => (cur.marker === marker ? { marker, state: retryErrorState(err) } : cur))
    }
  }

  let status: string | null = null
  if (state.kind === 'pending' || state.kind === 'started') status = 'Retrying — rendering the pages again…'
  else if (state.kind === 'source_changed') status = 'The file changed — ask the agent to preview again.'
  else if (state.kind === 'error') status = state.message

  return (
    <Card
      variant="inset"
      data-testid="preview-unavailable-card"
      data-reason={reason ?? undefined}
      className="flex flex-col gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-2)] max-w-md border border-[var(--color-border)]"
    >
      <div className="flex items-start gap-[var(--space-2)]">
        <FileX size={18} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
        <div className="min-w-0 flex flex-col gap-[var(--space-1)]">
          <p className="text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-secondary)] break-words">{headline}</p>
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] break-words">{sourceName}</p>
          {reason === 'renderer_missing' && (
            <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] break-words" data-testid="preview-install-guidance">
              To see page previews, install the PDF renderer: re-run the Omnipus installer with the PDF renderer
              component, or register it with <code>omnipus pdf-renderer register &lt;folder&gt;</code>.
            </p>
          )}
          <p
            role="status"
            aria-live="polite"
            className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] break-words"
            data-testid="preview-retry-status"
          >
            {status ?? ''}
          </p>
        </div>
      </div>
      {retryable && (
        <div>
          <Button
            variant="outline"
            size="sm"
            actionState={state.kind === 'pending' ? 'pending' : 'idle'}
            aria-disabled={state.kind === 'started' || undefined}
            onClick={() => {
              if (state.kind === 'pending' || state.kind === 'started') return
              void runRetry()
            }}
            className="h-auto gap-[var(--space-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] aria-disabled:opacity-50"
          >
            <ArrowsClockwise size={12} aria-hidden="true" />
            Retry
          </Button>
        </div>
      )}
    </Card>
  )
}
