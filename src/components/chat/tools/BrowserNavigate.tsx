import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { CaretDown, CaretUp, Camera, Broadcast } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, isCancelledStatus, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { stripUntrustedContentWrapper } from '@/lib/untrustedToolContent'

interface BrowserNavigateArgs {
  url?: string
  screenshot?: boolean
  wait_for?: string
}

interface BrowserResult {
  url?: string
  title?: string
  screenshot?: string // base64-encoded PNG
  content?: string
  error?: string
}

function displayUrl(url: string): string {
  try {
    const u = new URL(url)
    return u.hostname + (u.pathname !== '/' ? u.pathname : '')
  } catch {
    // URL parsing failed — display raw string. Expected for malformed URLs.
    return url
  }
}

function parseResult(result: unknown): BrowserResult {
  if (!result) return {}
  if (typeof result === 'string') {
    // SEC-25: browser.navigate is classified as untrusted (pkg/agent/
    // prompt_guard.go) and the gateway sends the PromptGuard-sanitized
    // string, wrapped in [UNTRUSTED_CONTENT] markers — unwrap before
    // treating this as JSON, otherwise the real url/title never parse out.
    const unwrapped = stripUntrustedContentWrapper(result)
    if (unwrapped === null) {
      // High-strictness install: the backend fully redacted this result.
      return { content: '(content withheld by security policy)' }
    }
    // Try JSON first
    try {
      return JSON.parse(unwrapped) as BrowserResult
    } catch {
      // Not JSON — display as plain content. Expected when backend returns text summary.
      return { content: unwrapped }
    }
  }
  if (typeof result === 'object') return result as BrowserResult
  return {}
}

// Flat text-line redesign (ticket "Tool components in chat", P2): the
// hardcoded Globe identity icon is dropped — it was purely decorative next
// to the fixed "browser.navigate" label, which already says what this row
// is. Status now lives only in the leading dot/spinner.
export function BrowserNavigateBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
  isCancelled,
}: {
  toolName: string
  args: BrowserNavigateArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  // Client-side render gate (issue #494): hides this row when
  // shouldRenderToolCall says so for the given tool name/args, unless the
  // user has opted into verbose chat. Must sit after every hook above and
  // before the JSX return (Rules of Hooks) — mirrors BashOutput.tsx's gate.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  if (
    !shouldRenderToolCall(toolName, args as unknown as Record<string, unknown>, verboseChatEnabled, !!isError)
  ) {
    return null
  }

  const url = args.url ?? '(unknown URL)'
  const parsed = parseResult(result)
  const hasResult = result != null
  const screenshotData = parsed.screenshot
  const pageTitle = parsed.title
  const hasDetail = !isRunning && hasResult

  // Always resolves to a real config — a terminal state (running finished,
  // no error/cancellation) with no result is still a real outcome (e.g. a
  // navigate call that legitimately returned nothing) and must still show a
  // status dot + label, not a silently blank indicator.
  const statusConfig: ToolBadgeStatusConfig = isRunning
    ? getToolBadgeStatusConfig('running', { size: 12 })
    : isCancelled
    ? getToolBadgeStatusConfig('cancelled', { size: 12, cancelledVariant: 'muted' })
    : isError
    ? getToolBadgeStatusConfig('error', { size: 12 })
    : getToolBadgeStatusConfig('success', { size: 12 })

  // ADR-038: "Watch live" opens the app-root BrowserLivePanel overlay onto the
  // browser session driving this tool call. Imperative store reads (not hooks)
  // — mirrors BrowserToolBlock's handleWatchLive in BrowserTool.tsx exactly.
  // Same v1 limitation applies: reads the globally-active session/agent, not
  // necessarily the agent that owns THIS specific tool call.
  function handleWatchLive() {
    const { activeSessionId, activeAgentId } = useSessionStore.getState()
    if (!activeSessionId || !activeAgentId) {
      useUiStore.getState().addToast({ message: 'No active session to watch.', variant: 'error' })
      return
    }
    useUiStore.getState().openBrowserPanel(activeSessionId, activeAgentId)
  }

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no
    // border, no surface fill, no rounded frame, no overflow-hidden — the
    // row is transparent on the thread.
    <div className="mt-2 text-xs font-mono">
      {/* Header — a row of composed controls (mirrors BrowserToolBlock in
          BrowserTool.tsx): the expand/collapse toggle is its own button so
          "Watch live" can be a separate, independently clickable sibling
          rather than nested inside it. */}
      <div className="flex w-full items-center gap-2">
        <button tabIndex={0}
          type="button"
          onClick={() => hasDetail && setExpanded((e) => !e)}
          className={cn(
            'flex flex-1 min-w-0 items-center gap-2 py-1 text-left transition-colors',
            hasDetail && 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer',
            !hasDetail && 'cursor-default'
          )}
          aria-expanded={hasDetail ? expanded : undefined}
          disabled={!hasDetail}
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-muted)] shrink-0">browser.navigate</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[10px]">
            {displayUrl(url)}
          </span>
          {pageTitle && !isRunning && (
            <span className="text-[var(--color-muted)] truncate max-w-[120px] text-[10px] hidden sm:inline">
              {pageTitle}
            </span>
          )}
          <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}>
            {statusConfig.label}
          </span>
          {screenshotData && <Camera size={11} className="text-[var(--color-muted)] shrink-0" />}
        </button>

        {/* "Watch live" is shown on every navigate row, running or completed —
            browser.navigate is the near-universal first browser action, and
            the agent's browser session persists after the call completes. */}
        <button tabIndex={0}
          type="button"
          onClick={handleWatchLive}
          aria-label="Watch live"
          title="Watch this agent's browser live"
          className="shrink-0 flex items-center gap-1 text-[10px] text-[var(--color-accent)] hover:underline transition-colors"
        >
          <Broadcast size={13} />
          <span>Watch live</span>
        </button>

        {hasDetail && (
          <span className="ml-auto shrink-0 text-[var(--color-muted)]">
            {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
          </span>
        )}
      </div>

      {/* Detail panel — indented left-accent block instead of the old
          bordered/backgrounded panel; each section keeps its own spacing
          via space-y-2 rather than individual borders/fills. */}
      {expanded && hasDetail && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] pl-3 py-1 space-y-2">
          {/* Full URL breadcrumb */}
          <div>
            <span className="text-[10px] text-[var(--color-muted)] font-mono break-all">{url}</span>
          </div>

          {/* Screenshot indicator (image itself renders in the assistant reply bubble via the media frame). */}
          {screenshotData && (
            <div className="flex items-center gap-1.5">
              <Camera size={11} className="text-[var(--color-muted)]" />
              <span className="text-[10px] text-[var(--color-muted)]">Screenshot captured</span>
            </div>
          )}

          {/* Page content preview */}
          {parsed.content && (
            <pre className="text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
              {parsed.content.slice(0, 2000)}
              {parsed.content.length > 2000 && (
                <span className="text-[var(--color-muted)] italic">
                  {'\n'}... (content truncated)
                </span>
              )}
            </pre>
          )}

          {/* Error */}
          {parsed.error && (
            <div className="text-[var(--color-error)] text-[10px]">{parsed.error}</div>
          )}
        </div>
      )}
    </div>
  )
}

// Issue #617: isError comes from the tool-call part's own `isError` field
// (set in omnipus-runtime.ts from the store's resolved ToolCall.status), not
// from `status.type === 'incomplete'` — that can never be true for a
// finished call carrying a result (see BashOutput.tsx's makeBashUI comment
// for the full mechanism).
export const BrowserNavigateUI = makeAssistantToolUI<BrowserNavigateArgs, unknown>({
  toolName: 'browser.navigate',
  render: ({ args, result, status, isError }) => (
    <BrowserNavigateBlock
      toolName="browser.navigate"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

// Underscore alias for the same tool
export const BrowserNavigateUnderscoreUI = makeAssistantToolUI<BrowserNavigateArgs, unknown>({
  toolName: 'browser_navigate',
  render: ({ args, result, status, isError }) => (
    <BrowserNavigateBlock
      toolName="browser_navigate"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})
