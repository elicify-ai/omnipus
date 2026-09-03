// baseViewNames.test.ts — the .base view enumeration and slug derivation
// (view-kinds-design-2026-09-03 §7).
//
// The slug oracle is pkg/vaultimport/util.go's SlugRegistry: kebab(stem) +
// "--" + kebab(view name), where kebab lowercases and collapses every
// non-[a-z0-9] run into one hyphen. Expected values below are computed BY
// HAND from that rule, not read back from the implementation.

import { describe, it, expect } from 'vitest'
import { baseViewSlug, kebab, parseBaseViews } from './baseViewNames'

describe('kebab — mirror of pkg/vaultimport/util.go', () => {
  it('lowercases and collapses non-alphanumeric runs into one hyphen', () => {
    expect(kebab('Paid this quarter')).toBe('paid-this-quarter')
    expect(kebab('Unpaid / by client')).toBe('unpaid-by-client')
    expect(kebab('  Aged!!  ')).toBe('aged')
    expect(kebab('Q3 2026')).toBe('q3-2026')
  })
})

describe('baseViewSlug', () => {
  it('joins base stem and view name with a double hyphen', () => {
    expect(baseViewSlug('Invoices.base', 'Outstanding')).toBe('invoices--outstanding')
    expect(baseViewSlug('CRM Deals.base', 'Open, by owner')).toBe('crm-deals--open-by-owner')
  })
  it('falls back to "view" when both halves kebab away to nothing', () => {
    expect(baseViewSlug('....base', '!!!')).toBe('view')
  })
})

const REALISTIC_BASE = `filters:
  and:
    - 'type == "invoice"'
views:
  - type: table
    name: Outstanding
    filters:
      and:
        - 'status != "paid"'
    order:
      - file.name
      - amount
  - type: table
    name: "Aged"
  - type: cards
    name: 'Paid this quarter'
    filters: 'status == "paid"'
`

describe('parseBaseViews', () => {
  it('enumerates the views in declaration order with their slugs', () => {
    const views = parseBaseViews(REALISTIC_BASE, 'Invoices.base')
    expect(views.map((v) => v.name)).toEqual(['Outstanding', 'Aged', 'Paid this quarter'])
    expect(views.map((v) => v.slug)).toEqual([
      'invoices--outstanding',
      'invoices--aged',
      'invoices--paid-this-quarter',
    ])
  })

  it('captures each view own filters block for the empty state, and only that view own', () => {
    const views = parseBaseViews(REALISTIC_BASE, 'Invoices.base')
    expect(views[0]?.filterText).toContain('status != "paid"')
    // The top-level (base-wide) filter block is NOT attributed to a view.
    expect(views[0]?.filterText).not.toContain('type == "invoice"')
    // A view with no filters has none, rather than inheriting a neighbour's.
    expect(views[1]?.filterText).toBeUndefined()
    // An inline (single-line) filters value is captured too.
    expect(views[2]?.filterText).toContain('status == "paid"')
  })

  it('answers empty for a file with no views key, and skips unnamed items', () => {
    expect(parseBaseViews('filters:\n  and: []\n', 'X.base')).toEqual([])
    const views = parseBaseViews('views:\n  - type: table\n  - type: table\n    name: Real\n', 'X.base')
    expect(views.map((v) => v.name)).toEqual(['Real'])
  })

  it('handles the name sharing the dash line', () => {
    const views = parseBaseViews('views:\n  - name: First\n    type: table\n', 'X.base')
    expect(views.map((v) => v.slug)).toEqual(['x--first'])
  })
})
