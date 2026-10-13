// ErrorDetailDisclosure — the verbose-only "Technical details" disclosure for
// typed LLM errors (ADR-051). Extracted verbatim from ChatScreen.tsx's
// VirtualAssistantMessageRow (the historical/replay render path) so the U8
// participant-label work could keep that grandfathered file from growing; the
// JSX is unchanged. Mounted ONLY when (status==='error' && errorCode &&
// verbose && errorDetail), ABSENT from the DOM otherwise. Replay frames carry
// no errorDetail (the gateway strips it before persisting), so on the
// historical path this effectively fires only when a live error bubble with
// detail set is still in the ring buffer after a reload. MessageItem.tsx
// (live render path) keeps its own near-identical block — it additionally
// renders the provider/model/request-id facts disclosure, which the replay
// payload never carries.

import { cn } from '@/lib/utils'
import { formatErrorDetail } from '@/lib/llm-error'

// ADR-051 — cap on the disclosure content, mirrored between the two render
// paths so live and replay show the same truncated length when a provider's
// error payload is verbose.
const ERROR_DETAIL_MAX_CHARS = 512

export function ErrorDetailDisclosure({ message, verboseChatEnabled }: {
  message: { status?: string; errorCode?: string; errorDetail?: string }
  verboseChatEnabled: boolean
}) {
  if (!(message.status === 'error' && message.errorCode && verboseChatEnabled && message.errorDetail && message.errorDetail.length > 0)) {
    return null
  }
  return (
    <details className="px-[var(--space-1)] mt-[var(--space-1)]" data-testid="error-detail-disclosure">
      <summary
        tabIndex={0}
        className={cn(
          'text-[length:var(--type-caption-size)] text-[var(--color-muted)] cursor-pointer select-none',
          'inline-flex items-center gap-[var(--space-1)] hover:text-[var(--color-secondary)] transition-colors',
        )}
      >
        Technical details
      </summary>
      <pre
        className={cn(
          'mt-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] rounded-md',
          'bg-[var(--color-surface-2)] text-[var(--color-secondary)]',
          'font-mono text-[length:var(--type-caption-size)] leading-relaxed whitespace-pre-wrap break-all',
          'max-h-40 overflow-y-auto',
        )}
      >
        {formatErrorDetail(message.errorDetail).slice(0, ERROR_DETAIL_MAX_CHARS)}
      </pre>
    </details>
  )
}
