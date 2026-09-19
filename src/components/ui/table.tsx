import * as React from 'react'
import { cn } from '@/lib/utils'

export interface TableProps extends React.HTMLAttributes<HTMLTableElement> {
  containerProps?: React.HTMLAttributes<HTMLDivElement>
}

const TABLE_KEYBOARD_SCROLL_STEP = 40

const Table = React.forwardRef<HTMLTableElement, TableProps>(
  ({ className, containerProps, ...props }, ref) => {
    const { className: containerClassName, onKeyDown, ...restContainerProps } = containerProps ?? {}
    const handleContainerKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
      onKeyDown?.(event)
      if (event.defaultPrevented || event.target !== event.currentTarget) return
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return

      const container = event.currentTarget
      const maximum = Math.max(0, container.scrollWidth - container.clientWidth)
      const delta = event.key === 'ArrowRight' ? TABLE_KEYBOARD_SCROLL_STEP : -TABLE_KEYBOARD_SCROLL_STEP
      const next = Math.min(maximum, Math.max(0, container.scrollLeft + delta))
      if (next === container.scrollLeft) return

      event.preventDefault()
      container.scrollLeft = next
    }

    return (
      <div
        {...restContainerProps}
        data-table-scroll=""
        className={cn('relative w-full overflow-auto', containerClassName)}
        onKeyDown={handleContainerKeyDown}
      >
        <table
          ref={ref}
          className={cn('w-full caption-bottom text-sm', className)}
          {...props}
        />
      </div>
    )
  }
)
Table.displayName = 'Table'

const TableHeader = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...props }, ref) => (
    <thead ref={ref} className={cn('bg-[var(--color-surface-1)] [&_tr]:border-b', className)} {...props} />
  )
)
TableHeader.displayName = 'TableHeader'

const TableBody = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...props }, ref) => (
    <tbody ref={ref} className={cn('[&_tr:last-child]:border-0', className)} {...props} />
  )
)
TableBody.displayName = 'TableBody'

const TableRow = React.forwardRef<HTMLTableRowElement, React.HTMLAttributes<HTMLTableRowElement>>(
  ({ className, ...props }, ref) => (
    <tr
      ref={ref}
      className={cn(
        'border-b border-[var(--color-border)] transition-colors motion-reduce:transition-none hover:bg-[var(--color-surface-3)]/50 data-[state=selected]:bg-[var(--color-surface-3)]',
        className
      )}
      {...props}
    />
  )
)
TableRow.displayName = 'TableRow'

const TableHead = React.forwardRef<HTMLTableCellElement, React.ThHTMLAttributes<HTMLTableCellElement>>(
  ({ className, ...props }, ref) => (
    <th
      ref={ref}
      className={cn(
        'h-10 px-4 text-left align-middle text-xs font-medium text-[var(--color-muted)] [&:has([role=checkbox])]:pr-0',
        className
      )}
      {...props}
    />
  )
)
TableHead.displayName = 'TableHead'

const TableCell = React.forwardRef<HTMLTableCellElement, React.TdHTMLAttributes<HTMLTableCellElement>>(
  ({ className, ...props }, ref) => (
    <td
      ref={ref}
      className={cn('px-4 py-2.5 align-middle [&:has([role=checkbox])]:pr-0', className)}
      {...props}
    />
  )
)
TableCell.displayName = 'TableCell'

export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell }
