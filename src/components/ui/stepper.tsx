import { cn } from '@/lib/utils'

// Stepper — the shared wizard step indicator (spec "Design-System
// Components": an ordered list of step labels, `aria-current="step"` on the
// current step, and a visually hidden "Step n of m" next to the list).
// Presentational only: labels and the current index, no navigation logic.
// The onboarding wizard's hand-built indicator is not migrated here (B1 scope).

export interface StepperProps {
  /** Step labels in display order. */
  steps: string[]
  /** Zero-based index of the current step. */
  current: number
  /** Accessible name for the list ("Export steps", "Import steps"). */
  label?: string
  className?: string
}

export function Stepper({ steps, current, label, className }: StepperProps) {
  return (
    <div data-slot="stepper" className={cn('flex flex-col gap-[var(--space-1)]', className)}>
      <ol
        aria-label={label}
        className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)]"
      >
        {steps.map((step, index) => {
          const isCurrent = index === current
          const isDone = index < current
          return (
            <li
              key={step}
              aria-current={isCurrent ? 'step' : undefined}
              data-state={isCurrent ? 'current' : isDone ? 'done' : 'upcoming'}
              className={cn(
                'rounded-full border px-[var(--space-2-5)] py-[var(--space-0-5)] forced-colors:border-[CanvasText]',
                isCurrent
                  ? 'border-[var(--color-accent)] font-semibold text-[var(--color-accent)]'
                  : isDone
                    ? 'border-[var(--color-border)] text-[var(--color-secondary)]'
                    : 'border-[var(--color-border)] text-[var(--color-muted)]',
              )}
            >
              {step}
            </li>
          )
        })}
      </ol>
      <span className="sr-only">{`Step ${current + 1} of ${steps.length}`}</span>
    </div>
  )
}
