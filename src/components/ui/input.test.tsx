import { describe, it, expect, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
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

  it('preserves native form identity, controlled changes, read-only and invalid state', () => {
    const onChange = vi.fn()
    const { rerender } = render(<Input id="title" name="title" value="Alpha" onChange={onChange} readOnly required aria-invalid />)
    const input = document.querySelector('input')!
    expect(input).toHaveAttribute('id', 'title')
    expect(input).toHaveAttribute('name', 'title')
    expect(input).toBeRequired()
    expect(input).toHaveAttribute('readonly')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    rerender(<Input value="Beta" onChange={onChange} />)
    expect(input).toHaveValue('Beta')
    fireEvent.change(input, { target: { value: 'Gamma' } })
    expect(onChange).toHaveBeenCalledOnce()
  })
})
