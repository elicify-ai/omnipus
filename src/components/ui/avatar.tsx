import * as React from 'react'
import { cn } from '@/lib/utils'

interface AvatarProps extends React.HTMLAttributes<HTMLDivElement> {
  size?: 'sm' | 'md' | 'lg'
}

const sizeClasses = {
  sm: 'h-7 w-7 text-[length:var(--type-utility-xs-size)]',
  md: 'h-9 w-9 text-[length:var(--type-body-compact-size)]',
  lg: 'h-12 w-12 text-base',
}

const Avatar = React.forwardRef<HTMLDivElement, AvatarProps>(
  ({ className, size = 'md', ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        'relative flex shrink-0 overflow-hidden rounded-full',
        sizeClasses[size],
        className
      )}
      {...props}
    />
  )
)
Avatar.displayName = 'Avatar'

export type AvatarImageProps = Omit<React.ImgHTMLAttributes<HTMLImageElement>, 'alt'> & { alt: string }

const AvatarImage = React.forwardRef<
  HTMLImageElement,
  AvatarImageProps
>(({ className, ...props }, ref) => (
  <img
    ref={ref}
    className={cn('aspect-square h-full w-full object-cover', className)}
    {...props}
  />
))
AvatarImage.displayName = 'AvatarImage'

const AvatarFallback = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn(
      'flex h-full w-full items-center justify-center rounded-full bg-[var(--color-surface-3)] text-[var(--color-secondary)] font-medium',
      className
    )}
    {...props}
  />
))
AvatarFallback.displayName = 'AvatarFallback'

export { Avatar, AvatarImage, AvatarFallback }
