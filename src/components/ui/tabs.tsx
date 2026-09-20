import * as React from 'react'
import * as TabsPrimitive from '@radix-ui/react-tabs'
import { cn } from '@/lib/utils'

const Tabs = TabsPrimitive.Root

const TabsList = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    className={cn(
      'inline-flex h-9 items-center justify-start rounded-lg bg-[var(--color-surface-1)] p-[var(--space-1)] gap-[var(--space-1)]',
      className
    )}
    {...props}
  />
))
TabsList.displayName = TabsPrimitive.List.displayName

const TabsTrigger = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Trigger
    ref={ref}
    className={cn(
      'relative inline-flex items-center justify-center whitespace-nowrap rounded-md px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)] font-medium ring-offset-[var(--color-primary)] transition-all motion-reduce:transition-none disabled:pointer-events-none disabled:opacity-50',
      'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
      'data-[state=active]:bg-[var(--color-surface-2)] data-[state=active]:text-[var(--color-secondary)] data-[state=active]:shadow-sm',
      className
    )}
    {...props}
    data-ds-action=""
  />
))
TabsTrigger.displayName = TabsPrimitive.Trigger.displayName

const TabsContent = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Content>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Content
    ref={ref}
    // Radix defaults tabpanels to tabindex="0" (APG's escape hatch for panels
    // with no focusable children). Every panel in this app contains focusable
    // controls, so the wrapper itself is a do-nothing Tab stop — pressing
    // Enter on it has no effect, and on WebKit (Safari's default Tab policy
    // reaches ONLY explicitly-tabindexed elements + form fields) these
    // wrappers used to be nearly the whole tab ring. Opt the panel out;
    // callers with a genuinely control-free panel can pass tabIndex={0} back.
    tabIndex={-1}
    className={cn(
      'mt-[var(--space-3)] ring-offset-[var(--color-primary)]',
      className
    )}
    {...props}
  />
))
TabsContent.displayName = TabsPrimitive.Content.displayName

export { Tabs, TabsList, TabsTrigger, TabsContent }
