import { ArrowsClockwise } from '@phosphor-icons/react'
import { clsx } from 'clsx'

import { formatTokens } from '@/lib/formatTokens'

export interface RunningIndicatorProps {
  /** Tokens consumed by the running task so far (human-formatted via formatTokens). */
  tokens: number
  /** While false the spinner holds still; callers render the chip only while the task runs. */
  streaming?: boolean
  className?: string
}

/**
 * RunningIndicator — the shared "this task is running" status chip (SP-41, FR-022):
 * a spinning Phosphor ArrowsClockwise icon next to the live token count, nothing else.
 *
 * One implementation for every surface that shows a running task — Board cards, List
 * rows, Graph nodes, the Plans band. Mirrors chat's own TokenCounter treatment
 * (src/components/chat/composer/TokenCounter.tsx: ArrowsClockwise + animate-spin while
 * streaming + token count) with the label shortened to "{n} tok" for the narrow
 * card/row widths (approved wireframe side-panel-wave3, SP-41/FR-022 — ONE treatment
 * everywhere). Status display, not a control.
 *
 * Reduced motion slows the spin to 3.4s instead of stopping it — a still icon would
 * read as a finished task (wireframe's declared reduced-motion treatment).
 */
export function RunningIndicator({ tokens, streaming = true, className }: RunningIndicatorProps) {
  const count = formatTokens(tokens)
  return (
    <span
      role="status"
      title="Running"
      aria-label={`${count} tokens`}
      className={clsx(
        'inline-flex items-center gap-[var(--space-1)] whitespace-nowrap font-bold text-[length:var(--type-utility-xs-size)] text-[var(--color-accent)]',
        className,
      )}
    >
      <ArrowsClockwise
        size={11}
        weight="bold"
        aria-hidden="true"
        className={clsx('shrink-0', streaming && 'animate-spin motion-reduce:[animation-duration:3.4s]')}
      />
      <span className="font-mono tabular-nums">{count} tok</span>
    </span>
  )
}
