import * as React from 'react'
import { Check, CaretUpDown } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from '@/components/ui/select'
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover'
import {
  Command,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
} from '@/components/ui/command'

interface SmartSelectItem {
  value: string
  label: string
  className?: string
}

interface SmartSelectProps {
  value: string
  onValueChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
  triggerClassName?: string
  id?: string
  required?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
  items: SmartSelectItem[]
  /** Accessible name for the trigger, forwarded to both the plain Radix
   *  trigger and the searchable cmdk trigger. Required — without it, the
   *  trigger's accessible name falls back to its current VALUE rather than
   *  identifying the field, which fails for screen-reader users. Name the
   *  field (e.g. "Default agent", "Priority", "Log level"). */
  ariaLabel: string
}

const SEARCHABLE_THRESHOLD = 5

export function SmartSelect({
  value,
  onValueChange,
  placeholder = 'Select...',
  disabled = false,
  className,
  triggerClassName,
  items,
  ariaLabel,
  id,
  required,
  'aria-describedby': ariaDescribedBy,
  'aria-invalid': ariaInvalid,
}: SmartSelectProps) {
  if (items.length <= SEARCHABLE_THRESHOLD) {
    return (
      <Select value={value} onValueChange={onValueChange} disabled={disabled}>
        <SelectTrigger
          id={id}
          className={cn(triggerClassName, className)}
          aria-label={ariaLabel}
          aria-describedby={ariaDescribedBy}
          aria-invalid={ariaInvalid}
          aria-required={required || undefined}
        >
          <SelectValue placeholder={placeholder} />
        </SelectTrigger>
        <SelectContent>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value} className={item.className}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    )
  }

  return (
    <SearchableSelect
      value={value}
      onValueChange={onValueChange}
      placeholder={placeholder}
      disabled={disabled}
      className={className}
      triggerClassName={triggerClassName}
      items={items}
      ariaLabel={ariaLabel}
      id={id}
      required={required}
      aria-describedby={ariaDescribedBy}
      aria-invalid={ariaInvalid}
    />
  )
}

function SearchableSelect({
  value,
  onValueChange,
  placeholder,
  disabled,
  className,
  triggerClassName,
  items,
  ariaLabel,
  id,
  required,
  'aria-describedby': ariaDescribedBy,
  'aria-invalid': ariaInvalid,
}: SmartSelectProps) {
  const [open, setOpen] = React.useState(false)
  const [search, setSearch] = React.useState('')

  const selectedLabel = React.useMemo(
    () => items.find((item) => item.value === value)?.label ?? null,
    [items, value]
  )

  function handleSelect(itemValue: string) {
    onValueChange(itemValue)
    setOpen(false)
    setSearch('')
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          disabled={disabled}
          id={id}
          role="combobox"
          aria-expanded={open}
          aria-haspopup="dialog"
          aria-label={ariaLabel}
          aria-describedby={ariaDescribedBy}
          aria-invalid={ariaInvalid}
          aria-required={required || undefined}
          className={cn(
            'flex w-full items-center justify-between px-3 py-2 text-[length:var(--type-body-compact-size)]',
            'bg-[var(--color-surface-1)] text-[var(--color-secondary)]',
            'ring-offset-[var(--color-primary)] transition-colors motion-reduce:transition-none',
            '',
            'disabled:cursor-not-allowed disabled:opacity-50',
            open
              ? 'border-[var(--color-accent)]'
              : 'border-[var(--color-border)]',
            triggerClassName,
            className
          )}
        >
          <span className={cn('line-clamp-1', !selectedLabel ? 'text-[var(--color-muted)]' : undefined)}>
            {selectedLabel ?? placeholder}
          </span>
          <CaretUpDown size={14} className="ml-2 shrink-0 opacity-50" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-[var(--radix-popover-trigger-width)] min-w-[8rem] p-0"
        align="start"
        sideOffset={4}
      >
        <Command>
          <CommandInput
            placeholder="Search..."
            value={search}
            onValueChange={setSearch}
          />
          <CommandList>
            <CommandEmpty>No results found.</CommandEmpty>
            <CommandGroup>
              {items.map((item) => (
                <CommandItem
                  key={item.value}
                  value={item.label}
                  onSelect={() => handleSelect(item.value)}
                  className={item.className}
                >
                  <span className="flex-1">{item.label}</span>
                  {item.value === value && (
                    <Check size={14} style={{ color: 'var(--color-accent)' }} className="ml-2 shrink-0" aria-hidden="true" />
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
