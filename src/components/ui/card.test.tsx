import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Card, CardHeader, CardTitle, CardContent } from './card'

// test_card_surface_color
// Traces to: wave0-brand-design-spec.md Scenario: Card uses elevated dark surface (US-2 AC3, FR-004)
// The default variant is flat (no shadow) — see the Card migration inventory:
// 98 of 103 hand-built card surfaces in the app carry no shadow utility.
describe('Card — flat dark surface (default variant)', () => {
  it('renders with surface-1 background CSS variable', () => {
    const { container } = render(<Card>Card content</Card>)
    const card = container.firstChild as HTMLElement
    // Card uses bg-[var(--color-surface-1)] (elevated surface ~#111113)
    expect(card.className).toContain('var(--color-surface-1)')
  })

  it('renders with Liquid Silver text CSS variable', () => {
    const { container } = render(<Card>Card content</Card>)
    const card = container.firstChild as HTMLElement
    // Card uses text-[var(--color-secondary)] = Liquid Silver
    expect(card.className).toContain('var(--color-secondary)')
  })

  it('renders with subtle border', () => {
    const { container } = render(<Card>Card content</Card>)
    const card = container.firstChild as HTMLElement
    // Card has border styling
    expect(card.className).toContain('border')
  })

  it('renders with rounded-lg radius, matching the dominant hand-built pattern', () => {
    const { container } = render(<Card>Card content</Card>)
    const card = container.firstChild as HTMLElement
    expect(card.className).toContain('rounded-lg')
  })

  it('renders flat — no shadow utility on the default variant', () => {
    const { container } = render(<Card>Card content</Card>)
    const card = container.firstChild as HTMLElement
    expect(card.className).not.toMatch(/shadow-/)
  })

  it('renders children correctly', () => {
    const { getByText } = render(
      <Card>
        <CardHeader>
          <CardTitle>Test Title</CardTitle>
        </CardHeader>
        <CardContent>Test Body</CardContent>
      </Card>
    )
    expect(getByText('Test Title')).toBeTruthy()
    expect(getByText('Test Body')).toBeTruthy()
  })

  it('CardTitle uses headline font class', () => {
    const { container } = render(<CardTitle>Title</CardTitle>)
    const title = container.firstChild as HTMLElement
    expect(title.className).toContain('font-headline')
  })
})

describe('Card — variant prop', () => {
  it('inset variant uses surface-2 background, for nested panels', () => {
    const { container } = render(<Card variant="inset">Nested panel</Card>)
    const card = container.firstChild as HTMLElement
    expect(card.className).toContain('var(--color-surface-2)')
    expect(card.className).not.toContain('var(--color-surface-1)')
  })

  it('floating variant uses the elevation-floating token, not an invented shadow', () => {
    const { container } = render(<Card variant="floating">Floating panel</Card>)
    const card = container.firstChild as HTMLElement
    expect(card.className).toContain('var(--elevation-floating)')
    expect(card.className).toContain('rounded-xl')
  })
})
