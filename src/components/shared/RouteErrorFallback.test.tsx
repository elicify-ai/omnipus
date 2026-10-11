import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RouteErrorFallback } from './RouteErrorFallback'

describe('RouteErrorFallback', () => {
  it('shows the error message under the heading', () => {
    render(<RouteErrorFallback error={new Error('A message with the same id already exists')} reset={vi.fn()} />)
    expect(screen.getByText('Something went wrong loading this page.')).toBeTruthy()
    expect(screen.getByText('A message with the same id already exists')).toBeTruthy()
  })

  it('Try again resets the route error in place', () => {
    const reset = vi.fn()
    render(<RouteErrorFallback error={new Error('boom')} reset={reset} />)
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(reset).toHaveBeenCalledTimes(1)
  })

  it('still offers Reload page', () => {
    render(<RouteErrorFallback error={new Error('boom')} reset={vi.fn()} />)
    expect(screen.getByRole('button', { name: 'Reload page' })).toBeTruthy()
  })
})
