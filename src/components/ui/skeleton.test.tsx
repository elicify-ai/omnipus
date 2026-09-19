import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Skeleton } from './skeleton'

describe('Skeleton — delayed visibility with reserved structure', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('reserves its requested geometry immediately and appears after 400ms', () => {
    render(<Skeleton pending className="h-16" data-testid="skeleton" />)
    const skeleton = screen.getByTestId('skeleton')
    expect(skeleton).toHaveClass('h-16')
    expect(skeleton).toHaveAttribute('data-visible', 'false')
    act(() => vi.advanceTimersByTime(399))
    expect(skeleton).toHaveAttribute('data-visible', 'false')
    act(() => vi.advanceTimersByTime(1))
    expect(skeleton).toHaveAttribute('data-visible', 'true')
  })

  it('stays visible for 300ms once shown', () => {
    const { rerender } = render(<Skeleton pending data-testid="skeleton" />)
    act(() => vi.advanceTimersByTime(400))
    rerender(<Skeleton pending={false} data-testid="skeleton" />)
    act(() => vi.advanceTimersByTime(299))
    expect(screen.getByTestId('skeleton')).toHaveAttribute('data-visible', 'true')
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByTestId('skeleton')).toHaveAttribute('data-visible', 'false')
  })

  it('never flashes when pending ends before the delay', () => {
    const { rerender } = render(<Skeleton pending data-testid="skeleton" />)
    act(() => vi.advanceTimersByTime(399))
    rerender(<Skeleton pending={false} data-testid="skeleton" />)
    act(() => vi.runAllTimers())
    expect(screen.getByTestId('skeleton')).toHaveAttribute('data-visible', 'false')
  })

  it('keeps decorative and reduced-motion invariants caller-proof', () => {
    render(<Skeleton pending aria-hidden={false} data-visible="forged" data-testid="skeleton" />)
    const skeleton = screen.getByTestId('skeleton')
    expect(skeleton).toHaveAttribute('aria-hidden', 'true')
    expect(skeleton).toHaveAttribute('data-visible', 'false')
    expect(skeleton).toHaveClass('motion-reduce:animate-none', 'motion-reduce:transition-none')
  })
})
