// Generic browser tool UI — handles browser.click, browser.type,
// browser.screenshot, browser.get_text, browser.wait, browser.evaluate.
//
// Each of the six browser tools is registered twice: once with the canonical
// dot name (e.g. `browser.click`) and once with an underscore alias
// (`browser_click`) for runtimes that reject dots in tool names. Both variants
// share the same renderer.

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
  CursorClick,
  Keyboard,
  TextT,
  Timer,
  Code,
  Broadcast,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

// ── Shared result parser ──────────────────────────────────────────────────────

interface GenericBrowserResult {
  screenshot?: string // base64 PNG (browser.screenshot)
  text?: string       // browser.get_text
  result?: unknown    // browser.evaluate JS result
  error?: string
  [key: string]: unknown
}

// Tools whose backend returns a raw string (not JSON) by design.
// Other tools that return a non-JSON string are reporting a malformed
// payload — we surface it as an error rather than silently coercing it
// into a `text` field which would mask the failure as success.
const RAW_STRING_RESULT_TOOLS = new Set([
  'browser.get_text',
  'browser_get_text',
  'browser.screenshot',
  'browser_screenshot',
])

function parseResult(result: unknown, toolName: string): GenericBrowserResult {
  if (!result) return {}
  if (typeof result === 'string') {
    try {
      return JSON.parse(result) as GenericBrowserResult
    } catch (err) {
      if (RAW_STRING_RESULT_TOOLS.has(toolName)) {
        return { text: result }
      }
      console.warn(
        `[BrowserTool] ${toolName} returned non-JSON string result`,
        err,
        result.slice(0, 200),
      )
      return {
        error: `Malformed result (expected JSON): ${result.slice(0, 200)}`,
      }
    }
  }
  if (typeof result === 'object') return result as GenericBrowserResult
  console.warn(`[BrowserTool] ${toolName} returned unexpected result type`, typeof result)
  return { error: `Unexpected result type: ${typeof result}` }
}

// ── Shared block renderer ─────────────────────────────────────────────────────

interface BrowserToolBlockProps {
  toolName: string
  icon: typeof Globe
  args: Record<string, unknown>
  result: unknown
  status: { type: string }
  summary: string
}

export function BrowserToolBlock({
  toolName,
  icon: ToolIcon,
  args,
  result,
  status,
  summary,
}: BrowserToolBlockProps) {
  const [expanded, setExpanded] = useState(false)

  const isRunning = status.type === 'running'
  const isError = status.type === 'incomplete'
  const hasResult = result != null
  const hasDetail = !isRunning && hasResult

  const parsed = parseResult(result, toolName)

  function iconColorClass(): string {
    if (isRunning) return 'text-[var(--color-accent)]'
    if (isError) return 'text-[var(--color-error)]'
    return 'text-[var(--color-secondary)]'
  }

  function renderStatusIcon(): React.ReactNode {
    if (isRunning) {
      return <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
    }
    if (isError) {
      return <XCircle size={12} weight="fill" className="text-[var(--color-error)]" />
    }
    if (hasResult) {
      return <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
    }
    return null
  }

  // ADR-038: "Watch live" opens the app-root BrowserLivePanel overlay onto the
  // browser session driving this tool call. Imperative store reads (not hooks)
  // — this block renders once per browser tool call in a conversation, so
  // subscribing here would cause needless re-renders across the whole list.
  //
  // v1 limitation: this reads the globally-active session/agent, not the
  // agent that actually owns THIS tool call. For a delegated sub-turn (this
  // browser call made by a subagent mid-delegation), the globally-active
  // agent in the store may be the parent, not the delegate that opened the
  // browser session — "Watch live" would then attach to the wrong agent's
  // browser (or none). Fine for the common case (no delegation in flight);
  // revisit if/when this tool block gets access to its own owning agent id.
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
      {/* Header — a row of composed controls (mirrors ChromeBar in IframePreview.tsx):
          the expand/collapse toggle is its own button so "Watch live" can be a
          separate, independently clickable sibling rather than nested inside it. */}
      <div className="flex w-full items-center gap-1 bg-[var(--color-surface-1)] transition-colors">
        <button
          type="button"
          onClick={() => hasDetail && setExpanded((e) => !e)}
          className={cn(
            'flex flex-1 min-w-0 items-center gap-2 px-3 py-2 text-left',
            hasDetail && 'hover:bg-[var(--color-surface-2)] cursor-pointer',
            !hasDetail && 'cursor-default'
          )}
          aria-expanded={expanded}
          disabled={!hasDetail}
        >
          <ToolIcon size={13} weight="duotone" className={iconColorClass()} />
          <span className="text-[var(--color-muted)] shrink-0">{toolName}</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[10px]">
            {summary}
          </span>
        </button>

        {/* "Watch live" is shown on every browser tool-call row, not only while
            the call is running: browser tools complete in well under a second,
            but the agent's browser session persists afterwards, so the viewer
            can open the live panel to watch/continue at any point. */}
        <button
          type="button"
          onClick={handleWatchLive}
          aria-label="Watch live"
          title="Watch this agent's browser live"
          className="shrink-0 flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
        >
          <Broadcast size={13} />
          <span>Watch live</span>
        </button>

        <span className="flex items-center gap-1 shrink-0 pr-3 py-2">
          {renderStatusIcon()}
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
          {/* Args row */}
          {Object.keys(args).length > 0 && (
            <div className="px-3 py-2 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
              <p className="text-[10px] text-[var(--color-muted)] mb-1 uppercase tracking-wider">Args</p>
              <pre className="text-[10px] font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all">
                {JSON.stringify(args, null, 2)}
              </pre>
            </div>
          )}

          {/* Screenshot indicator (image itself renders in the assistant reply bubble via the media frame). */}
          {parsed.screenshot && (
            <div className="px-3 py-2 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
              <div className="flex items-center gap-1.5">
                <Camera size={11} className="text-[var(--color-muted)]" />
                <span className="text-[10px] text-[var(--color-muted)]">Screenshot captured</span>
              </div>
            </div>
          )}

          {/* Text output (browser.get_text) */}
          {parsed.text && !parsed.screenshot && (
            <pre className="px-3 py-2 text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
              {String(parsed.text).slice(0, 4000)}
              {String(parsed.text).length > 4000 && (
                <span className="text-[var(--color-muted)] italic">
                  {'\n'}... (content truncated)
                </span>
              )}
            </pre>
          )}

          {/* JS evaluate result (browser.evaluate) */}
          {parsed.result !== undefined && (
            <div className="px-3 py-2 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
              <p className="text-[10px] text-[var(--color-muted)] mb-1 uppercase tracking-wider">Result</p>
              <pre className="text-[10px] font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-40 overflow-auto">
                {JSON.stringify(parsed.result, null, 2)}
              </pre>
            </div>
          )}

          {/* Error */}
          {parsed.error && (
            <div className="px-3 py-2 text-[var(--color-error)] text-[10px] bg-[var(--color-surface-1)]">
              {parsed.error}
            </div>
          )}

          {/* Simple OK for click/type/wait when no rich payload */}
          {!parsed.screenshot && !parsed.text && parsed.result === undefined && !parsed.error && (
            <div className="px-3 py-2 text-[10px] text-[var(--color-muted)] bg-[var(--color-surface-1)]">
              {isError ? 'Failed' : 'OK'}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── Per-tool arg types ────────────────────────────────────────────────────────

interface BrowserClickArgs      { selector?: string }
interface BrowserTypeArgs       { selector?: string; text?: string }
interface BrowserScreenshotArgs { selector?: string }
interface BrowserGetTextArgs    { selector?: string }
interface BrowserWaitArgs       { selector?: string; timeout_ms?: number }
interface BrowserEvaluateArgs   { expression?: string }

// ── Summary builders (one per tool kind) ──────────────────────────────────────

function clickSummary(args: BrowserClickArgs): string {
  return args.selector ?? '(no selector)'
}

function typeSummary(args: BrowserTypeArgs): string {
  if (!args.selector) return '(no selector)'
  return args.text ? `${args.selector} — "${args.text}"` : args.selector
}

function screenshotSummary(args: BrowserScreenshotArgs): string {
  return args.selector ?? 'full page'
}

function getTextSummary(args: BrowserGetTextArgs): string {
  return args.selector ?? '(no selector)'
}

function waitSummary(args: BrowserWaitArgs): string {
  if (!args.selector) return '(no selector)'
  return args.timeout_ms != null ? `${args.selector} (${args.timeout_ms}ms)` : args.selector
}

function evaluateSummary(args: BrowserEvaluateArgs): string {
  if (!args.expression) return '(no expression)'
  return args.expression.length > 60 ? args.expression.slice(0, 60) + '…' : args.expression
}

// ── Factory: create dot + underscore UI pair for one tool kind ────────────────

interface BrowserToolSpec<TArgs> {
  displayName: string   // canonical dot name shown in the UI (e.g. "browser.click")
  dotTool: string       // registered tool name with dot (e.g. "browser.click")
  underscoreTool: string // registered tool name with underscore (e.g. "browser_click")
  icon: typeof Globe
  summary: (args: TArgs) => string
}

function createBrowserToolUI<TArgs extends object>(
  spec: BrowserToolSpec<TArgs>
): { dotUI: ReturnType<typeof makeAssistantToolUI>; underscoreUI: ReturnType<typeof makeAssistantToolUI> } {
  function renderBlock(toolArgs: TArgs | undefined, result: unknown, status: { type: string }) {
    const args = toolArgs ?? ({} as TArgs)
    return (
      <BrowserToolBlock
        toolName={spec.displayName}
        icon={spec.icon}
        args={args as Record<string, unknown>}
        result={result}
        status={status}
        summary={spec.summary(args)}
      />
    )
  }

  const dotUI = makeAssistantToolUI<TArgs, unknown>({
    toolName: spec.dotTool,
    render: ({ args, result, status }) => renderBlock(args, result, status),
  })

  const underscoreUI = makeAssistantToolUI<TArgs, unknown>({
    toolName: spec.underscoreTool,
    render: ({ args, result, status }) => renderBlock(args, result, status),
  })

  return { dotUI, underscoreUI }
}

// ── Six exported tool UIs (dot + underscore variants) ────────────────────────

const click = createBrowserToolUI<BrowserClickArgs>({
  displayName: 'browser.click',
  dotTool: 'browser.click',
  underscoreTool: 'browser_click',
  icon: CursorClick,
  summary: clickSummary,
})
export const BrowserClickUI = click.dotUI
export const BrowserClickUnderscoreUI = click.underscoreUI

const type_ = createBrowserToolUI<BrowserTypeArgs>({
  displayName: 'browser.type',
  dotTool: 'browser.type',
  underscoreTool: 'browser_type',
  icon: Keyboard,
  summary: typeSummary,
})
export const BrowserTypeUI = type_.dotUI
export const BrowserTypeUnderscoreUI = type_.underscoreUI

const screenshot = createBrowserToolUI<BrowserScreenshotArgs>({
  displayName: 'browser.screenshot',
  dotTool: 'browser.screenshot',
  underscoreTool: 'browser_screenshot',
  icon: Camera,
  summary: screenshotSummary,
})
export const BrowserScreenshotUI = screenshot.dotUI
export const BrowserScreenshotUnderscoreUI = screenshot.underscoreUI

const getText = createBrowserToolUI<BrowserGetTextArgs>({
  displayName: 'browser.get_text',
  dotTool: 'browser.get_text',
  underscoreTool: 'browser_get_text',
  icon: TextT,
  summary: getTextSummary,
})
export const BrowserGetTextUI = getText.dotUI
export const BrowserGetTextUnderscoreUI = getText.underscoreUI

const wait = createBrowserToolUI<BrowserWaitArgs>({
  displayName: 'browser.wait',
  dotTool: 'browser.wait',
  underscoreTool: 'browser_wait',
  icon: Timer,
  summary: waitSummary,
})
export const BrowserWaitUI = wait.dotUI
export const BrowserWaitUnderscoreUI = wait.underscoreUI

const evaluate = createBrowserToolUI<BrowserEvaluateArgs>({
  displayName: 'browser.evaluate',
  dotTool: 'browser.evaluate',
  underscoreTool: 'browser_evaluate',
  icon: Code,
  summary: evaluateSummary,
})
export const BrowserEvaluateUI = evaluate.dotUI
export const BrowserEvaluateUnderscoreUI = evaluate.underscoreUI
