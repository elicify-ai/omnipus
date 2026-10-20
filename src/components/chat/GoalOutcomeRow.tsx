// The lasting goal-ending line. Copy comes from src/lib/goalOutcome.ts;
// native disclosure behaviour is shared with helper events (spec §19–20).
import type { ReactNode } from 'react'
import { CheckCircle, StopCircle, WarningCircle } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { describeGoalOutcome, goalOutcomeHeadline, type GoalOutcome, type GoalOutcomeTone } from '@/lib/goalOutcome'
import { OutcomeDisclosure } from './OutcomeDisclosure'

const TONE_ICON = {
  met: CheckCircle,
  not_met: WarningCircle,
  stopped: StopCircle,
} as const

const TONE_ICON_CLASS: Record<GoalOutcomeTone, string> = {
  met: 'text-[var(--color-accent)]',
  not_met: 'text-[var(--color-warning)]',
  stopped: 'text-[var(--color-muted)]',
}

export interface GoalOutcomeRowProps {
  outcome: GoalOutcome
}

export function GoalOutcomeRow({ outcome }: GoalOutcomeRowProps): ReactNode {
  const copy = describeGoalOutcome(outcome)
  const Icon = TONE_ICON[copy.tone]
  return (
    <OutcomeDisclosure
      label={goalOutcomeHeadline(outcome)}
      data-testid="goal-outcome-line"
      data-goal-ending={outcome.ending}
      data-goal-tone={copy.tone}
      icon={
        <Icon
          size={14}
          weight="fill"
          className={cn('mt-[var(--border-width-hairline)] shrink-0', TONE_ICON_CLASS[copy.tone])}
          aria-hidden="true"
        />
      }
      detail={copy.detail && (
        <p
          data-testid="goal-outcome-detail"
          className="ml-[var(--space-4)] mt-[var(--space-1)] whitespace-pre-wrap break-words text-[var(--color-secondary)]"
        >
          {copy.detail}
        </p>
      )}
    >
      <span
        data-testid="goal-outcome-headline"
        className="block truncate group-open:whitespace-normal group-open:break-words"
      >
        <span className="font-medium text-[var(--color-secondary)]">{copy.label}</span>
        <span className="text-[var(--color-muted)]"> — {outcome.goal_text}</span>
      </span>
      {copy.summary && (
        <span
          data-testid="goal-outcome-summary"
          className="block truncate text-[var(--color-muted)] group-open:whitespace-pre-wrap group-open:break-words"
        >
          {copy.summary}
        </span>
      )}
    </OutcomeDisclosure>
  )
}
