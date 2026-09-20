import * as React from 'react'
import { Command as CommandPrimitive } from 'cmdk'
import { clsx } from 'clsx'

const Command = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive>
>(({ className, label = 'Command search', ...props }, ref) => (
  <CommandPrimitive
    ref={ref}
    label={label}
    className={clsx("flex h-full w-full flex-col overflow-hidden rounded-md", className)}
    style={{ backgroundColor: 'var(--color-surface-1)', color: 'var(--color-secondary)' }}
    {...props}
  />
))
Command.displayName = CommandPrimitive.displayName

const CommandInput = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive.Input>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive.Input>
>(({ className, ...props }, ref) => (
  <div className="flex items-center border-b px-3" style={{ borderColor: 'var(--color-border)' }}>
    <CommandPrimitive.Input
      ref={ref}
      className={clsx("flex h-10 w-full rounded-md bg-transparent py-3 text-[length:var(--type-body-compact-size)] outline-none [@media(pointer:coarse)]:min-h-[44px] disabled:cursor-not-allowed disabled:opacity-50", className)}
      style={{ color: 'var(--color-secondary)' }}
      {...props}
    />
  </div>
))
CommandInput.displayName = CommandPrimitive.Input.displayName

const CommandList = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive.List>
>(({ className, ...props }, ref) => (
  <CommandPrimitive.List
    ref={ref}
    className={clsx("max-h-[300px] overflow-y-auto overflow-x-hidden", className)}
    {...props}
  />
))
CommandList.displayName = CommandPrimitive.List.displayName

const CommandEmpty = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive.Empty>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive.Empty>
>((props, ref) => (
  <CommandPrimitive.Empty ref={ref} className="py-6 text-center text-[length:var(--type-body-compact-size)]" style={{ color: 'var(--color-muted)' }} {...props} />
))
CommandEmpty.displayName = CommandPrimitive.Empty.displayName

const CommandGroup = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive.Group>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive.Group>
>(({ className, ...props }, ref) => (
  <CommandPrimitive.Group
    ref={ref}
    className={clsx("overflow-hidden p-1", className)}
    style={{ color: 'var(--color-secondary)' }}
    {...props}
  />
))
CommandGroup.displayName = CommandPrimitive.Group.displayName

const CommandSeparator = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive.Separator>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive.Separator>
>(({ className, ...props }, ref) => (
  <CommandPrimitive.Separator
    ref={ref}
    className={clsx("-mx-1 h-px", className)}
    style={{ backgroundColor: 'var(--color-border)' }}
    {...props}
  />
))
CommandSeparator.displayName = CommandPrimitive.Separator.displayName

const CommandItem = React.forwardRef<
  React.ComponentRef<typeof CommandPrimitive.Item>,
  React.ComponentPropsWithoutRef<typeof CommandPrimitive.Item>
>(({ className, ...props }, ref) => (
  <CommandPrimitive.Item
    ref={ref}
    className={clsx("relative flex cursor-pointer select-none items-center rounded-sm px-2 py-1.5 text-[length:var(--type-body-compact-size)] outline-none transition-colors data-[selected=true]:bg-[rgba(212,175,55,0.12)] data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50", className)}
    style={{ color: 'var(--color-secondary)' }}
    {...props}
  />
))
CommandItem.displayName = CommandPrimitive.Item.displayName

export { Command, CommandInput, CommandList, CommandEmpty, CommandGroup, CommandItem, CommandSeparator }
