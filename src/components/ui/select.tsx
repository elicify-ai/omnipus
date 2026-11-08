import * as React from 'react'
import * as SelectPrimitive from '@radix-ui/react-select'
import { CaretDown, CaretUp, Check } from '@phosphor-icons/react'
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const Select = SelectPrimitive.Root
const SelectGroup = SelectPrimitive.Group
const SelectValue = SelectPrimitive.Value

// Trigger variants (W2-1 contract §2.3; R39, R41). `field` is the form
// control and keeps the pre-W2-1 classes byte for byte, so every existing
// caller renders unchanged. `ghost` is the compact, borderless filter
// trigger for toolbars (FilterMenu parity): sized to its content, muted
// text, accent text when `active` (a filter is applied), and a real 44px
// box on a coarse pointer (skill rule 12). The look lives here; callers
// pass layout only (R41).
const selectTriggerVariants = cva('', {
  variants: {
    variant: {
      field:
        'flex h-9 w-full items-center justify-between rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] [@media(pointer:coarse)]:h-[44px] placeholder:text-[var(--color-muted)] disabled:cursor-not-allowed disabled:opacity-50 [&>span]:line-clamp-1',
      ghost:
        'inline-flex h-8 w-auto items-center justify-center gap-[var(--space-1)] rounded-md border border-transparent bg-transparent px-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-medium transition-colors motion-reduce:transition-none hover:bg-[var(--color-surface-2)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)] forced-colors:text-[ButtonText] disabled:cursor-not-allowed disabled:opacity-50 [&>span]:line-clamp-1',
    },
    active: { true: '', false: '' },
  },
  compoundVariants: [
    { variant: 'ghost', active: false, className: 'text-[var(--color-muted)]' },
    { variant: 'ghost', active: true, className: 'text-[var(--color-accent)]' },
  ],
  defaultVariants: { variant: 'field', active: false },
})

export interface SelectTriggerProps extends React.ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger> {
  /** `field` (default): the form control. `ghost`: the compact toolbar filter trigger. */
  variant?: 'field' | 'ghost'
  /** Ghost only: a filter value is applied, shown in the accent colour. */
  active?: boolean
}

const SelectTrigger = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Trigger>,
  SelectTriggerProps
>(({ className, children, variant = 'field', active = false, ...props }, ref) => (
  <SelectPrimitive.Trigger
    ref={ref}
    // WebKit tabbability (repo convention): Radix renders the trigger button,
    // so the explicit tabIndex stamp lives here; {...props} may override.
    tabIndex={0}
    data-variant={variant}
    className={cn(selectTriggerVariants({ variant, active: variant === 'ghost' && active }), className)}
    {...props}
  >
    {children}
    <SelectPrimitive.Icon asChild>
      {variant === 'ghost'
        ? <CaretDown size={11} className="opacity-60" aria-hidden="true" />
        : <CaretDown size={14} className="opacity-50" aria-hidden="true" />}
    </SelectPrimitive.Icon>
  </SelectPrimitive.Trigger>
))
SelectTrigger.displayName = SelectPrimitive.Trigger.displayName

const SelectScrollUpButton = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.ScrollUpButton>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.ScrollUpButton>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.ScrollUpButton
    ref={ref}
    className={cn('flex cursor-default items-center justify-center py-[var(--space-1)]', className)}
    {...props}
  >
    <CaretUp size={14} aria-hidden="true" />
  </SelectPrimitive.ScrollUpButton>
))
SelectScrollUpButton.displayName = SelectPrimitive.ScrollUpButton.displayName

const SelectScrollDownButton = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.ScrollDownButton>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.ScrollDownButton>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.ScrollDownButton
    ref={ref}
    className={cn('flex cursor-default items-center justify-center py-[var(--space-1)]', className)}
    {...props}
  >
    <CaretDown size={14} aria-hidden="true" />
  </SelectPrimitive.ScrollDownButton>
))
SelectScrollDownButton.displayName = SelectPrimitive.ScrollDownButton.displayName

const SelectContent = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Content>
>(({ className, children, position = 'popper', ...props }, ref) => (
  <SelectPrimitive.Portal>
    <SelectPrimitive.Content
      ref={ref}
      className={cn(
        'relative z-50 max-h-96 min-w-[8rem] overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-secondary)] shadow-md',
        'data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 motion-reduce:animate-none motion-reduce:transition-none',
        position === 'popper' ? 'data-[side=bottom]:translate-y-1 data-[side=left]:-translate-x-1 data-[side=right]:translate-x-1 data-[side=top]:-translate-y-1' : undefined,
        className
      )}
      position={position}
      {...props}
    >
      <SelectScrollUpButton />
      <SelectPrimitive.Viewport
        className={cn(
          'p-[var(--space-1)]',
          position === 'popper' ? 'h-[var(--radix-select-trigger-height)] w-full min-w-[var(--radix-select-trigger-width)]' : undefined
        )}
      >
        {children}
      </SelectPrimitive.Viewport>
      <SelectScrollDownButton />
    </SelectPrimitive.Content>
  </SelectPrimitive.Portal>
))
SelectContent.displayName = SelectPrimitive.Content.displayName

const SelectLabel = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Label>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Label>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.Label
    ref={ref}
    className={cn('py-[var(--space-1)] pl-[var(--space-5)] pr-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)]', className)}
    {...props}
  />
))
SelectLabel.displayName = SelectPrimitive.Label.displayName

const SelectItem = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Item>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Item>
>(({ className, children, ...props }, ref) => (
  <SelectPrimitive.Item
    ref={ref}
    className={cn(
      'relative flex w-full cursor-default select-none items-center rounded-sm py-[var(--space-1)] pl-[var(--space-5)] pr-[var(--space-2)] text-[length:var(--type-body-compact-size)] outline-none focus:bg-[var(--color-surface-3)] focus:text-[var(--color-secondary)] data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
      className
    )}
    {...props}
  >
    <span className="absolute left-2 flex h-3.5 w-3.5 items-center justify-center">
      <SelectPrimitive.ItemIndicator>
        <Check size={14} aria-hidden="true" />
      </SelectPrimitive.ItemIndicator>
    </span>
    <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
  </SelectPrimitive.Item>
))
SelectItem.displayName = SelectPrimitive.Item.displayName

const SelectSeparator = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Separator>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Separator>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.Separator
    ref={ref}
    className={cn('-mx-[var(--space-1)] my-[var(--space-1)] h-px bg-[var(--color-border)]', className)}
    {...props}
  />
))
SelectSeparator.displayName = SelectPrimitive.Separator.displayName

export {
  Select,
  SelectGroup,
  SelectValue,
  SelectTrigger,
  SelectContent,
  SelectLabel,
  SelectItem,
  SelectSeparator,
  SelectScrollUpButton,
  SelectScrollDownButton,
  selectTriggerVariants,
}
