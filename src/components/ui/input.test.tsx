import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Input } from './input'

// test_input_focus_ring
// Traces to: wave0-brand-design-spec.md Scenario: Input shows Forge Gold focus ring (US-2 AC4, FR-004)
describe('Input — Forge Gold focus ring', () => {
  it('carries NO per-component focus ring (the central :focus-visible rule in globals.css owns it)', () => {
    const { container } = render(<Input />)
    const input = container.querySelector('input')
    expect(input).not.toBeNull()
    // Centralized focus system: components must NOT declare their own
    // focus-visible ring/border — the global 2px gold outline applies.
    expect(input!.className).not.toContain('focus-visible:ring')
    expect(input!.className).not.toContain('focus-visible:border')
  })

  it('renders with dark background CSS variable', () => {
    const { container } = render(<Input />)
    const input = container.querySelector('input')
    // Input uses bg-[var(--color-surface-1)] = dark surface
    expect(input!.className).toContain('var(--color-surface-1)')
  })

  it('renders with Liquid Silver text CSS variable', () => {
    const { container } = render(<Input />)
    const input = container.querySelector('input')
    // Input uses text-[var(--color-secondary)] = Liquid Silver
    expect(input!.className).toContain('var(--color-secondary)')
  })

  it('renders as an input element', () => {
    const { container } = render(<Input placeholder="Enter text" />)
    const input = container.querySelector('input')
    expect(input).not.toBeNull()
    expect(input!.getAttribute('placeholder')).toBe('Enter text')
  })

  it('is disabled when disabled prop is set', () => {
    const { container } = render(<Input disabled />)
    expect(container.querySelector('input')).toBeDisabled()
  })
})
