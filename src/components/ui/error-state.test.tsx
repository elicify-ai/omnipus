import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ErrorState } from './error-state'

describe('ErrorState — presentation contract', () => {
  it('renders an alert and invokes retry when supplied', () => {
    const onRetry = vi.fn()
    render(<ErrorState message="Could not load." onRetry={onRetry} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load.')
    expect(screen.getByText('Could not load.')).toHaveClass('forced-colors:text-[CanvasText]')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
