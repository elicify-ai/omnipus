import type { ReactNode } from 'react'
import { CaretDown } from '@phosphor-icons/react'
import { Button } from './button'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuCheckboxItem, DropdownMenuSeparator } from './dropdown-menu'
import { cn } from '@/lib/utils'

export interface FilterMenuOption {
  value: string
  label: string
  icon?: ReactNode
}

type FilterSelection =
  | { mode: 'single'; value: string | null; onChange: (value: string | null) => void; clearLabel: string }
  | { mode: 'multiple'; value: readonly string[]; onChange: (value: string[]) => void; clearLabel?: string }

export type FilterMenuProps = FilterSelection & {
  label: string
  'aria-label': string
  icon?: ReactNode
  options: readonly FilterMenuOption[]
  align?: 'start' | 'center' | 'end'
  disabled?: boolean
  className?: string
  'data-testid'?: string
}

/** Flat ghost filter trigger with a single-value or keep-open multi-value menu.
 * Composition of the catalogued shadcn DropdownMenu and Button primitives.
 */
export function FilterMenu({ label, icon, options, align = 'start', disabled, className, ...props }: FilterMenuProps) {
  const active = props.mode === 'single' ? props.value !== null : props.value.length > 0
  return (
    <div data-slot="filter-menu" data-testid={props['data-testid']} className={cn('min-w-0', className)}>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" disabled={disabled} aria-label={props['aria-label']}
            className={cn(
              'h-8 min-w-0 max-w-full gap-[var(--space-1)] px-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-medium pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]',
              active ? 'text-[var(--color-accent)]' : 'text-[var(--color-muted)]',
            )}>
            {icon}
            <span className="min-w-0 truncate">{label}</span>
            <CaretDown size={11} className="shrink-0 opacity-60" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align={align} className="max-h-64 max-w-[calc(100vw-var(--space-4))] overflow-y-auto">
          {props.mode === 'single' && (
            <>
              <DropdownMenuItem onClick={() => props.onChange(null)} className="gap-[var(--space-2)]">
                {props.clearLabel}
                {props.value === null && <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-success)]">active</span>}
              </DropdownMenuItem>
              {options.length > 0 && <DropdownMenuSeparator />}
            </>
          )}
          {options.map((option) => props.mode === 'single' ? (
            <DropdownMenuItem key={option.value} onClick={() => props.onChange(option.value)} className="gap-[var(--space-2)]">
              {option.icon}
              <span className="min-w-0 wrap-anywhere">{option.label}</span>
              {props.value === option.value && <span className="ml-auto shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-success)]">active</span>}
            </DropdownMenuItem>
          ) : (
            <DropdownMenuCheckboxItem key={option.value} checked={props.value.includes(option.value)}
              onCheckedChange={() => props.onChange(props.value.includes(option.value) ? props.value.filter((value) => value !== option.value) : [...props.value, option.value])}
              onSelect={(event) => event.preventDefault()} className="text-[length:var(--type-utility-xs-size)] wrap-anywhere">
              {option.icon}{option.label}
            </DropdownMenuCheckboxItem>
          ))}
          {props.mode === 'multiple' && active && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => props.onChange([])} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                {props.clearLabel ?? 'Clear filter'}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
