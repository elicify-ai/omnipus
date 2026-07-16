import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import {
  Globe,
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  CaretDown,
  CaretUp,
  Camera,
  Broadcast,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

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
    // Try JSON first
    try {
      return JSON.parse(result) as BrowserResult
    } catch {
      // Not JSON — display as plain content. Expected when backend returns text summary.
      return { content: result }
    }
  }
  if (typeof result === 'object') return result as BrowserResult
  return {}
}

export function BrowserNavigateBlock({
  args,
  result,
  isRunning,
  isError,
}: {
  args: BrowserNavigateArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  const url = args.url ?? '(unknown URL)'
  const parsed = parseResult(result)
  const hasResult = result != null
  const screenshotData = parsed.screenshot
  const pageTitle = parsed.title
  const hasDetail = !isRunning && hasResult

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
    <div
      className={cn(
        'mt-2 rounded-md border overflow-hidden text-xs',
        isError && !isRunning
          ? 'border-[var(--color-error)]/30'
          : 'border-[var(--color-border)]'
      )}
    >
      {/* Header — a row of composed controls (mirrors BrowserToolBlock in
          BrowserTool.tsx): the expand/collapse toggle is its own button so
          "Watch live" can be a separate, independently clickable sibling
          rather than nested inside it. */}
      <div className="flex w-full items-center gap-1 bg-[var(--color-surface-1)] transition-colors">
        <button
          type="button"
          onClick={() => hasDetail && setExpanded((e) => !e)}
          className={cn(
            'flex flex-1 min-w-0 items-center gap-2 px-3 py-2 text-left',
            hasDetail && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
            !hasDetail && 'cursor-default'
          )}
          aria-expanded={expanded}
          disabled={!hasDetail}
        >
          <Globe
            size={13}
            weight="duotone"
            className={cn(
              isRunning ? 'text-[var(--color-accent)]' :
              isError ? 'text-[var(--color-error)]' :
              'text-[var(--color-secondary)]'
            )}
          />
          <span className="text-[var(--color-muted)] shrink-0">browser.navigate</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[10px]">
            {displayUrl(url)}
          </span>
          {pageTitle && !isRunning && (
            <span className="text-[var(--color-muted)] truncate max-w-[120px] text-[10px] hidden sm:inline">
              {pageTitle}
            </span>
          )}
        </button>

        {/* "Watch live" is shown on every navigate row, running or completed —
            browser.navigate is the near-universal first browser action, and
            the agent's browser session persists after the call completes. */}
        <button
          type="button"
          onClick={handleWatchLive}
          aria-label="Watch live"
          title="Watch this agent's browser live"
          className="shrink-0 flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-3)] transition-colors"
        >
          <Broadcast size={13} />
          <span>Watch live</span>
        </button>

        <span className="flex items-center gap-1 shrink-0 pr-3 py-2">
          {isRunning ? (
            <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
          ) : isError ? (
            <XCircle size={12} weight="fill" className="text-[var(--color-error)]" />
          ) : hasResult ? (
            <>
              <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
              {screenshotData && <Camera size={11} className="text-[var(--color-muted)]" />}
            </>
          ) : null}
          {hasDetail && (
            <span className="ml-1 text-[var(--color-muted)]">
              {expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
            </span>
          )}
        </span>
      </div>

      {/* Detail panel */}
      {expanded && hasDetail && (
        <div className="border-t border-[var(--color-border)]">
          {/* Full URL breadcrumb */}
          <div className="px-2 py-0.5 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
            <span className="text-[10px] text-[var(--color-muted)] font-mono break-all">{url}</span>
          </div>

          {/* Screenshot indicator (image itself renders in the assistant reply bubble via the media frame). */}
          {screenshotData && (
            <div className="px-3 py-2 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
              <div className="flex items-center gap-1.5">
                <Camera size={11} className="text-[var(--color-muted)]" />
                <span className="text-[10px] text-[var(--color-muted)]">Screenshot captured</span>
              </div>
            </div>
          )}

          {/* Page content preview */}
          {parsed.content && (
            <pre className="px-3 py-2 text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto bg-[var(--color-surface-1)]">
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
            <div className="px-3 py-2 text-[var(--color-error)] text-[10px]">{parsed.error}</div>
          )}
        </div>
      )}
    </div>
  )
}

export const BrowserNavigateUI = makeAssistantToolUI<BrowserNavigateArgs, unknown>({
  toolName: 'browser.navigate',
  render: ({ args, result, status }) => (
    <BrowserNavigateBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

// Underscore alias for the same tool
export const BrowserNavigateUnderscoreUI = makeAssistantToolUI<BrowserNavigateArgs, unknown>({
  toolName: 'browser_navigate',
  render: ({ args, result, status }) => (
    <BrowserNavigateBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})
