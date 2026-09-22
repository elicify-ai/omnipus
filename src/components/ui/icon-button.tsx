import * as React from 'react'

import { Button, type ButtonProps } from './button'
import { cn } from '@/lib/utils'

type AccessibleName =
  | { 'aria-label': string; 'aria-labelledby'?: never }
  | { 'aria-label'?: never; 'aria-labelledby': string }

export type IconButtonProps = Omit<ButtonProps, 'aria-label' | 'aria-labelledby' | 'size'> & AccessibleName & {
  size?: 'sm' | 'default' | 'lg'
}

const iconButtonSizes = { sm: 'h-8 w-8 p-0', default: 'h-9 w-9 p-0', lg: 'h-10 w-10 p-0' } as const

// Thrown (never a raw TypeError from a missing property) when a caller
// bypasses the AccessibleName union at runtime — e.g. spreading untyped
// props — without supplying either accessible-name mechanism. Internal only:
// no consumer outside this module and its test catches this specific class
// (unlike e.g. `buttonVariants`, which is a genuine public export of
// button.tsx) — callers only need `Error.name`/`message`, so it is not
// re-exported from the barrel or the design-system catalog.
class IconButtonAccessibleNameError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'IconButtonAccessibleNameError'
  }
}

// Defaults to `ghost`, NOT to Button's own `default`. An icon-only control is
// a toolbar affordance — close, remove, zoom, expand — essentially never the
// primary call to action on a screen. Inheriting Button's default painted
// every variant-less IconButton as a solid Forge Gold block with a near-black
// glyph, which on the dark shell reads as a bright tile that only resolves
// into an icon on hover. That is not a hypothetical: it shipped across 16
// Library call sites in C2 wave 1 and the founder spotted it in the running
// app. A caller that genuinely wants a filled icon button still asks for
// `variant="default"` explicitly.
const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(({ size = 'default', variant = 'ghost', className, children, 'aria-label': ariaLabel, 'aria-labelledby': ariaLabelledBy, ...buttonProps }, ref) => {
  const accessibleName = ariaLabel ?? ariaLabelledBy
  if (accessibleName === undefined) {
    throw new IconButtonAccessibleNameError('IconButton requires an aria-label or aria-labelledby prop')
  }
  if (accessibleName.trim().length === 0) {
    throw new IconButtonAccessibleNameError('IconButton accessible name must contain visible text')
  }
  return (
    <Button
      ref={ref}
      size="icon"
      variant={variant}
      className={cn(iconButtonSizes[size], className)}
      {...buttonProps}
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
    >
      {children}
    </Button>
  )
})
IconButton.displayName = 'IconButton'

export { IconButton }
