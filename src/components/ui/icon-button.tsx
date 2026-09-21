import * as React from 'react'

import { Button, type ButtonProps } from './button'
import { clsx } from 'clsx'

type AccessibleName =
  | { 'aria-label': string; 'aria-labelledby'?: never }
  | { 'aria-label'?: never; 'aria-labelledby': string }

export type IconButtonProps = Omit<ButtonProps, 'aria-label' | 'aria-labelledby' | 'size'> & AccessibleName & {
  size?: 'sm' | 'default' | 'lg'
}

const iconButtonSizes = { sm: 'h-8 w-8 p-0', default: 'h-9 w-9 p-0', lg: 'h-10 w-10 p-0' } as const

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
  if (accessibleName.trim().length === 0) throw new Error('IconButton accessible name must contain visible text')
  return (
    <Button
      ref={ref}
      size="icon"
      variant={variant}
      className={clsx(`${iconButtonSizes[size]}`, className)}
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
