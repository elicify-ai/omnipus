import * as React from 'react'
import { Button, type ButtonProps } from './button'
import { cn } from '@/lib/utils'

/**
 * TextToggle — a labelled on/off button for a cramped row.
 *
 * This is not a sliding switch. The word is always visible. Off is plain muted
 * text. On is the same word on a quiet surface fill with accent text — the
 * same selected treatment as SegmentedControlItem. A screen reader hears
 * "pressed" or "not pressed" from aria-pressed.
 *
 * The control grows to the touch minimum on a coarse pointer, in its own box,
 * so the shared hit-area expansion does not cover the control beside it.
 */
export type TextToggleProps = Omit<ButtonProps, 'onClick' | 'aria-pressed' | 'children'> & {
  pressed: boolean
  onPressedChange: (next: boolean) => void
  children: React.ReactNode
}

const TextToggle = React.forwardRef<HTMLButtonElement, TextToggleProps>(
  ({ pressed, onPressedChange, disabled, className, children, ...props }, ref) => (
    <Button
      ref={ref}
      type="button"
      variant="ghost"
      size="sm"
      aria-pressed={pressed}
      data-state={pressed ? 'on' : 'off'}
      data-ds-action=""
      disabled={disabled}
      onClick={() => {
        if (!disabled) onPressedChange(!pressed)
      }}
      className={cn(
        'h-7 min-w-0 rounded px-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-medium',
        'pointer-coarse:h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]',
        pressed
          ? 'bg-[var(--color-surface-3)] text-[var(--color-accent)] shadow-sm hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)] forced-colors:bg-[Highlight] forced-colors:text-[HighlightText]'
          : 'bg-transparent text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)] forced-colors:bg-[Canvas] forced-colors:text-[CanvasText]',
        className,
      )}
      {...props}
    >
      {children}
    </Button>
  ),
)
TextToggle.displayName = 'TextToggle'

export { TextToggle }
