import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Progress } from './progress'

describe('Progress — truthful value contract', () => {
  it.each([
    { value: 0, max: 100, transform: 'translateX(-100%)' },
    { value: 50, max: 200, transform: 'translateX(-75%)' },
    { value: 100, max: 100, transform: 'translateX(-0%)' },
  ])('forwards determinate value $value of $max', ({ value, max, transform }) => {
    render(<Progress value={value} max={max} label="Upload progress" />)
    const root = screen.getByRole('progressbar', { name: 'Upload progress' })
    expect(root).toHaveAttribute('aria-valuenow', String(value))
    expect(root).toHaveAttribute('aria-valuemax', String(max))
    expect(root.firstElementChild).toHaveStyle({ transform })
  })

  it.each([undefined, null, Number.NaN, Number.POSITIVE_INFINITY, -1, 101])(
    'renders %s as unknown progress without a fabricated aria value',
    (value) => {
      render(<Progress value={value} max={100} aria-label="Indexing" />)
      const root = screen.getByRole('progressbar', { name: 'Indexing' })
      expect(root).not.toHaveAttribute('aria-valuenow')
      expect(root.firstElementChild).not.toHaveAttribute('style')
    },
  )

  it.each([0, -1, Number.NaN, Number.POSITIVE_INFINITY])(
    'treats invalid max %s as unknown progress',
    (max) => {
      render(<Progress value={0} max={max} aria-label="Exporting" />)
      const root = screen.getByRole('progressbar', { name: 'Exporting' })
      expect(root).not.toHaveAttribute('aria-valuenow')
      expect(root).toHaveAttribute('aria-valuemax', '100')
    },
  )

  it('shows unknown active progress without essential motion', () => {
    render(<Progress value={undefined} aria-label="Indexing" />)
    const indicator = screen.getByRole('progressbar', { name: 'Indexing' }).firstElementChild
    expect(indicator).toHaveClass('mx-auto', 'w-1/3', 'animate-pulse', 'motion-reduce:animate-none', 'motion-reduce:transition-none')
    expect(indicator).not.toHaveClass('w-full')
    expect(indicator).not.toHaveClass('translate-x-full')
  })

  it('uses distinct system colors for the forced-colors track and fill', () => {
    render(<Progress value={50} aria-label="Upload progress" />)
    const track = screen.getByRole('progressbar', { name: 'Upload progress' })
    const indicator = track.firstElementChild
    expect(track).toHaveClass(
      'forced-colors:border',
      'forced-colors:border-[CanvasText]',
      'forced-colors:bg-[Canvas]',
      'forced-colors:[forced-color-adjust:none]',
    )
    expect(indicator).toHaveClass('forced-colors:bg-[Highlight]')
  })
})

// Truthful progress semantics belong to value/max, including callers outside TypeScript.
describe('Progress — numeric accessibility ownership', () => {
  it('ignores conflicting numeric ARIA input and keeps the announced range aligned with its fill', () => {
    const overrides: Record<string, number> = { 'aria-valuenow': 90, 'aria-valuemin': 10, 'aria-valuemax': 50 }
    render(<Progress {...overrides} value={25} max={100} label="Upload" />)
    const bar = screen.getByRole('progressbar', { name: 'Upload' })
    expect(bar).toHaveAttribute('aria-valuenow', '25')
    expect(bar).toHaveAttribute('aria-valuemin', '0')
    expect(bar).toHaveAttribute('aria-valuemax', '100')
    expect(bar).toHaveAttribute('aria-valuetext', '25%')
    expect(bar.firstElementChild).toHaveStyle({ transform: 'translateX(-75%)' })
  })

  it.each([{ value: null, max: 100 }, { value: 25, max: 0 }])('cannot fabricate determinate progress for value=$value max=$max', ({ value, max }) => {
    const overrides: Record<string, number> = { 'aria-valuenow': 75, 'aria-valuemin': 10, 'aria-valuemax': 50 }
    render(<Progress {...overrides} value={value} max={max} label="Indexing" />)
    const bar = screen.getByRole('progressbar', { name: 'Indexing' })
    expect(bar).not.toHaveAttribute('aria-valuenow')
    expect(bar).toHaveAttribute('aria-valuemin', '0')
    expect(bar).toHaveAttribute('aria-valuemax', '100')
    expect(bar).toHaveAttribute('data-state', 'indeterminate')
    expect(bar.firstElementChild).not.toHaveAttribute('style')
  })

  it('retains caller naming and custom value text while deriving numeric semantics', () => {
    render(<Progress value={3} max={5} label="Fallback" aria-label="Files" getValueLabel={(value, max) => `${value} of ${max} files`} />)
    const bar = screen.getByRole('progressbar', { name: 'Files' })
    expect(bar).toHaveAttribute('aria-valuetext', '3 of 5 files')
    expect(bar).toHaveAttribute('aria-valuenow', '3')
    expect(bar).toHaveAttribute('aria-valuemax', '5')
    expect(bar.firstElementChild).toHaveStyle({ transform: 'translateX(-40%)' })
  })
})
