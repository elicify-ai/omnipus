// M4 Frontend — ErrorBoundary component tests
//
// Covers the ErrorBoundary class component introduced in M4:
//   1. Renders children normally when no error has occurred
//   2. Renders the default fallback UI when a child throws
//   3. Renders a custom fallback when provided
//   4. "Try again" button resets the error state (recovery)
//   5. Displays the thrown error message in the fallback
//
// BDD scenarios inferred from the ErrorBoundary implementation at
// src/components/shared/ErrorBoundary.tsx (no formal spec file for M4 frontend).
// Traces to: src/components/shared/ErrorBoundary.tsx — ErrorBoundary class component

import { describe, it, expect, vi, beforeAll, afterAll } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ErrorBoundary } from '@/components/shared/ErrorBoundary'

// Suppress console.error for expected boundary catches in these tests
const originalError = console.error
beforeAll(() => {
  console.error = vi.fn()
})
afterAll(() => {
  console.error = originalError
})

// Component that throws during render — used as a test child
function ThrowingChild({ shouldThrow, message }: { shouldThrow: boolean; message?: string }) {
  if (shouldThrow) {
    throw new Error(message ?? 'Test render error')
  }
  return <div>Healthy child content</div>
}

// ---------------------------------------------------------------------------
// M4-F1: ErrorBoundary renders children when no error
// ---------------------------------------------------------------------------

// BDD: Given an ErrorBoundary wrapping a non-throwing child,
// When the component renders,
// Then the child content is displayed without a fallback.
// Traces to: src/components/shared/ErrorBoundary.tsx — render() normal path
describe('ErrorBoundary — normal rendering (no error)', () => {
  it('renders child content when no error is thrown', () => {
    render(
      <ErrorBoundary>
        <ThrowingChild shouldThrow={false} />
      </ErrorBoundary>
    )

    expect(screen.getByText('Healthy child content')).toBeTruthy()
  })

  it('does not render the fallback when children are healthy', () => {
    render(
      <ErrorBoundary>
        <ThrowingChild shouldThrow={false} />
      </ErrorBoundary>
    )

    // Default fallback text should not appear
    expect(screen.queryByText('Something went wrong')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// M4-F2: ErrorBoundary shows default fallback on error
// ---------------------------------------------------------------------------

// BDD: Given an ErrorBoundary wrapping a child that throws during render,
// When the child throws,
// Then the default fallback UI is displayed showing "Something went wrong".
// Traces to: src/components/shared/ErrorBoundary.tsx — getDerivedStateFromError + render fallback
describe('ErrorBoundary — default fallback on error', () => {
  it('shows "Something went wrong" when a child throws', () => {
    render(
      <ErrorBoundary>
        <ThrowingChild shouldThrow={true} message="test error" />
      </ErrorBoundary>
    )

    expect(screen.getByText('Something went wrong')).toBeTruthy()
  })

  it('displays the thrown error message in the fallback', () => {
    // Differentiation: two different errors produce two different messages displayed
    render(
      <ErrorBoundary>
        <ThrowingChild shouldThrow={true} message="unique-error-ABC123" />
      </ErrorBoundary>
    )

    expect(screen.getByText('unique-error-ABC123')).toBeTruthy()
  })

  it('hides the child content when an error is caught', () => {
    render(
      <ErrorBoundary>
        <ThrowingChild shouldThrow={true} />
      </ErrorBoundary>
    )

    expect(screen.queryByText('Healthy child content')).toBeNull()
  })

  it('shows a "Try again" button in the default fallback', () => {
    render(
      <ErrorBoundary>
        <ThrowingChild shouldThrow={true} />
      </ErrorBoundary>
    )

    expect(screen.getByText('Try again')).toBeTruthy()
  })
})

// ---------------------------------------------------------------------------
// M4-F3: ErrorBoundary accepts and renders custom fallback
// ---------------------------------------------------------------------------

// BDD: Given an ErrorBoundary with a custom fallback prop,
// When a child throws,
// Then the custom fallback is rendered instead of the default.
// Traces to: src/components/shared/ErrorBoundary.tsx — render() fallback ?? default
describe('ErrorBoundary — custom fallback', () => {
  it('renders the custom fallback when provided and a child throws', () => {
    render(
      <ErrorBoundary fallback={<div>Custom error UI here</div>}>
        <ThrowingChild shouldThrow={true} />
      </ErrorBoundary>
    )

    expect(screen.getByText('Custom error UI here')).toBeTruthy()
    // Default fallback must NOT appear
    expect(screen.queryByText('Something went wrong')).toBeNull()
  })

  it('renders children normally even when a custom fallback is provided', () => {
    render(
      <ErrorBoundary fallback={<div>Custom error UI here</div>}>
        <ThrowingChild shouldThrow={false} />
      </ErrorBoundary>
    )

    expect(screen.getByText('Healthy child content')).toBeTruthy()
    expect(screen.queryByText('Custom error UI here')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// M4-F4: "Try again" button resets error state
// ---------------------------------------------------------------------------

// BDD: Given an ErrorBoundary displaying the default fallback after a child threw,
// When the user clicks "Try again",
// Then the ErrorBoundary resets to hasError=false.
// Traces to: src/components/shared/ErrorBoundary.tsx — onClick setState reset
describe('ErrorBoundary — "Try again" recovery', () => {
  it('clicking "Try again" resets hasError and clears the error', () => {
    // We need a child that stops throwing after the first render so we can confirm recovery.
    // Wrap in a stateful container to control whether the child throws.
    let shouldThrow = true

    function ControlledChild() {
      if (shouldThrow) throw new Error('First render error')
      return <div>Recovered content</div>
    }

    const { rerender } = render(
      <ErrorBoundary>
        <ControlledChild />
      </ErrorBoundary>
    )

    // Verify fallback is shown
    expect(screen.getByText('Something went wrong')).toBeTruthy()

    // Stop throwing before clicking Try again
    shouldThrow = false
    fireEvent.click(screen.getByText('Try again'))

    // After reset, rerender to let the child render without throwing
    rerender(
      <ErrorBoundary>
        <ControlledChild />
      </ErrorBoundary>
    )

    // Fallback must be gone
    expect(screen.queryByText('Something went wrong')).toBeNull()
  })
})
