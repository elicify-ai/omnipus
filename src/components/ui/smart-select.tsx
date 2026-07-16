import * as React from 'react'
import { Check, CaretUpDown } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
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
  items: SmartSelectItem[]
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
}: SmartSelectProps) {
  if (items.length <= SEARCHABLE_THRESHOLD) {
    return (
      <Select value={value} onValueChange={onValueChange} disabled={disabled}>
        <SelectTrigger className={cn(triggerClassName, className)}>
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
        <button
          type="button"
          disabled={disabled}
          aria-expanded={open}
          aria-haspopup="listbox"
          className={cn(
            'flex h-9 w-full items-center justify-between rounded-md border px-3 py-2 text-sm',
            'bg-[var(--color-surface-1)] text-[var(--color-secondary)]',
            'ring-offset-[var(--color-primary)] transition-colors',
            '',
            'disabled:cursor-not-allowed disabled:opacity-50',
            open
              ? 'border-[var(--color-accent)]'
              : 'border-[var(--color-border)]',
            triggerClassName,
            className
          )}
        >
          <span className={cn('line-clamp-1', !selectedLabel && 'text-[var(--color-muted)]')}>
            {selectedLabel ?? placeholder}
          </span>
          <CaretUpDown size={14} className="ml-2 shrink-0 opacity-50" />
        </button>
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
                    <Check size={14} style={{ color: 'var(--color-accent)' }} className="ml-2 shrink-0" />
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
