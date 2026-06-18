// FormError — shared form-field error renderer tests
//
// Wave 6 / A3 / C7 (WCAG 3.3.1, 4.1.3). Two acceptance cases from the
// wave-6 plan §W6-A3 / C7:
//   1. Renders nothing when error is null/empty
//   2. Renders the error message with role=alert when error is truthy
//
// The role=alert assertion is the load-bearing part — that's the bit that
// makes screen readers announce the error. If a future refactor drops
// role=alert, this test fails loud and the WCAG contract is broken.
//
// WCAG 3.3.3 (Error Suggestion) is intentionally NOT in this component's
// contract — see the file header. Callers pass actionable strings when
// the failure mode is known.

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

  // Wave 6 / A-fix: `role="alert"` already implies `aria-live="assertive"`
  // + `aria-atomic="true"` per ARIA 1.2. Setting `aria-live="polite"` would
  // override the assertive default and weaken the announcement — that's the
  // bug the A-fix commits removed. Pin it: the element must not set
  // aria-live at all (let the implicit value win).
  it('does NOT override the implicit assertive aria-live of role=alert', () => {
    render(<FormError error="Boom" />)
    const alert = screen.getByRole('alert')
    expect(alert.getAttribute('aria-live')).toBeNull()
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
