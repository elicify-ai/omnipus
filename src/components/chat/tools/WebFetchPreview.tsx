import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { CaretDown, CaretUp, ArrowSquareOut } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'
import { isSafeHref } from '@/lib/url-safe'

interface WebFetchArgs {
  url?: string
  max_chars?: number
  start_index?: number
}

function truncateContent(text: string, maxLines = 30): { preview: string; truncated: boolean } {
  const lines = text.split('\n')
  if (lines.length <= maxLines) return { preview: text, truncated: false }
  return {
    preview: lines.slice(0, maxLines).join('\n'),
    truncated: true,
  }
}

function displayUrl(url: string): string {
  try {
    const u = new URL(url)
    return u.hostname + (u.pathname !== '/' ? u.pathname : '')
  } catch {
    return url
  }
}

function WebFetchBlock({
  args,
  result,
  isRunning,
  isError,
  isCancelled,
}: {
  args: WebFetchArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  const url = args.url ?? '(unknown URL)'
  const content = result != null ? String(result) : ''
  const { preview, truncated } = content ? truncateContent(content) : { preview: '', truncated: false }
  // Only render the "open in new tab" action link for a real http(s)/mailto/tel
  // URL — never for javascript:/data:/etc. Reuses the same allow-list IframePreview
  // already applies to preview URLs (@/lib/url-safe), so both surfaces agree.
  const linkable = isSafeHref(url)
  // Nothing to expand until the call finishes with actual content — mirrors
  // GenericToolCall.tsx's `hasDetail` gate.
  const hasDetail = !isRunning && !!content

  // Switched from a bare statusDot to the full getToolBadgeStatusConfig so
  // the status label (Running.../Done/Failed/Cancelled) is always rendered
  // next to the dot, like BashOutput does — a dot-only signal is
  // indistinguishable for colorblind users/screen readers (WCAG 1.4.1).
  // Always resolves to a real config: a completed fetch with empty content
  // (no error, no cancellation) is still a real "Done" outcome, not a
  // silent blank row.
  const statusConfig = getToolBadgeStatusConfig(
    isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
    { size: 12, cancelledVariant: 'muted' }
  )

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no card
    // frame — see GenericToolCall.tsx/toolStatusConfig.tsx for the reference
    // language. The decorative Globe tool-type icon is gone; the leading slot
    // is the status dot/spinner only. The "open fetched URL" action link is a
    // separate sibling control (mirrors GenericToolCall's "Watch live"), not
    // nested inside the toggle button, so it stays independently clickable.
    <div className="mt-2 text-xs font-mono">
      <div className="flex w-full items-center gap-2">
        {/* Header */}
        <button tabIndex={0}
          type="button"
          onClick={() => hasDetail && setExpanded((e) => !e)}
          className={cn(
            'flex min-w-0 flex-1 items-center gap-2 py-1 transition-colors text-left',
            hasDetail && 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer',
            !hasDetail && 'cursor-default'
          )}
          aria-expanded={hasDetail ? expanded : undefined}
          disabled={!hasDetail}
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-muted)] shrink-0">web_fetch</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[10px]">
            {displayUrl(url)}
          </span>
          <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}>
            {statusConfig.label}
          </span>
        </button>
        {linkable && (
          <a tabIndex={0}
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            aria-label="Open fetched URL"
            title="Open in new tab"
            className="shrink-0 flex items-center gap-1 text-[10px] text-[var(--color-accent)] hover:underline transition-colors"
          >
            <ArrowSquareOut size={12} />
          </a>
        )}
        {hasDetail && (
          <span className="ml-auto shrink-0 text-[var(--color-muted)]">
            {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
          </span>
        )}
      </div>

      {/* Content panel — left-accent block, no bordered card. Fetched-page
          preview keeps its identity (plain text preview), just without a
          bordered/backgrounded breadcrumb row. */}
      {expanded && hasDetail && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] py-1 pl-3">
          <div className="text-[10px] text-[var(--color-muted)] font-mono break-all mb-1">{url}</div>
          <pre className="text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto">
            {preview}
            {truncated && (
              <span className="text-[var(--color-muted)] italic">{'\n'}... (content truncated)</span>
            )}
          </pre>
        </div>
      )}
    </div>
  )
}

// New canonical name (post §7 rename: web_fetch → fetch_url)
export const WebFetchPreviewUI = makeAssistantToolUI<WebFetchArgs, unknown>({
  toolName: 'fetch_url',
  render: ({ args, result, status }) => (
    <WebFetchBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

// Legacy alias kept for backward compat with old session transcripts.
export const WebFetchLegacyUI = makeAssistantToolUI<WebFetchArgs, unknown>({
  toolName: 'web_fetch',
  render: ({ args, result, status }) => (
    <WebFetchBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})
