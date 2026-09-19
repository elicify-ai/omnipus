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

const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(({ size = 'default', className, children, 'aria-label': ariaLabel, 'aria-labelledby': ariaLabelledBy, ...buttonProps }, ref) => {
  const accessibleName = ariaLabel ?? ariaLabelledBy
  if (accessibleName.trim().length === 0) throw new Error('IconButton accessible name must contain visible text')
  return (
    <Button
      ref={ref}
      size="icon"
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
