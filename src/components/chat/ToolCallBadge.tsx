import { useState } from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import type { ToolCall } from '@/lib/api'
import type { MarshalErrorResult } from '@/lib/ws'
import { cn } from '@/lib/utils'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, statusDot, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { isDelegationFailure, policyAxisLabel } from './tools/GenericToolCall'

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
  // background infra calls (load_tool, background delegate/bash dispatch,
  // status polls) unless the user has opted into verbose chat. Covers all
  // call sites of this shared badge (MessageItem's historical list,
  // SubagentBlock's nested steps, and ActivityPanel's expanded native-agent
  // step rows). An error/marshal-failure outcome always overrides the hide
  // decision — a failed/denied call, or one whose result silently failed to
  // marshal, must never disappear just because its params look like ordinary
  // background dispatch. Must sit after every hook above and before the JSX
  // return (Rules of Hooks).
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const marshalErr = isMarshalErrorResult(toolCall.result)
  // Ported from GenericToolCall.tsx (G17/BLOCKER 2): a denied delegation is
  // an error-status result carrying the structured DelegationFailure
  // sentinel as its `result`. Detected here too so the historical-list /
  // SubagentBlock-step badge (this component) renders the SAME amber
  // "Delegation denied · <axis>" chip GenericToolCall's live/replay path
  // does, instead of a generic red "Failed".
  const delegationFailure = isDelegationFailure(toolCall.result) ? toolCall.result : null
  if (
    !shouldRenderToolCall(
      toolCall.tool,
      toolCall.params,
      verboseChatEnabled,
      toolCall.status === 'error' || marshalErr || !!delegationFailure,
    )
  ) {
    return null
  }

  const config: ToolBadgeStatusConfig = delegationFailure
    ? {
        // No equivalent status in getToolBadgeStatusConfig's 4-value domain
        // (see toolStatusConfig.tsx's file header) — reuses the shared
        // `statusDot` helper so its dot matches the other four exactly,
        // mirroring GenericToolCall's own local delegationFailure branch.
        indicator: statusDot('bg-[var(--color-warning)]'),
        label: `Delegation denied · ${policyAxisLabel(delegationFailure.policy)}`,
      }
    : getToolBadgeStatusConfig(toolCall.status, { durationMs: toolCall.duration_ms })
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
            <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all">
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
