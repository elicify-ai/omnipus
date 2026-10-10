/**
 * smart-select.icon.test.tsx — the optional SmartSelect item `icon`
 * (the mechanism that lets the Role badge dropdown show each option's
 * badge glyph; founder 2026-10-10: EACH OPTION SHOWS ITS BADGE ICON).
 *
 * Oracle (founder decision, relayed 2026-10-10): an item's optional icon
 * renders in the list AND on the trigger when that item is selected; items
 * without an icon render exactly as before (no regression for the existing
 * callers, whose items carry none). The icon is decorative: the label stays
 * the accessible name.
 */
import * as React from 'react'
import { beforeAll, describe, expect, it } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { SmartSelect } from './smart-select'

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

/** A distinguishable decorative glyph per item, the way RoleBadgeIcon is one. */
function glyph(label: string): React.ReactNode {
  return <svg data-testid={`glyph-${label}`} aria-hidden="true" />
}

/** Five plain items (below the searchable threshold) — three with icons, two without. */
const PLAIN_ITEMS = [
  { value: 'a', label: 'Alpha', icon: glyph('Alpha') },
  { value: 'b', label: 'Beta', icon: glyph('Beta') },
  { value: 'c', label: 'Gamma' },
  { value: 'd', label: 'Delta' },
  { value: 'e', label: 'Epsilon', icon: glyph('Epsilon') },
]

/** Six items — crosses the searchable threshold, same icon mix. */
const SEARCH_ITEMS = [
  ...PLAIN_ITEMS,
  { value: 'f', label: 'Zeta', icon: glyph('Zeta') },
]

function Harness({ items, initial }: { items: typeof PLAIN_ITEMS; initial: string }) {
  const [value, setValue] = React.useState(initial)
  return <SmartSelect value={value} onValueChange={setValue} ariaLabel="Pick" items={items} />
}

describe('SmartSelect item icon — searchable branch (more than 5 items)', () => {
  it('renders an item icon in the open list', () => {
    render(<Harness items={SEARCH_ITEMS} initial="a" />)
    fireEvent.click(screen.getByRole('combobox', { name: 'Pick' }))
    const option = screen.getByRole('option', { name: 'Beta' })
    expect(within(option).getByTestId('glyph-Beta')).toBeInTheDocument()
  })

  it('shows the selected item icon on the trigger', () => {
    render(<Harness items={SEARCH_ITEMS} initial="b" />)
    const trigger = screen.getByRole('combobox', { name: 'Pick' })
    expect(within(trigger).getByTestId('glyph-Beta')).toBeInTheDocument()
    // Selecting a different item swaps the trigger icon to that item's.
    fireEvent.click(trigger)
    fireEvent.click(screen.getByRole('option', { name: 'Zeta' }))
    expect(within(trigger).getByTestId('glyph-Zeta')).toBeInTheDocument()
    expect(within(trigger).queryByTestId('glyph-Beta')).not.toBeInTheDocument()
  })

  it('leaves icon-less items exactly as before: label only, no glyph', () => {
    render(<Harness items={SEARCH_ITEMS} initial="c" />)
    fireEvent.click(screen.getByRole('combobox', { name: 'Pick' }))
    const option = screen.getByRole('option', { name: 'Gamma' })
    expect(option).toHaveTextContent('Gamma')
    // No GLYPH where none was configured (the selection check svg is
    // legitimate chrome and renders regardless of icons).
    expect(option.querySelector('[data-testid^="glyph-"]')).toBeNull()
    // Selecting it must not put any glyph on the trigger either.
    fireEvent.click(option)
    expect(within(screen.getByRole('combobox', { name: 'Pick' })).queryByTestId(/^glyph-/)).toBeNull()
  })
})

describe('SmartSelect item icon — plain branch (5 items or fewer)', () => {
  it('renders an item icon in the open list', () => {
    render(<Harness items={PLAIN_ITEMS} initial="a" />)
    fireEvent.click(screen.getByRole('combobox', { name: 'Pick' }))
    const option = screen.getByRole('option', { name: 'Alpha' })
    expect(within(option).getByTestId('glyph-Alpha')).toBeInTheDocument()
  })

  it('shows the selected item icon on the trigger', () => {
    render(<Harness items={PLAIN_ITEMS} initial="e" />)
    const trigger = screen.getByRole('combobox', { name: 'Pick' })
    expect(within(trigger).getByTestId('glyph-Epsilon')).toBeInTheDocument()
  })

  it('leaves icon-less items exactly as before: label only, no glyph', () => {
    render(<Harness items={PLAIN_ITEMS} initial="d" />)
    const trigger = screen.getByRole('combobox', { name: 'Pick' })
    expect(trigger).toHaveTextContent('Delta')
    expect(within(trigger).queryByTestId(/^glyph-/)).toBeNull()
    fireEvent.click(trigger)
    const option = screen.getByRole('option', { name: 'Delta' })
    expect(option).toHaveTextContent('Delta')
    expect(option.querySelector('[data-testid^="glyph-"]')).toBeNull()
  })
})
