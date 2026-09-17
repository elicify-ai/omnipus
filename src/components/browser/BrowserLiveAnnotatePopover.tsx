// Annotate comment popover (ADR-039 D-B1/B2) — appears once a drag/click
// selection finalizes into a cropped pendingAnnotation.

import { Textarea } from '@/components/ui/textarea'

export function BrowserLiveAnnotatePopover({
  previewUrl,
  comment,
  onCommentChange,
  submitting,
  error,
  onCancel,
  onSend,
}: {
  previewUrl: string
  comment: string
  onCommentChange: (value: string) => void
  submitting: boolean
  error: string | null
  onCancel: () => void
  onSend: () => void
}) {
  return (
    <div
      data-testid="annotate-popover"
      className="absolute inset-x-0 bottom-0 z-20 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 shadow-lg"
    >
      <div className="flex items-start gap-3">
        <img
          src={previewUrl}
          alt="Selected region"
          className="h-16 w-16 shrink-0 rounded border border-[var(--color-border)] object-cover"
        />
        <div className="min-w-0 flex-1">
          <Textarea
            value={comment}
            onChange={(e) => onCommentChange(e.target.value)}
            onKeyDown={(e) => {
              // Cancel the pending annotation on Escape. Historical
              // note (now stale): this used to also matter for
              // outrunning Radix's Sheet, which listened for Escape
              // via a capture-phase document listener that would
              // otherwise close the whole panel and discard the
              // drafted comment first. The Sheet was retired
              // 2026-07-16 (panel is now always a plain docked
              // `<aside>` — no Sheet, no capture-phase listener
              // anywhere above this element), so that race no longer
              // exists; Escape here is just the ordinary "cancel this
              // popover" affordance. stopPropagation is kept as
              // defense in depth against any future wrapping
              // dialog/modal reintroducing the same race.
              if (e.key === 'Escape') {
                e.stopPropagation()
                onCancel()
              }
            }}
            placeholder="What would you like to discuss about this?"
            aria-label="Annotation comment"
            className="min-h-[60px] text-xs"
            disabled={submitting}
            autoFocus
          />
          {error && (
            <p role="alert" className="mt-1 text-[11px] text-[var(--color-error)]">
              {error}
            </p>
          )}
          <div className="mt-2 flex justify-end gap-2">
            <button tabIndex={0}
              type="button"
              onClick={onCancel}
              disabled={submitting}
              className="rounded px-2.5 py-1 text-xs text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              Cancel
            </button>
            <button tabIndex={0}
              type="button"
              onClick={onSend}
              disabled={submitting || comment.trim().length === 0}
              className="rounded bg-[var(--color-accent)] px-3 py-1 text-xs font-medium text-[var(--color-primary)] transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {submitting ? 'Sending…' : 'Send'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
