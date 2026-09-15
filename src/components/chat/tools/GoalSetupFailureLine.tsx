// GoalSetupFailureLine — operator-reported UX fix, 2026-09-08.
//
// Repro: while a goal was active and its record still empty (the ADR-081
// D1 window between `/goal` activation and the working agent's first
// `set_goal` call), a tool call the agent needed for that setup — the
// reported case was `ask_user_question`, rejected by argument validation —
// FAILED, and the user saw nothing at all: just the generic rotating
// thinking indicator, for 17 minutes in the reported case, while the agent
// was actually off doing something else entirely.
//
// This renders ONE quiet line at that exact point — mirroring
// SetGoalToolUI.tsx's own `<details>`-based "failed" treatment byte-for-
// byte in structure (same summary/disclosure shape, same muted styling) —
// with the raw error/result text available on expand. Scope is
// deliberately narrow: ChatScreen.tsx only mounts this in place of the
// ordinary failed-call rendering (GenericToolCall / FallbackToolUI) when
// BOTH (a) the call would otherwise render visibly at all (respects the
// existing toolVisibility.ts hidden-tool contract — a failed background
// bash/delegate call stays hidden exactly as documented there, this
// component is never reached for those) and (b) isGoalRecordEmpty(goalStatus)
// is true. Outside that narrow window, general tool-error rendering is
// completely unchanged.

import type { ReactNode } from 'react'
import { Warning } from '@phosphor-icons/react'

/** Renders the failure/chip detail text: the explicit error when the store
 * carries one, else the result verbatim. Mirrors SetGoalToolUI.tsx's own
 * private `detailText` helper (duplicated here rather than imported/
 * exported across files — both are small, independent, and this keeps
 * SetGoalToolUI.tsx's failed-call treatment free of a goal-setup-specific
 * import it has no other reason to carry). */
function detailText(result: unknown, error: string | undefined): string {
  if (error && error.trim() !== '') return error
  if (typeof result === 'string') return result
  if (result === null || result === undefined) return ''
  try {
    return JSON.stringify(result, null, 2)
  } catch {
    return String(result)
  }
}

/**
 * The quiet summary line's copy, adapted per tool name. `ask_user_question`
 * is the reported repro's own failing call, so it gets its own specific
 * wording; every other tool that could plausibly fail during this same
 * empty-record window (e.g. `set_goal` itself is excluded by the caller —
 * it already has its own dedicated failed treatment) gets a generic
 * fallback that still names the concrete situation ("during goal setup")
 * rather than a bare "something went wrong".
 */
export function goalSetupFailureLineText(toolName: string): string {
  if (toolName === 'ask_user_question') {
    return 'A clarifying question could not be sent — retrying.'
  }
  return 'A step could not be completed during goal setup — retrying.'
}

export interface GoalSetupFailureLineProps {
  toolName: string
  result: unknown
  error?: string
}

/** Quiet, expandable failure trace — see the file doc comment. Exported as
 * a plain component (not a `makeAssistantToolUI` registration) since it is
 * mounted by ChatScreen.tsx's own dispatch, not by tool name. */
export function GoalSetupFailureLine({ toolName, result, error }: GoalSetupFailureLineProps): ReactNode {
  const detail = detailText(result, error)
  const summaryText = goalSetupFailureLineText(toolName)
  return (
    <details data-testid="goal-setup-failure-line" className="my-1 text-xs font-mono">
      <summary
        tabIndex={0}
        className="flex cursor-pointer list-none items-center gap-1.5 py-0.5 text-[var(--color-muted)]"
        title={detail || summaryText}
      >
        <Warning size={12} weight="fill" className="shrink-0 text-[var(--color-error)]" aria-hidden="true" />
        <span>{summaryText}</span>
      </summary>
      {detail && (
        <pre
          data-testid="goal-setup-failure-line-detail"
          className="ml-[3px] mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-all border-l-2 border-[var(--color-border)] py-1 pl-3 text-[10px] text-[var(--color-secondary)]"
        >
          {detail}
        </pre>
      )}
    </details>
  )
}
