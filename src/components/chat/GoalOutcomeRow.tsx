// GoalOutcomeRow — the lasting "how did this goal end" line in the chat
// thread (founder decision 2026-09-14). See src/lib/goalOutcome.ts for the
// data source and the copy rules.
//
// Visual language: the thread's existing quiet system rows (the goal-ack line
// and browser-handover notice — centered, text-xs, surface-2 background) plus
// GoalSetupFailureLine's `<details>` expand pattern for the longer text. The
// icon carries the tone: Forge Gold for met, warning for not met, muted for a
// stop. Always rendered — it is NOT gated by Verbose chat.

import type { ReactNode } from 'react'
import { CaretDown, CheckCircle, StopCircle, WarningCircle } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { describeGoalOutcome, type GoalOutcome, type GoalOutcomeTone } from '@/lib/goalOutcome'

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
    <details
      data-testid="goal-outcome-line"
      data-goal-ending={outcome.ending}
      data-goal-tone={copy.tone}
      className="group w-full max-w-2xl rounded-lg bg-[var(--color-surface-2)] px-3 py-1.5 text-[length:var(--type-utility-xs-size)]"
    >
      <summary
        tabIndex={0}
        className="flex cursor-pointer list-none items-start gap-2 [&::-webkit-details-marker]:hidden"
        title="Show the full text"
      >
        <Icon
          size={14}
          weight="fill"
          className={cn('mt-px shrink-0', TONE_ICON_CLASS[copy.tone])}
          aria-hidden="true"
        />
        <span className="min-w-0 flex-1">
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
        </span>
        <CaretDown
          size={10}
          className="mt-1 shrink-0 text-[var(--color-muted)] transition-transform group-open:rotate-180"
          aria-hidden="true"
        />
      </summary>
      {copy.detail && (
        <p
          data-testid="goal-outcome-detail"
          className="ml-[22px] mt-1 whitespace-pre-wrap break-words text-[var(--color-secondary)]"
        >
          {copy.detail}
        </p>
      )}
    </details>
  )
}
