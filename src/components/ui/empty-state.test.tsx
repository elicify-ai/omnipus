import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { EmptyState } from './empty-state'

// @ts-expect-error action label and handler are a required pair
const missingHandler = <EmptyState icon={null} message="Empty" actionLabel="Add" />
// @ts-expect-error action label and handler are a required pair
const missingLabel = <EmptyState icon={null} message="Empty" onAction={() => {}} />
void [missingHandler, missingLabel]

describe('EmptyState — presentation contract', () => {
  it('renders decorative icon, message, and optional action', () => {
    const onAction = vi.fn()
    render(<EmptyState icon={<svg data-testid="icon" />} message="No tools available." actionLabel="Add tool" onAction={onAction} />)
    expect(screen.getByTestId('icon').parentElement).toHaveAttribute('aria-hidden', 'true')
    fireEvent.click(screen.getByRole('button', { name: 'Add tool' }))
    expect(onAction).toHaveBeenCalledOnce()
  })
})
