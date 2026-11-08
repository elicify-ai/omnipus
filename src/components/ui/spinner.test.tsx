/**
 * Spinner — W2-1 contract §2.7 (R41 "loading → Spinner + Skeleton").
 *
 * RED on 69aa07d9a: `src/components/ui/spinner.tsx` does not exist yet
 * (contract §2.7 names it as a new file) — fails at import for the right
 * reason.
 *
 * jsdom notes: "Size lg gives 24×24" and "computed animation-name is none
 * under prefers-reduced-motion" are computed-style facts jsdom cannot
 * produce — asserted here as the token classes (--icon-size-prominent /
 * motion-reduce:animate-none) that generate them; the real checks are the
 * manifest's pointer (coarse) and reduced-motion browser kinds. The glyph
 * (CircleNotch, the same shape as Button's pending indicator) is pixel data
 * owned by the screenshot gate.
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Spinner } from './spinner'

// The size/motion classes may sit on the status span or the svg — collect
// both class strings rather than pinning which element carries them. (SVG
// elements expose class via the class attribute, not a plain className.)
function spinnerClasses(container: HTMLElement): string {
  const root = container.firstElementChild as HTMLElement
  const svg = root.querySelector('svg')
  return [root.className, svg?.getAttribute('class') ?? ''].join(' ')
}

describe('Spinner — status semantics (§2.7)', () => {
  it('default renders a status region labelled "Loading"', () => {
    render(<Spinner />)
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument()
  })

  it('label names the status region', () => {
    render(<Spinner label="Fetching connectors" />)
    expect(screen.getByRole('status', { name: 'Fetching connectors' })).toBeInTheDocument()
  })

  it('decorative removes the status role and hides the glyph from the accessibility tree', () => {
    const { container } = render(<Spinner decorative />)
    expect(container.querySelector('[role="status"]')).toBeNull()
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeNull()
  })
})

describe('Spinner — motion and size classes', () => {
  it('spins, and stops under prefers-reduced-motion via motion-reduce:animate-none', () => {
    const { container } = render(<Spinner />)
    expect(spinnerClasses(container)).toContain('animate-spin')
    expect(spinnerClasses(container)).toContain('motion-reduce:animate-none')
  })

  it('default (sm) uses the 16px medium icon token', () => {
    const { container } = render(<Spinner />)
    expect(spinnerClasses(container)).toContain('--icon-size-medium')
  })

  it('lg uses the 24px prominent icon token (class proxy for the 24×24 box)', () => {
    const { container } = render(<Spinner size="lg" />)
    expect(spinnerClasses(container)).toContain('--icon-size-prominent')
    expect(spinnerClasses(container)).not.toContain('--icon-size-medium')
  })

  it('renders an svg glyph', () => {
    const { container } = render(<Spinner />)
    expect((container.firstElementChild as HTMLElement).querySelector('svg')).not.toBeNull()
  })

  it('className passes through', () => {
    // Passthrough class uses a token spacing value (closed scale, spacing lock).
    const { container } = render(<Spinner className="mr-[var(--space-2)]" />)
    expect(container.firstElementChild).toHaveClass('mr-[var(--space-2)]')
  })
})
