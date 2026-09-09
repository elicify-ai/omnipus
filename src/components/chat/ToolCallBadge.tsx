import { useState } from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import type { ToolCall } from '@/lib/api'
import type { MarshalErrorResult } from '@/lib/ws'
import { cn } from '@/lib/utils'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall, shouldRenderToolCallInPanel } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { detectToolResultSentinels } from './tools/toolResultSentinels'

interface ToolCallBadgeProps {
  toolCall: ToolCall & { call_id: string }
  /**
   * Which chat surface is rendering this badge — selects the visibility
   * policy (Fix 2, user-approved 2026-07-16). Defaults to 'thread': the
   * hidden-by-default noisy-infra set (ToolSearch, background delegate/bash
   * dispatch, status polls) via shouldRenderToolCall — used by MessageItem's
   * historical list and SubagentBlock's nested steps. 'panel' swaps in
   * shouldRenderToolCallInPanel — ActivityPanel is the designated home for
   * that same noisy detail, so its default INVERTS to show everything except
   * `ToolSearch`. Kept as a prop switch (not a second component) so the two
   * policies never leak into each other's call sites.
   */
  surface?: 'thread' | 'panel'
}

/**
 * Returns true when the result is the marshal-error sentinel from replay.go.
 * Mirrors GenericToolCall.tsx's detector of the same name — the backend emits
 * `{_marshal_error: "..."}` when JSON-marshaling a tool result fails during
 * replay-frame construction, which can happen even when the call itself
 * succeeded (i.e. `toolCall.status` doesn't reflect it).
 */
function isMarshalErrorResult(value: unknown): value is MarshalErrorResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>)['_marshal_error'] === 'string'
  )
}

export function ToolCallBadge({ toolCall, surface = 'thread' }: ToolCallBadgeProps) {
  const [expanded, setExpanded] = useState(false)

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background infra calls (ToolSearch, background delegate/bash dispatch,
  // status polls) unless the user has opted into verbose chat. surface=
  // 'thread' (MessageItem's historical list, SubagentBlock's nested steps)
  // uses shouldRenderToolCall, whose error/marshal-failure override is now
  // per-tool-class (see that function's doc comment) — ToolSearch still
  // forces visible on error, but delegate/background-bash do NOT (that
  // failure is left to the calling agent's own response text). surface=
  // 'panel' (ActivityPanel's expanded native-agent step rows) uses
  // shouldRenderToolCallInPanel instead — an inverted, outcome-blind policy
  // that shows everything except ToolSearch, since the panel is the
  // designated transparency surface for exactly what the thread hides,
  // failures included. That transparency is scoped to steps belonging to a
  // span that made it into the panel at all (running or retained in
  // recentlyFinished) — a top-level delegation denied outright (no span ever
  // opens) never reaches this component via 'panel' either; see
  // toolVisibility.ts's shouldRenderToolCall doc comment for that gap. Must
  // sit after every hook above and before the JSX return (Rules of Hooks).
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const marshalErr = isMarshalErrorResult(toolCall.result)
  // F1: the three structured-failure sentinels (delegation-denied,
  // file-exists refusal, permission-denied) are detected by the ONE shared
  // module (./tools/toolResultSentinels) GenericToolCall.tsx also uses —
  // previously this component carried its own byte-identical copy of all
  // three detectors plus their amber statusConfig branches. Detected here so
  // the historical-list / SubagentBlock-step badge (this component) renders
  // the SAME amber "Delegation denied · <axis>" / "File already exists" /
  // "Permission denied" chip GenericToolCall's live/replay path does,
  // instead of a generic red "Failed" — this surface is the one that most
  // needed it: ToolCallBadge renders SubagentBlock's nested steps, a
  // DELEGATED worker's tool calls, which is exactly where a
  // filesystem-scope or tool-policy denial reaches the model.
  const sentinels = detectToolResultSentinels(toolCall.result)
  const isVisible =
    surface === 'panel'
      ? shouldRenderToolCallInPanel(toolCall.tool, verboseChatEnabled)
      : shouldRenderToolCall(
          toolCall.tool,
          toolCall.params,
          verboseChatEnabled,
          toolCall.status === 'error' || marshalErr || sentinels.any,
        )
  if (!isVisible) {
    return null
  }

  // F1: sentinel detection sits ABOVE all four ordinary statuses
  // (running/success/error/cancelled) — a sentinel result always wins here,
  // unlike GenericToolCall.tsx which checks isRunning/isCancelled FIRST (see
  // that file's own precedence comment). This is the two callers'
  // deliberately different precedence the shared module's doc comment
  // describes — do not "fix" this to match GenericToolCall's ordering.
  const config: ToolBadgeStatusConfig =
    sentinels.statusConfig ?? getToolBadgeStatusConfig(toolCall.status, { durationMs: toolCall.duration_ms })
  const isRunning = toolCall.status === 'running'

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no
    // border, no surface fill, no rounded frame, no overflow-hidden — the
    // row is transparent on the thread. Separation comes from `mt-2`
    // spacing and the status dot, not a card frame.
    <div data-testid="tool-call-badge" data-tool={toolCall.tool} className="mt-2 text-xs font-mono">
      <div className="flex w-full items-center gap-2">
        {/* Toggle button. Mirrors GenericToolCall.tsx's `disabled={!hasDetail}`
            gate: while running, there is nothing to expand — a focusable
            button whose Enter/Space no-ops while still announcing
            aria-expanded is an inert-focusable trap for keyboard/AT users.
            Disabling natively removes it from the tab order and drops
            aria-expanded entirely (rather than leaving it stuck at `false`,
            which would falsely announce "collapsible, currently collapsed"
            for a row that can never actually expand yet). */}
        <button tabIndex={0}
          type="button"
          onClick={() => !isRunning && setExpanded((e) => !e)}
          disabled={isRunning}
          className={cn(
            'flex flex-1 min-w-0 items-center gap-2 py-1 text-left transition-colors',
            !isRunning && 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer',
            isRunning && 'cursor-default'
          )}
          aria-expanded={!isRunning ? expanded : undefined}
        >
          {config.indicator}
          <span className="text-[var(--color-secondary)] font-medium">
            {humanizeToolName(toolCall.tool)}
          </span>
          <span className={cn('text-[var(--color-muted)]', config.textClass)}>{config.label}</span>
          {/* Caret lives inside the toggle button (not a split-out sibling
              control) — there is no other independently-clickable action on
              this row to justify splitting the row, so the whole row stays
              one click target (mirrors BashOutput.tsx). */}
          {!isRunning && (
            <span className="ml-auto shrink-0 text-[var(--color-muted)]">
              {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
            </span>
          )}
        </button>
      </div>

      {/* Expanded detail — indented quote-block: a thin left accent line
          stands in for the old bordered panel, aligned under the status-dot
          column instead of boxing the whole row. */}
      {expanded && !isRunning && (
        <div className="ml-[3px] space-y-2 border-l-2 border-[var(--color-border)] py-1 pl-3">
          <div>
            <div className="text-[var(--color-muted)] mb-1">Tool</div>
            <code className="text-[10px] text-[var(--color-secondary)] break-all">
              {toolCall.tool}
            </code>
          </div>
          <div>
            <div className="text-[var(--color-muted)] mb-1">Parameters</div>
            {/* Fix 7 (2026-07-16): capped like the Result pane below — params
                are now retained post-completion (see chat.ts's params-survive
                -merge fix), so an uncapped write_file/edit content param can
                render arbitrarily tall. */}
            <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
              {JSON.stringify(toolCall.params, null, 2)}
            </pre>
          </div>
          {toolCall.result !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-1">Result</div>
              {/* Keyboard-scrollable: WebKit doesn't put a plain scrollable
                  <pre> in the Tab order by default, so a keyboard-only user
                  can't reach/scroll it at all. tabIndex + role="region" +
                  label make it a reachable, scrollable landmark. */}
              <pre
                tabIndex={0}
                role="region"
                aria-label="Tool output"
                className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
              >
                {typeof toolCall.result === 'string'
                  ? toolCall.result
                  : JSON.stringify(toolCall.result, null, 2)}
              </pre>
            </div>
          )}
          {toolCall.error && (
            <div className="text-[var(--color-error)] text-[10px]">{toolCall.error}</div>
          )}
        </div>
      )}
    </div>
  )
}
