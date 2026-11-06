/**
 * Empty — W2-1 contract §2.5 (R41, shadcn Empty interface).
 *
 * RED on 69aa07d9a: `src/components/ui/empty.tsx` does not exist yet
 * (contract §2.5 names it as a new file) — fails at import for the right
 * reason. EmptyState (the superseded component) is NOT touched here; it
 * stays published until W2-7 deletes it (§2.5 Relation).
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from './empty'
import { Button } from './button'

describe('Empty — slot contract', () => {
  it('every slot renders with its kebab-case data-slot', () => {
    const { container } = render(
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">⌀</EmptyMedia>
          <EmptyTitle>No connectors yet</EmptyTitle>
          <EmptyDescription>Connect a source to see records here.</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button type="button">Add connector</Button>
        </EmptyContent>
      </Empty>,
    )
    for (const slot of ['empty', 'empty-header', 'empty-media', 'empty-title', 'empty-description', 'empty-content']) {
      expect(container.querySelector(`[data-slot="${slot}"]`)).not.toBeNull()
    }
  })

  it('the root carries the dashed-border centered layout from the contract', () => {
    const { container } = render(<Empty />)
    const root = container.querySelector('[data-slot="empty"]')!
    expect(root).toHaveClass(
      'flex',
      'flex-col',
      'items-center',
      'text-center',
      'w-full',
      'rounded-[var(--radius-large)]',
      'border',
      'border-dashed',
      'border-[var(--color-border)]',
      'gap-[var(--space-2)]',
      'p-[var(--space-4)]',
    )
  })

  it('is static: no role, and no interactive element unless the caller adds one to EmptyContent', () => {
    const { container } = render(
      <Empty>
        <EmptyTitle>Nothing here</EmptyTitle>
      </Empty>,
    )
    const root = container.querySelector('[data-slot="empty"]')!
    expect(root).not.toHaveAttribute('role')
    expect(root.querySelectorAll('button, a, input, [role]')).toHaveLength(0)
  })

  it('accepts a caller Button in EmptyContent as the only interactive element', () => {
    render(
      <Empty>
        <EmptyContent>
          <Button type="button">Add connector</Button>
        </EmptyContent>
      </Empty>,
    )
    expect(screen.getByRole('button', { name: 'Add connector' })).toBeInTheDocument()
  })
})

describe('EmptyMedia — variants', () => {
  it('variant="icon" is a 40px surface-2 box with a muted aria-hidden glyph', () => {
    const { container } = render(
      <EmptyMedia variant="icon">
        <span>⌀</span>
      </EmptyMedia>,
    )
    const media = container.querySelector('[data-slot="empty-media"]')!
    // --space-6 = 40px (token G9) — the icon box size comes from the token.
    expect(media).toHaveClass('size-[var(--space-6)]')
    expect(media).toHaveClass('bg-[var(--color-surface-2)]')
    expect(media).toHaveClass('text-[var(--color-muted)]')
    expect(media).toHaveClass('rounded-[var(--radius-large)]')
    expect(media).toHaveAttribute('aria-hidden', 'true')
  })

  it('variant="default" does not claim aria-hidden (no icon box semantics)', () => {
    const { container } = render(
      <EmptyMedia variant="default">
        <span>logo</span>
      </EmptyMedia>,
    )
    const media = container.querySelector('[data-slot="empty-media"]')!
    expect(media).not.toHaveAttribute('aria-hidden')
    expect(media.className).not.toContain('size-[var(--space-6)]')
  })
})

describe('EmptyTitle / EmptyDescription / EmptyContent — typography', () => {
  it('EmptyTitle renders a div (never a heading) at body size, semibold, secondary', () => {
    const { container } = render(<EmptyTitle>No connectors yet</EmptyTitle>)
    const title = container.querySelector('[data-slot="empty-title"]')!
    // §2.5: "it renders a `div` by default (asChild-free) so the caller's
    // heading order is never broken."
    expect(title.tagName).toBe('DIV')
    expect(title).toHaveClass('text-[length:var(--type-body-size)]')
    expect(title).toHaveClass('font-semibold')
    expect(title).toHaveClass('text-[var(--color-secondary)]')
  })

  it('EmptyDescription renders a p at body-compact, muted', () => {
    const { container } = render(<EmptyDescription>Nothing to see yet.</EmptyDescription>)
    const desc = container.querySelector('[data-slot="empty-description"]')!
    expect(desc.tagName).toBe('P')
    expect(desc).toHaveClass('text-[length:var(--type-body-compact-size)]')
    expect(desc).toHaveClass('text-[var(--color-muted)]')
  })

  it('EmptyContent is an action row with the contract gap', () => {
    const { container } = render(<EmptyContent>x</EmptyContent>)
    const content = container.querySelector('[data-slot="empty-content"]')!
    expect(content).toHaveClass('gap-[var(--space-2)]')
  })

  it('className passes through on the root', () => {
    // Passthrough class uses a token spacing value (closed scale, spacing lock).
    const { container } = render(<Empty className="mt-[var(--space-4)]" />)
    const root = container.querySelector('[data-slot="empty"]')!
    expect(root).toHaveClass('mt-[var(--space-4)]')
  })
})
