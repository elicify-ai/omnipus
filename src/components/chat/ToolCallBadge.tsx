import { useState } from 'react'
import type { ToolCall } from '@/lib/api'
import type { MarshalErrorResult } from '@/lib/ws'
import { cn } from '@/lib/utils'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { detectToolResultSentinels } from './tools/toolResultSentinels'
import { SetGoalCardBlock, partStatusFromToolCallStatus } from './tools/SetGoalToolUI'

interface ToolCallBadgeProps {
  toolCall: ToolCall & { call_id: string }
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

export function ToolCallBadge({ toolCall }: ToolCallBadgeProps) {
  const [expanded, setExpanded] = useState(false)

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background infra calls (ToolSearch, background-bash dispatch/poll/read)
  // unless the user has opted into verbose chat, via shouldRenderToolCall —
  // this component's ONE caller is MessageItem's historical/live list
  // (ADR-091 D10 deleted SubagentBlock, this badge's other former caller,
  // along with the ActivityPanel step list it fed — see toolVisibility.ts's
  // header comment). shouldRenderToolCall's error/marshal-failure override
  // is per-tool-class (see that function's doc comment): ToolSearch still
  // forces visible on error, delegate does not consult isError at all
  // (ADR-091 D7/AC-7: the `run` action is already visible unconditionally),
  // and background-bash does not either (that failure is left to the
  // calling agent's own response text). Must sit after every hook above and
  // before the JSX return (Rules of Hooks).
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const marshalErr = isMarshalErrorResult(toolCall.result)
  // F1: the three structured-failure sentinels (delegation-denied,
  // file-exists refusal, permission-denied) are detected by the ONE shared
  // module (./tools/toolResultSentinels) GenericToolCall.tsx also uses —
  // previously this component carried its own byte-identical copy of all
  // three detectors plus their amber statusConfig branches. Detected here so
  // this badge renders the SAME amber "Delegation denied · <axis>" /
  // "File already exists" / "Permission denied" chip GenericToolCall's
  // live/replay path does, instead of a generic red "Failed".
  const sentinels = detectToolResultSentinels(toolCall.result)
  // ADR-082 D9 review S11: a `set_goal` call in this historical list used to
  // fall into shouldRenderToolCall's hide-by-default `set_goal` case and
  // vanish. Route it to the same dedicated UI the top-level thread uses: the
  // record card on success, the quiet "Goal registration failed" line on the
  // refusal. Verbose chat falls through to this badge's own raw rendering
  // below (shouldRenderToolCall returns true there), matching
  // SetGoalCardBlock's own verbose contract.
  if (toolCall.tool === 'set_goal' && !verboseChatEnabled) {
    return (
      <SetGoalCardBlock
        args={toolCall.params}
        result={toolCall.result}
        status={partStatusFromToolCallStatus(toolCall.status)}
        isRunning={toolCall.status === 'running'}
        isError={toolCall.status === 'error' || marshalErr || sentinels.any}
        error={toolCall.error}
        durationMs={toolCall.duration_ms}
      />
    )
  }
  const isVisible = shouldRenderToolCall(
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
    <div data-testid="tool-call-badge" data-tool={toolCall.tool} className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
      <div className="flex w-full items-center gap-[var(--space-2)]">
        {/* Toggle button. Mirrors GenericToolCall.tsx's `disabled={!hasDetail}`
            gate: while running, there is nothing to expand — a focusable
            button whose Enter/Space no-ops while still announcing
            aria-expanded is an inert-focusable trap for keyboard/AT users.
            Disabling natively removes it from the tab order and drops
            aria-expanded entirely (rather than leaving it stuck at `false`,
            which would falsely announce "collapsible, currently collapsed"
            for a row that can never actually expand yet). */}
        <DisclosureRow
          expanded={expanded}
          onExpandedChange={setExpanded}
          expandable={!isRunning}
          data-testid="tool-call-toggle"
        >
          {config.indicator}
          <span className="text-[var(--color-secondary)] font-medium">
            {humanizeToolName(toolCall.tool)}
          </span>
          <span className={cn('text-[var(--color-muted)]')}>{config.label}</span>
        </DisclosureRow>
      </div>

      {/* Expanded detail — indented quote-block: a thin left accent line
          stands in for the old bordered panel, aligned under the status-dot
          column instead of boxing the whole row. */}
      {expanded && !isRunning && (
        <div className="ml-[var(--space-1)] space-y-[var(--space-2)] border-l-2 border-[var(--color-border)] py-[var(--space-1)] pl-[var(--space-2-5)]">
          <div>
            <div className="text-[var(--color-muted)] mb-[var(--space-1)]">Tool</div>
            <code className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] break-all">
              {toolCall.tool}
            </code>
          </div>
          <div>
            <div className="text-[var(--color-muted)] mb-[var(--space-1)]">Parameters</div>
            {/* Fix 7 (2026-07-16): capped like the Result pane below — params
                are now retained post-completion (see chat.ts's params-survive
                -merge fix), so an uncapped write_file/edit content param can
                render arbitrarily tall. */}
            <pre className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
              {JSON.stringify(toolCall.params, null, 2)}
            </pre>
          </div>
          {toolCall.result !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-[var(--space-1)]">Result</div>
              {/* Keyboard-scrollable: WebKit doesn't put a plain scrollable
                  <pre> in the Tab order by default, so a keyboard-only user
                  can't reach/scroll it at all. tabIndex + role="region" +
                  label make it a reachable, scrollable landmark. */}
              <pre
                tabIndex={0}
                role="region"
                aria-label="Tool output"
                className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
              >
                {typeof toolCall.result === 'string'
                  ? toolCall.result
                  : JSON.stringify(toolCall.result, null, 2)}
              </pre>
            </div>
          )}
          {toolCall.error && (
            <div className="text-[var(--color-error)] text-[length:var(--type-caption-size)]">{toolCall.error}</div>
          )}
        </div>
      )}
    </div>
  )
}
