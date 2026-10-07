import type { ReactNode } from 'react'
import { RadioGroup, RadioGroupItem, type RadioGroupProps } from './radio-group'
import { cn } from '@/lib/utils'

export interface ViewSwitchOption<T extends string = string> {
  value: T
  label: string
  icon?: ReactNode
  disabled?: boolean
  testId?: string
}

export type ViewSwitchProps<T extends string = string> = Pick<RadioGroupProps, 'className' | 'disabled'> & (
  | { 'aria-label': string; 'aria-labelledby'?: never }
  | { 'aria-label'?: never; 'aria-labelledby': string }
) & {
  value: T
  onValueChange: (value: T) => void
  options: readonly ViewSwitchOption<T>[]
}

/** Flat, gold-active, exactly-one-selected view control.
 * Uses the existing RadioGroup's arrow/Home/End and mandatory-selection
 * contract instead of a ToggleGroup that permits deselecting the only view.
 */
export function ViewSwitch<T extends string>({ value, onValueChange, options, className, disabled, ...name }: ViewSwitchProps<T>) {
  const accessibleName = name['aria-label'] !== undefined
    ? { 'aria-label': name['aria-label'] }
    : { 'aria-labelledby': name['aria-labelledby'] ?? '' }
  return (
    <RadioGroup {...accessibleName} data-slot="view-switch" value={value} onValueChange={(next) => onValueChange(next as T)} disabled={disabled}
      className={cn('shrink-0 gap-[var(--space-3)]', className)}>
      {options.map((option) => (
        <RadioGroupItem key={option.value} value={option.value} disabled={option.disabled} data-testid={option.testId}
          className={cn(
            'h-auto w-auto justify-start border-0 bg-transparent gap-[var(--space-1)] p-0 text-[length:var(--type-body-compact-size)] font-medium transition-colors motion-reduce:transition-none pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]',
            value === option.value
              ? 'text-[var(--color-accent)] hover:bg-transparent hover:text-[var(--color-accent)] forced-colors:text-[Highlight]'
              : 'text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)] forced-colors:text-[ButtonText]',
          )}>
          {option.icon}{option.label}
        </RadioGroupItem>
      ))}
    </RadioGroup>
  )
}
