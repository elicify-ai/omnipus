// FormError — shared form-field error renderer tests
//
// Wave 6 / A3 / C7 (WCAG 3.3.1, 3.3.3, 4.1.3). Two acceptance cases from
// the wave-6 plan §W6-A3 / C7:
//   1. Renders nothing when error is null/empty
//   2. Renders the error message with role=alert when error is truthy
//
// The role=alert assertion is the load-bearing part — that's the bit that
// makes screen readers announce the error. If a future refactor drops
// role=alert, this test fails loud and the WCAG contract is broken.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FormError } from './FormError'

describe('FormError', () => {
  it('renders nothing when error is null', () => {
    const { container } = render(<FormError error={null} />)
    expect(container.firstChild).toBeNull()
    // No role=alert region must exist
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders nothing when error is undefined', () => {
    const { container } = render(<FormError error={undefined} />)
    expect(container.firstChild).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders nothing when error is an empty string', () => {
    const { container } = render(<FormError error="" />)
    expect(container.firstChild).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders the error message with role=alert when error is truthy', () => {
    render(<FormError error="Name is required" />)
    const alert = screen.getByRole('alert')
    expect(alert).toBeTruthy()
    expect(alert.textContent).toBe('Name is required')
  })

  it('exposes the id prop on the alert element for aria-describedby wiring', () => {
    render(<FormError id="name-error" error="Name is required" />)
    const alert = screen.getByRole('alert')
    expect(alert.id).toBe('name-error')
  })

  it('applies a custom className alongside the default error styling', () => {
    render(<FormError error="Bad" className="custom-class" />)
    const alert = screen.getByRole('alert')
    expect(alert.className).toContain('custom-class')
    // Default text-xs / color-error should still be present
    expect(alert.className).toContain('text-[var(--color-error)]')
  })
})
