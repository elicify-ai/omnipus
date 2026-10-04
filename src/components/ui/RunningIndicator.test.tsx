import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RunningIndicator } from '@/components/ui/RunningIndicator'

describe('RunningIndicator', () => {
  it('renders the short "{n} tok" label for values under 1000', () => {
    render(<RunningIndicator tokens={44} />)
    expect(screen.getByText('44 tok')).toBeInTheDocument()
  })

  it('formats large counts through formatTokens ("4.4k tok")', () => {
    render(<RunningIndicator tokens={4400} />)
    expect(screen.getByText('4.4k tok')).toBeInTheDocument()
  })

  it('is a polite status region whose accessible name carries the full word "tokens"', () => {
    render(<RunningIndicator tokens={44} />)
    const status = screen.getByRole('status')
    expect(status).toHaveAttribute('aria-label', '44 tokens')
    expect(status).toHaveAttribute('title', 'Running')
  })

  it('spins while streaming (default) and carries the reduced-motion slow-down', () => {
    const { container } = render(<RunningIndicator tokens={44} />)
    const icon = container.querySelector('svg')
    expect(icon).toHaveClass('animate-spin')
    expect(icon).toHaveClass('motion-reduce:[animation-duration:3.4s]')
  })

  it('holds the spinner still when streaming is false', () => {
    const { container } = render(<RunningIndicator tokens={44} streaming={false} />)
    const icon = container.querySelector('svg')
    expect(icon).not.toHaveClass('animate-spin')
    expect(icon).toHaveAttribute('aria-hidden', 'true')
  })

  it('renders only the spinner and the count — nothing else (SP-41 treatment)', () => {
    const { container } = render(<RunningIndicator tokens={44} />)
    expect(container.querySelectorAll('svg')).toHaveLength(1)
    expect(container.textContent).toBe('44 tok')
  })
})
