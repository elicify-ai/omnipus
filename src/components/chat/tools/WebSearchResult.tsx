import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { ArrowSquareOut } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useSessionStore } from '@/store/session'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'
import {
  ToolResultSentinelBody,
  isSentinelResolution,
  resolveToolResult,
} from './toolResultDisplay'

interface WebSearchArgs {
  query?: string
  count?: number
  provider?: string
}

interface ParsedResult {
  index: number
  title: string
  url: string
  snippet: string
}

/** Best-effort parser for the text-based search results returned by web_search */
function parseSearchResults(text: string): ParsedResult[] {
  const results: ParsedResult[] = []
  // Pattern: lines starting with "1. Title\n   URL" or similar
  const blocks = text.split(/\n(?=\d+\. )/)

  for (const block of blocks) {
    const firstLine = block.split('\n')[0]
    const match = firstLine.match(/^(\d+)\.\s+(.+)/)
    if (!match) continue

    const index = parseInt(match[1], 10)
    const restLines = block.split('\n').slice(1)

    // Find URL line (starts with http or 3+ spaces)
    const urlLine = restLines.find((l) => l.trim().startsWith('http'))
    const url = urlLine?.trim() ?? ''

    // Title from match or URL
    const title = match[2].trim()

    // Snippet is everything else
    const snippet = restLines
      .filter((l) => l.trim() !== url)
      .join(' ')
      .trim()

    results.push({ index, title, url, snippet })
  }

  return results
}

export function WebSearchBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
  isCancelled,
  error,
  sessionId,
}: {
  /** The wire tool name this row renders — `search_web` (canonical) or the `web_search` legacy alias. */
  toolName: string
  args: WebSearchArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
  /** Failed call's reason (frame.error via replay.go::applyPersistedFailureReason). Rendered in the expanded panel when there is no result text. */
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

  const query = args.query ?? '(search query)'
  // ctui-gate fix 1: resolve through the shared module — a `{ text }`
  // envelope, an offload/truncation/marshal-error sentinel, or a structured
  // failure renders its dedicated display instead of `String(result)`'s
  // "[object Object]". The hit count derives ONLY from a plain-string
  // listing (honest 0 for any other shape) — never from a stringified object.
  const resolved = resolveToolResult(result)
  const content = resolved.kind === 'text' ? resolved.text : ''
  const parsed = content ? parseSearchResults(content) : []
  const hasStructured = parsed.length > 0
  // Nothing to expand until the call finishes with actual content (a result,
  // the failure reason, or a structured sentinel) — mirrors
  // GenericToolCall.tsx's `hasDetail` gate.
  const hasDetail = !isRunning && (!!content || !!error || isSentinelResolution(resolved))

  // Always resolves to a real config (running/cancelled/error/success) so
  // every terminal state gets a status dot — a failed search previously
  // rendered a GREEN dot (or nothing for an empty/no-content success), which
  // is indistinguishable from success for colorblind users/screen readers.
  const statusConfig = getToolBadgeStatusConfig(
    isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
    { size: 12, cancelledVariant: 'muted' }
  )
  // On a real success, always show the parsed-results count — including
  // "0 results" for the parse-nothing case (previously silent: no count text
  // shown at all when hasStructured was false). Running/error/cancelled show
  // the shared status label instead, matching BashOutput/WebFetchPreview.
  const countOrStatusLabel =
    isRunning || isError || isCancelled ? statusConfig.label : `${parsed.length} results`

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no card
    // frame — see GenericToolCall.tsx/toolStatusConfig.tsx for the reference
    // language. The decorative MagnifyingGlass tool-type icon is gone; the
    // leading slot is the status dot/spinner only, same as the other rows.
    <div className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
      {/* Header */}
      <DisclosureRow
        expanded={expanded}
        onExpandedChange={setExpanded}
        expandable={hasDetail}
        data-testid="web-search-toggle"
      >
        {statusConfig.indicator}
        <span className="shrink-0 text-[var(--color-muted)]">{toolName}</span>
        <span className="text-[var(--color-secondary)] truncate flex-1 min-w-0 italic">{query}</span>
        <span className={cn('text-[var(--color-muted)] shrink-0')}>
          {countOrStatusLabel}
        </span>
      </DisclosureRow>

      {/* Results panel — left-accent block, no bordered card. Inner content
          keeps its identity (numbered result list / raw preview) but the
          old divide-y row dividers are gone; spacing carries the separation. */}
      {expanded && hasDetail && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] py-[var(--space-1)] pl-[var(--space-2-5)]">
          {isSentinelResolution(resolved) ? (
            <ToolResultSentinelBody
              resolved={resolved}
              sessionId={sessionId ?? activeSessionId ?? ''}
            />
          ) : hasStructured ? (
            <div className="space-y-[var(--space-2)]">
              {parsed.map((item) => (
                <div key={item.index} className="flex items-start gap-[var(--space-1)]">
                  <span className="text-[var(--color-muted)] shrink-0 mt-[var(--space-0-5)]">{item.index}.</span>
                  <div className="min-w-0">
                    <p className="text-[var(--color-secondary)] font-medium leading-snug break-words">
                      {item.title}
                    </p>
                    {item.url && (
                      <p className="text-[var(--color-accent)] font-mono text-[length:var(--type-caption-size)] truncate flex items-center gap-[var(--space-1)]">
                        {item.url}
                        <ArrowSquareOut size={9} className="shrink-0" />
                      </p>
                    )}
                    {item.snippet && (
                      <p className="text-[var(--color-muted)] text-[length:var(--type-caption-size)] leading-relaxed mt-[var(--space-0-5)] line-clamp-2">
                        {item.snippet}
                      </p>
                    )}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <pre className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto">
              {content || (error ? (
                <span className="italic text-[var(--color-error)] break-words">{error}</span>
              ) : null)}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

// Issue #617: isError comes from the tool-call part's own `isError` field
// (set in omnipus-runtime.ts from the store's real resolved status), not
// from `status.type === 'incomplete'` — that can never be true for a
// finished call carrying a result.
function makeWebSearchUI(toolName: string) {
  return makeAssistantToolUI<WebSearchArgs, unknown>({
    toolName,
    render: ({ args, result, status, isError }) => (
      <WebSearchBlock
        toolName={toolName}
        args={args ?? {}}
        result={result}
        isRunning={status.type === 'running'}
        isError={isError}
        isCancelled={isCancelledStatus(status)}
      />
    ),
  })
}

// Canonical backend name (pkg/tools/web.go::WebSearchTool.Name) — issue #898:
// this was claimed as registered in OmnipusRuntimeProvider.tsx's comment but
// never actually registered, so live AND replayed `search_web` calls fell
// through to the generic badge.
export const WebSearchCanonicalUI = makeWebSearchUI('search_web')

// Legacy alias kept for backward compat with old session transcripts only
// (historical JSONL is never migrated). Do NOT use this name for new calls.
export const WebSearchResultUI = makeWebSearchUI('web_search')
