// Generic browser tool UI — handles browser.click, browser.type,
// browser.screenshot, browser.get_text, browser.wait, browser.evaluate.
//
// Each of the six browser tools is registered twice: once with the canonical
// dot name (e.g. `browser.click`) and once with an underscore alias
// (`browser_click`) for runtimes that reject dots in tool names. Both variants
// share the same renderer.

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
    // SEC-25: click/type/wait/evaluate/get_text/navigate are classified as
    // untrusted (pkg/agent/prompt_guard.go) and the gateway sends us the
    // PromptGuard-sanitized string, not the tool's raw output — unwrap the
    // [UNTRUSTED_CONTENT] marker before treating this as JSON.
    const unwrapped = stripUntrustedContentWrapper(result)
    if (unwrapped === null) {
      // High-strictness install: the backend fully redacted this result.
      return { text: '(content withheld by security policy)' }
    }
    try {
      return JSON.parse(unwrapped) as GenericBrowserResult
    } catch (err) {
      if (RAW_STRING_RESULT_TOOLS.has(toolName)) {
        return { text: unwrapped }
      }
      console.warn(
        `[BrowserTool] ${toolName} returned non-JSON string result`,
        err,
        unwrapped.slice(0, 200),
      )
      return {
        error: `Malformed result (expected JSON): ${unwrapped.slice(0, 200)}`,
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
  args: Record<string, unknown>
  result: unknown
  status: { type: string; reason?: string }
  /**
   * Issue #617: the tool call's real error outcome, sourced from the store's
   * resolved ToolCall.status (threaded through by the live path's render
   * props and the replay path's caller — see BrowserToolReplayBlock and
   * ChatScreen.tsx). `status` above is still used for isRunning/isCancelled
   * — only the error derivation moved off `status.type === 'incomplete'`,
   * which can never be true for a finished call carrying a result.
   *
   * F5: required (nullable), not optional. An omitted optional prop is
   * `undefined` → falsy → renders success, silently reopening the exact bug
   * this field exists to fix. Making it `boolean | undefined` (required)
   * forces every call site to pass it explicitly — zero runtime change for
   * the two existing callers (BrowserToolReplayBlock, createBrowserToolUI's
   * renderBlock), both of which already do.
   */
  isError: boolean | undefined
  summary: string
}

// Flat text-line redesign (ticket "Tool components in chat", P2): the
// per-tool identity icon (CursorClick/Keyboard/Camera/…) is no longer
// rendered here — it was purely decorative alongside `toolName`, which
// already disambiguates the tool uniquely (e.g. "browser.click"). Status now
// lives only in the leading dot/spinner. The `icon` prop/field has been
// removed entirely from BrowserToolBlockProps, BrowserToolSpec, and the
// replay-dispatch table (BROWSER_TOOL_SPECS) below — it was fully dead
// plumbing (mirrors the same cleanup already made in FileWriteConfirm.tsx).
export function BrowserToolBlock({
  toolName,
  args,
  result,
  status,
  isError,
  summary,
}: BrowserToolBlockProps) {
  const [expanded, setExpanded] = useState(false)

  // Client-side render gate (issue #494): hides this row when
  // shouldRenderToolCall says so for the given tool name/args, unless the
  // user has opted into verbose chat. Must sit after every hook above and
  // before the JSX return (Rules of Hooks) — mirrors BashOutput.tsx's gate.
  // Covers both the live path (createBrowserToolUI below) and the replay
  // path (BrowserToolReplayBlock), since both funnel through this component.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  if (!shouldRenderToolCall(toolName, args, verboseChatEnabled, !!isError)) {
    return null
  }

  const isRunning = status.type === 'running'
  const isCancelled = isCancelledStatus(status)
  const hasResult = result != null
  const hasDetail = !isRunning && hasResult

  const parsed = parseResult(result, toolName)

  // Always resolves to a real config — a terminal state (completed, no
  // error/cancellation) with no result is still a real outcome and must
  // still show a status dot + label, not a silently blank indicator.
  const statusConfig: ToolBadgeStatusConfig = isRunning
    ? getToolBadgeStatusConfig('running', { size: 12 })
    : isCancelled
    ? getToolBadgeStatusConfig('cancelled', { size: 12, cancelledVariant: 'muted' })
    : isError
    ? getToolBadgeStatusConfig('error', { size: 12 })
    : getToolBadgeStatusConfig('success', { size: 12 })

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
    // Flat text-line design (ticket "Tool components in chat", P2): no
    // border, no surface fill, no rounded frame, no overflow-hidden — the
    // row is transparent on the thread.
    <div className="mt-2 text-xs font-mono">
      {/* Header — a row of composed controls (mirrors GenericToolCall.tsx):
          the expand/collapse toggle is its own button so "Watch live" can be a
          separate, independently clickable sibling rather than nested inside it. */}
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
          <span className="text-[var(--color-muted)] shrink-0">{toolName}</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[10px]">
            {summary}
          </span>
          <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}>
            {statusConfig.label}
          </span>
        </button>

        {/* "Watch live" is shown on every browser tool-call row, not only while
            the call is running: browser tools complete in well under a second,
            but the agent's browser session persists afterwards, so the viewer
            can open the live panel to watch/continue at any point. */}
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
          {/* Args row */}
          {Object.keys(args).length > 0 && (
            <div>
              <p className="text-[10px] text-[var(--color-muted)] mb-1 uppercase tracking-wider">Args</p>
              <pre className="text-[10px] font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all">
                {JSON.stringify(args, null, 2)}
              </pre>
            </div>
          )}

          {/* Screenshot indicator (image itself renders in the assistant reply bubble via the media frame). */}
          {parsed.screenshot && (
            <div className="flex items-center gap-1.5">
              <Camera size={11} className="text-[var(--color-muted)]" />
              <span className="text-[10px] text-[var(--color-muted)]">Screenshot captured</span>
            </div>
          )}

          {/* Text output (browser.get_text) */}
          {parsed.text && !parsed.screenshot && (
            <pre className="text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
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
            <div>
              <p className="text-[10px] text-[var(--color-muted)] mb-1 uppercase tracking-wider">Result</p>
              <pre className="text-[10px] font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-40 overflow-auto">
                {JSON.stringify(parsed.result, null, 2)}
              </pre>
            </div>
          )}

          {/* Error */}
          {parsed.error && (
            <div className="text-[var(--color-error)] text-[10px]">{parsed.error}</div>
          )}

          {/* Simple OK for click/type/wait when no rich payload */}
          {!parsed.screenshot && !parsed.text && parsed.result === undefined && !parsed.error && (
            <div className="text-[10px] text-[var(--color-muted)]">
              {isCancelled ? 'Cancelled' : isError ? 'Failed' : 'OK'}
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
  summary: (args: TArgs) => string
}

function createBrowserToolUI<TArgs extends object>(
  spec: BrowserToolSpec<TArgs>
): { dotUI: ReturnType<typeof makeAssistantToolUI>; underscoreUI: ReturnType<typeof makeAssistantToolUI> } {
  function renderBlock(
    toolArgs: TArgs | undefined,
    result: unknown,
    status: { type: string; reason?: string },
    isError: boolean | undefined,
  ) {
    const args = toolArgs ?? ({} as TArgs)
    return (
      <BrowserToolBlock
        toolName={spec.displayName}
        args={args as Record<string, unknown>}
        result={result}
        status={status}
        isError={isError}
        summary={spec.summary(args)}
      />
    )
  }

  // Issue #617: isError comes from the tool-call part's own `isError` field
  // (set in omnipus-runtime.ts from the store's resolved ToolCall.status),
  // not from `status.type === 'incomplete'`.
  const dotUI = makeAssistantToolUI<TArgs, unknown>({
    toolName: spec.dotTool,
    render: ({ args, result, status, isError }) => renderBlock(args, result, status, isError),
  })

  const underscoreUI = makeAssistantToolUI<TArgs, unknown>({
    toolName: spec.underscoreTool,
    render: ({ args, result, status, isError }) => renderBlock(args, result, status, isError),
  })

  return { dotUI, underscoreUI }
}

// ── Six exported tool UIs (dot + underscore variants) ────────────────────────

const click = createBrowserToolUI<BrowserClickArgs>({
  displayName: 'browser.click',
  dotTool: 'browser.click',
  underscoreTool: 'browser_click',
  summary: clickSummary,
})
export const BrowserClickUI = click.dotUI
export const BrowserClickUnderscoreUI = click.underscoreUI

const type_ = createBrowserToolUI<BrowserTypeArgs>({
  displayName: 'browser.type',
  dotTool: 'browser.type',
  underscoreTool: 'browser_type',
  summary: typeSummary,
})
export const BrowserTypeUI = type_.dotUI
export const BrowserTypeUnderscoreUI = type_.underscoreUI

const screenshot = createBrowserToolUI<BrowserScreenshotArgs>({
  displayName: 'browser.screenshot',
  dotTool: 'browser.screenshot',
  underscoreTool: 'browser_screenshot',
  summary: screenshotSummary,
})
export const BrowserScreenshotUI = screenshot.dotUI
export const BrowserScreenshotUnderscoreUI = screenshot.underscoreUI

const getText = createBrowserToolUI<BrowserGetTextArgs>({
  displayName: 'browser.get_text',
  dotTool: 'browser.get_text',
  underscoreTool: 'browser_get_text',
  summary: getTextSummary,
})
export const BrowserGetTextUI = getText.dotUI
export const BrowserGetTextUnderscoreUI = getText.underscoreUI

const wait = createBrowserToolUI<BrowserWaitArgs>({
  displayName: 'browser.wait',
  dotTool: 'browser.wait',
  underscoreTool: 'browser_wait',
  summary: waitSummary,
})
export const BrowserWaitUI = wait.dotUI
export const BrowserWaitUnderscoreUI = wait.underscoreUI

const evaluate = createBrowserToolUI<BrowserEvaluateArgs>({
  displayName: 'browser.evaluate',
  dotTool: 'browser.evaluate',
  underscoreTool: 'browser_evaluate',
  summary: evaluateSummary,
})
export const BrowserEvaluateUI = evaluate.dotUI
export const BrowserEvaluateUnderscoreUI = evaluate.underscoreUI

// ── Replay dispatch (B-fix) ────────────────────────────────────────────────
//
// The LIVE path dispatches a tool call to its registered UI by tool name via
// makeAssistantToolUI (AssistantUI's MessagePrimitive.Parts `components.tools`
// registry — see the six *UI exports above, wired in OmnipusRuntimeProvider).
// The REPLAY path (ChatScreen.tsx's VirtualAssistantMessageRow, used once a
// session has been reloaded from history and every message is "historical")
// renders OUTSIDE AssistantUI's message-part context, so that registry does
// not apply — it has its own hardcoded tool-call loop that, before this fix,
// only special-cased web_serve/serve_workspace/run_in_workspace and fell back
// to the generic raw-JSON GenericToolCall view for every other tool, INCLUDING
// all six browser.* / browser_* tools. This is why a reloaded browser_screenshot
// call showed a compact JSON blob instead of the parsed screenshot/text/error
// card the live view shows.
//
// This lookup + block let the replay renderer reuse the exact same
// BrowserToolBlock presentation (summary, parsed result, "Watch live"
// button) as the live path, keyed by either the canonical underscore name or
// the legacy dot alias (see the naming-convention comment in
// OmnipusRuntimeProvider.tsx).
const BROWSER_TOOL_SPECS: Record<
  string,
  { summary: (args: Record<string, unknown>) => string; displayName: string }
> = {
  'browser.click': { summary: (a) => clickSummary(a as BrowserClickArgs), displayName: 'browser.click' },
  'browser_click': { summary: (a) => clickSummary(a as BrowserClickArgs), displayName: 'browser.click' },
  'browser.type': { summary: (a) => typeSummary(a as BrowserTypeArgs), displayName: 'browser.type' },
  'browser_type': { summary: (a) => typeSummary(a as BrowserTypeArgs), displayName: 'browser.type' },
  'browser.screenshot': { summary: (a) => screenshotSummary(a as BrowserScreenshotArgs), displayName: 'browser.screenshot' },
  'browser_screenshot': { summary: (a) => screenshotSummary(a as BrowserScreenshotArgs), displayName: 'browser.screenshot' },
  'browser.get_text': { summary: (a) => getTextSummary(a as BrowserGetTextArgs), displayName: 'browser.get_text' },
  'browser_get_text': { summary: (a) => getTextSummary(a as BrowserGetTextArgs), displayName: 'browser.get_text' },
  'browser.wait': { summary: (a) => waitSummary(a as BrowserWaitArgs), displayName: 'browser.wait' },
  'browser_wait': { summary: (a) => waitSummary(a as BrowserWaitArgs), displayName: 'browser.wait' },
  'browser.evaluate': { summary: (a) => evaluateSummary(a as BrowserEvaluateArgs), displayName: 'browser.evaluate' },
  'browser_evaluate': { summary: (a) => evaluateSummary(a as BrowserEvaluateArgs), displayName: 'browser.evaluate' },
}

/** True when `toolName` is one of the six browser tools (dot or underscore form). */
export function isReplayBrowserToolName(toolName: string): boolean {
  return Object.prototype.hasOwnProperty.call(BROWSER_TOOL_SPECS, toolName)
}

/**
 * Replay-path renderer for a browser tool call — the same BrowserToolBlock
 * presentation the live path gets via makeAssistantToolUI, callable directly
 * (no AssistantUI context required) from VirtualAssistantMessageRow. Returns
 * null for an unrecognized tool name so callers can use it as a plain
 * component without a separate isReplayBrowserToolName guard if preferred.
 */
export function BrowserToolReplayBlock({
  toolName,
  args,
  result,
  status,
  isError,
}: {
  toolName: string
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  /** Issue #617: the tool call's real error outcome (tc.status === 'error'
   *  at the ChatScreen.tsx call site) — replaces the old `status.type ===
   *  'incomplete'` derivation, which is never true for a finished replayed
   *  call. F5: required (nullable) rather than optional — see
   *  BrowserToolBlockProps.isError's doc comment for why an optional prop
   *  here would silently reopen the bug this fixes. */
  isError: boolean | undefined
}) {
  const spec = BROWSER_TOOL_SPECS[toolName]
  if (!spec) return null
  const resolvedArgs = (args ?? {}) as Record<string, unknown>
  return (
    <BrowserToolBlock
      toolName={spec.displayName}
      args={resolvedArgs}
      result={result}
      status={status}
      isError={isError}
      summary={spec.summary(resolvedArgs)}
    />
  )
}
