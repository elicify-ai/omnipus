import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { ArrowSquareOut } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useSessionStore } from '@/store/session'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'
import { isSafeHref } from '@/lib/url-safe'
import {
  ToolResultSentinelBody,
  isSentinelResolution,
  resolveToolResult,
} from './toolResultDisplay'

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

// Exported for ChatScreen.tsx's history-replay loop: a reloaded `fetch_url`/
// `web_fetch` call must render the same dedicated row the live path shows
// (ticket "chat tool-UI collapse", item 4).
export function WebFetchBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
  isCancelled,
  error,
  sessionId,
}: {
  toolName: string
  args: WebFetchArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
  /** Failed call's reason (frame.error via replay.go::applyPersistedFailureReason). Rendered in the expanded panel when there is no fetched content. */
  error?: string
  /** Session this call belongs to — required to fetch a ToolResultRef sentinel's full body session-scoped. The live path falls back to the active session. */
  sessionId?: string
}) {
  const [expanded, setExpanded] = useState(false)
  // ctui-gate fix 1: live path has no sessionId prop — fall back to the
  // active session (same fallback BashOutputBlock uses).
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  // Client-side render gate (issue #494): mirrors BashOutput.tsx's gate —
  // hides this row when shouldRenderToolCall says so, unless verbose chat is
  // on. Must sit after every hook above and before the JSX return.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  if (
    !shouldRenderToolCall(toolName, args as unknown as Record<string, unknown>, verboseChatEnabled, !!isError)
  ) {
    return null
  }

  const url = args.url ?? '(unknown URL)'
  // ctui-gate fix 1: resolve through the shared module — a `{ text }`
  // envelope, an offload/truncation/marshal-error sentinel, or a structured
  // failure renders its dedicated display instead of `String(result)`'s
  // "[object Object]". The preview derives ONLY from a plain-string body.
  const resolved = resolveToolResult(result)
  const content = resolved.kind === 'text' ? resolved.text : ''
  const { preview, truncated } = content ? truncateContent(content) : { preview: '', truncated: false }
  // Only render the "open in new tab" action link for a real http(s)/mailto/tel
  // URL — never for javascript:/data:/etc. Reuses the same allow-list IframePreview
  // already applies to preview URLs (@/lib/url-safe), so both surfaces agree.
  const linkable = isSafeHref(url)
  // Nothing to expand until the call finishes with actual content (a result,
  // the failure reason, or a structured sentinel) — mirrors
  // GenericToolCall.tsx's `hasDetail` gate.
  const hasDetail = !isRunning && (!!content || !!error || isSentinelResolution(resolved))

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
    <div className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
      <div className="flex w-full items-center gap-[var(--space-2)]">
        {/* Header */}
        <DisclosureRow
          expanded={expanded}
          onExpandedChange={setExpanded}
          expandable={hasDetail}
          data-testid="web-fetch-toggle"
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-muted)] shrink-0">web_fetch</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[length:var(--type-caption-size)]">
            {displayUrl(url)}
          </span>
          <span className={cn('text-[var(--color-muted)] shrink-0')}>
            {statusConfig.label}
          </span>
        </DisclosureRow>
        {linkable && (
          <a tabIndex={0}
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            aria-label="Open fetched URL"
            title="Open in new tab"
            className="shrink-0 flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-accent)] hover:underline transition-colors"
          >
            <ArrowSquareOut size={12} />
          </a>
        )}
      </div>

      {/* Content panel — left-accent block, no bordered card. Fetched-page
          preview keeps its identity (plain text preview), just without a
          bordered/backgrounded breadcrumb row. */}
      {expanded && hasDetail && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] py-[var(--space-1)] pl-[var(--space-2-5)]">
          <div className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] font-mono break-all mb-[var(--space-1)]">{url}</div>
          {isSentinelResolution(resolved) ? (
            <ToolResultSentinelBody
              resolved={resolved}
              sessionId={sessionId ?? activeSessionId ?? ''}
            />
          ) : (
            <pre className="text-[length:var(--type-caption-size)] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto">
              {content ? (
                <>
                  {preview}
                  {truncated && (
                    <span className="text-[var(--color-muted)] italic">{'\n'}... (content truncated)</span>
                  )}
                </>
              ) : error ? (
                <span className="italic text-[var(--color-error)] break-words">{error}</span>
              ) : null}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

// Issue #617: isError comes from the tool-call part's own `isError` field
// (set in omnipus-runtime.ts from the store's resolved ToolCall.status), not
// from `status.type === 'incomplete'` — that can never be true for a
// finished call carrying a result.
// New canonical name (post §7 rename: web_fetch → fetch_url)
export const WebFetchPreviewUI = makeAssistantToolUI<WebFetchArgs, unknown>({
  toolName: 'fetch_url',
  render: ({ args, result, status, isError }) => (
    <WebFetchBlock
      toolName="fetch_url"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

// Legacy alias kept for backward compat with old session transcripts.
export const WebFetchLegacyUI = makeAssistantToolUI<WebFetchArgs, unknown>({
  toolName: 'web_fetch',
  render: ({ args, result, status, isError }) => (
    <WebFetchBlock
      toolName="web_fetch"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})
