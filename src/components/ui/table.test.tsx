import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './table'

describe('Table semantic structure', () => {
  it('retains native table, row, columnheader, and cell roles', () => {
    render(<Table aria-label="Agents"><TableHeader><TableRow><TableHead>Name</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell>Mia</TableCell></TableRow></TableBody></Table>)
    expect(screen.getByRole('table', { name: 'Agents' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeVisible()
    expect(screen.getByRole('cell', { name: 'Mia' })).toBeVisible()
    expect(screen.getAllByRole('row')).toHaveLength(2)
  })

  it('puts accessible scroll-region props on the actual overflow container', () => {
    const { container } = render(<Table containerProps={{ role: 'region', 'aria-label': 'Scrollable agents', tabIndex: 0 }}><TableBody /></Table>)
    const region = screen.getByRole('region', { name: 'Scrollable agents' })
    expect(region).toHaveAttribute('data-table-scroll')
    expect(region).toHaveAttribute('tabindex', '0')
    expect(region).toBe(container.firstElementChild)
  })
})

function makeHorizontallyScrollable(element: HTMLElement, { left = 0, width = 256, scrollWidth = 768 } = {}) {
  Object.defineProperties(element, {
    clientWidth: { configurable: true, value: width },
    scrollWidth: { configurable: true, value: scrollWidth },
    scrollLeft: { configurable: true, writable: true, value: left },
  })
}

describe('Table overflow keyboard portability', () => {
  it('moves the focused overflow region by 40px in each available arrow direction', () => {
    render(<Table containerProps={{ role: 'region', 'aria-label': 'Scrollable agents', tabIndex: 0 }}><TableBody /></Table>)
    const region = screen.getByRole('region', { name: 'Scrollable agents' })
    makeHorizontallyScrollable(region)

    expect(fireEvent.keyDown(region, { key: 'ArrowRight' })).toBe(false)
    expect(region.scrollLeft).toBe(40)
    expect(fireEvent.keyDown(region, { key: 'ArrowLeft' })).toBe(false)
    expect(region.scrollLeft).toBe(0)
  })

  it('honours the caller handler first and does not scroll when it prevents default', () => {
    const onKeyDown = vi.fn((event: React.KeyboardEvent<HTMLDivElement>) => event.preventDefault())
    render(<Table containerProps={{ role: 'region', 'aria-label': 'Scrollable agents', tabIndex: 0, onKeyDown }}><TableBody /></Table>)
    const region = screen.getByRole('region', { name: 'Scrollable agents' })
    makeHorizontallyScrollable(region)

    expect(fireEvent.keyDown(region, { key: 'ArrowRight' })).toBe(false)
    expect(onKeyDown).toHaveBeenCalledOnce()
    expect(region.scrollLeft).toBe(0)
  })

  it('does not consume arrow keys from interactive descendants', () => {
    const onKeyDown = vi.fn()
    render(
      <Table containerProps={{ role: 'region', 'aria-label': 'Scrollable agents', tabIndex: 0, onKeyDown }}>
        <TableBody><TableRow><TableCell><input aria-label="Cell editor" /></TableCell></TableRow></TableBody>
      </Table>,
    )
    const region = screen.getByRole('region', { name: 'Scrollable agents' })
    makeHorizontallyScrollable(region)

    expect(fireEvent.keyDown(screen.getByRole('textbox', { name: 'Cell editor' }), { key: 'ArrowRight' })).toBe(true)
    expect(onKeyDown).toHaveBeenCalledOnce()
    expect(region.scrollLeft).toBe(0)
  })

  it('leaves boundary, non-overflow, and unrelated-key events unconsumed', () => {
    render(<Table containerProps={{ role: 'region', 'aria-label': 'Scrollable agents', tabIndex: 0 }}><TableBody /></Table>)
    const region = screen.getByRole('region', { name: 'Scrollable agents' })
    makeHorizontallyScrollable(region, { left: 512 })

    expect(fireEvent.keyDown(region, { key: 'ArrowRight' })).toBe(true)
    expect(region.scrollLeft).toBe(512)

    region.scrollLeft = 0
    expect(fireEvent.keyDown(region, { key: 'ArrowLeft' })).toBe(true)
    expect(region.scrollLeft).toBe(0)

    makeHorizontallyScrollable(region, { width: 256, scrollWidth: 256 })
    expect(fireEvent.keyDown(region, { key: 'ArrowRight' })).toBe(true)
    expect(fireEvent.keyDown(region, { key: 'ArrowDown' })).toBe(true)
    expect(region.scrollLeft).toBe(0)
  })
})
