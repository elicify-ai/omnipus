import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { QueryErrorState } from './query-error-state'

describe('QueryErrorState — pure query error presentation', () => {
  it('preserves layout, test id, and retry behavior without application state', () => {
    const onRetry = vi.fn()
    render(<QueryErrorState message="Could not load tasks." layout="fill" testId="query-failure" onRetry={onRetry} />)
    expect(screen.getByTestId('query-failure')).toHaveClass('flex-1')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
