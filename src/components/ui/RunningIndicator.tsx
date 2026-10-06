import { ArrowsClockwise } from '@phosphor-icons/react'
import { clsx } from 'clsx'

import { formatTokens } from '@/lib/formatTokens'

export interface RunningIndicatorProps {
  /** Optional known token count. Omit for spinner-only task/plan indicators
   * (founder decisions PI1/PI3, 2026-10-05). An explicit zero remains a count. */
  tokens?: number
  /** While false the spinner holds still; callers render the chip only while the task runs. */
  streaming?: boolean
  className?: string
}

/**
 * RunningIndicator — the shared "this task is running" status chip (SP-41, FR-022):
 * a spinning Phosphor ArrowsClockwise icon, plus a known token count when
 * supplied — nothing else.
 *
 * One implementation for every surface that shows a running task — Board cards, List
 * rows, Graph nodes, the Plans band. Mirrors chat's own TokenCounter treatment
 * (src/components/chat/composer/TokenCounter.tsx: ArrowsClockwise + animate-spin while
 * streaming + token count) with the label shortened to "{n} tok" for the narrow
 * card/row widths (approved wireframe side-panel-wave3, SP-41/FR-022 — ONE treatment
 * everywhere). Status display, not a control.
 *
 * Reduced motion keeps the arrow visible without animation. Its Running status
 * remains available to assistive technology; normal motion uses the shared spin.
 */
export function RunningIndicator({ tokens, streaming = true, className }: RunningIndicatorProps) {
  const hasCount = typeof tokens === 'number'
  return (
    <span
      role="status"
      title="Running"
      aria-label={hasCount ? `${formatTokens(tokens)} tokens` : 'Running'}
      className={clsx(
        'inline-flex items-center gap-[var(--space-1)] whitespace-nowrap font-bold text-[length:var(--type-utility-xs-size)] text-[var(--color-accent)]',
        className,
      )}
    >
      <ArrowsClockwise
        size={11}
        weight="bold"
        aria-hidden="true"
        className={clsx('shrink-0', streaming && 'animate-spin motion-reduce:animate-none')}
      />
      {hasCount && <span className="font-mono tabular-nums">{formatTokens(tokens)} tok</span>}
    </span>
  )
}
