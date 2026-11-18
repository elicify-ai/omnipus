/**
 * PageHeader — W2-1 contract §2.4 (R40, R36, R38).
 *
 * RED on 69aa07d9a: `src/components/ui/page-header.tsx` does not exist yet
 * (contract §2.4 names it as a new file) — fails at import for the right
 * reason.
 *
 * The 21px title size: jsdom cannot resolve a stylesheet var() into computed
 * px, so the unit suite pins the chain that makes it 21px — the h1 carries
 * `text-[length:var(--type-page-title-size)]` (never a literal, never
 * text-2xl) and the generated token is 1.5rem at the 14px root
 * (`src/styles/page-title-token.test.ts`). 1.5rem × 14px = 21px, the
 * founder's R40 value. The computed-px rows of §2.4 "Assertable" (21px at
 * default, 30px at --user-font-size: 20px, 18px at 12px) belong to the
 * manifest's root-size browser kind.
 */
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Button } from './button'
import { PageHeader } from './page-header'

describe('PageHeader — landmark and title', () => {
  it('renders a div[data-slot="page-header"] that is NOT a header/banner landmark', () => {
    const { container } = render(<PageHeader title="Settings" />)
    const root = container.querySelector('div[data-slot="page-header"]')
    expect(root).not.toBeNull()
    expect(root!.tagName).toBe('DIV')
    expect(container.querySelector('header')).toBeNull()
    expect(screen.queryByRole('banner')).toBeNull()
  })

  it('renders exactly one h1 with the token-driven title classes and no text-2xl', () => {
    const { container } = render(<PageHeader title="Settings" />)
    const h1s = container.querySelectorAll('h1')
    expect(h1s).toHaveLength(1)
    const h1 = h1s[0]
    expect(h1).toHaveTextContent('Settings')
    // §2.4 Title — every value comes from the page-title token family; no
    // literal size and no legacy text-2xl (R40, founder 2026-10-06).
    expect(h1).toHaveClass('text-[length:var(--type-page-title-size)]')
    expect(h1).toHaveClass('font-[family-name:var(--type-page-title-family)]')
    expect(h1).toHaveClass('font-[var(--type-page-title-weight)]')
    expect(h1).toHaveClass('leading-[var(--type-page-title-line-height)]')
    expect(h1).toHaveClass('tracking-[var(--type-page-title-letter-spacing)]')
    expect(h1).toHaveClass('text-[var(--color-secondary)]')
    expect(h1.className).not.toContain('text-2xl')
  })

  it('uses titleId as the h1 id when given', () => {
    render(<PageHeader title="Settings" titleId="settings-title" />)
    expect(screen.getByText('Settings')).toHaveAttribute('id', 'settings-title')
  })
})

describe('PageHeader — description', () => {
  it('renders the description as a styled p after the title', () => {
    const { container } = render(
      <PageHeader title="Settings" description="Tune how the assistant behaves." />,
    )
    const p = container.querySelector('p')
    expect(p).not.toBeNull()
    expect(p).toHaveTextContent('Tune how the assistant behaves.')
    expect(p).toHaveClass('text-[length:var(--type-body-compact-size)]')
    expect(p).toHaveClass('text-[var(--color-muted)]')
    expect(p).toHaveClass('mt-[var(--space-0-5)]')
    const h1 = container.querySelector('h1')!
    // Description follows the title in DOM order.
    expect(Boolean(h1.compareDocumentPosition(p!) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
  })
})

describe('PageHeader — actions', () => {
  it('renders no actions wrapper when there are no actions (no empty ml-auto container)', () => {
    const { container } = render(<PageHeader title="Settings" />)
    expect(container.querySelector('.ml-auto')).toBeNull()
  })

  it('keeps the primary action as the LAST action and wraps actions in the ml-auto row', () => {
    const { container } = render(
      <PageHeader
        title="Settings"
        actions={<Button type="button" data-testid="secondary-action">Export</Button>}
        primaryAction={<Button type="button" data-testid="primary-action">Save</Button>}
      />,
    )
    const row = container.querySelector('.ml-auto')
    expect(row).not.toBeNull()
    expect(row).toHaveClass('gap-[var(--space-2)]')
    const buttons = within(row as HTMLElement).getAllByRole('button')
    expect(buttons.length).toBe(2)
    expect(buttons[buttons.length - 1]).toHaveAttribute('data-testid', 'primary-action')
    expect(buttons[0]).toHaveAttribute('data-testid', 'secondary-action')
  })

  it('renders actions alone without a primary action', () => {
    render(
      <PageHeader
        title="Settings"
        actions={<Button type="button">Export</Button>}
      />,
    )
    expect(screen.getByRole('button', { name: 'Export' })).toBeInTheDocument()
  })
})

describe('PageHeader — leading slot and layout', () => {
  it('renders leading content BEFORE the title (e.g. the drawer menu button)', () => {
    const { container } = render(
      <PageHeader title="Settings" leading={<span data-testid="leading-slot">☰</span>} />,
    )
    const leading = screen.getByTestId('leading-slot')
    const h1 = container.querySelector('h1')!
    // leading precedes the title in DOM order.
    expect(Boolean(leading.compareDocumentPosition(h1) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
  })

  it('carries the wrapping flex layout so actions fold under the title on narrow viewports', () => {
    const { container } = render(
      <PageHeader
        title="Settings"
        actions={<Button type="button">Export</Button>}
        primaryAction={<Button type="button">Save</Button>}
      />,
    )
    const root = container.querySelector('div[data-slot="page-header"]')!
    // Class proxy for §2.4 "At 320px the actions wrap under the title with
    // no overflow" — the real reflow row is the manifest's reflow kind.
    expect(root).toHaveClass('flex', 'flex-wrap', 'items-start', 'gap-[var(--space-3)]')
  })
})
