/**
 * Callout — W2-1 contract §2.6 (R41 "error → Callout/Alert").
 *
 * RED on 69aa07d9a: `src/components/ui/callout.tsx` does not exist yet
 * (contract §2.6 names it as a new file) — fails at import for the right
 * reason.
 *
 * Arbitrary-value Tailwind classes write spaces as underscores
 * (`border-[color-mix(in_srgb,...)]`), so colour-mix assertions normalize
 * underscores back to spaces before matching the contract's CSS text.
 * Glyph identity (Info/Warning/WarningCircle/CheckCircle) is pixel data —
 * the screenshot gate (contract G8) owns it; unit scope asserts presence,
 * aria-hidden and the per-variant colour token.
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Button } from './button'
import { Callout, CalloutActions, CalloutDescription, CalloutTitle, calloutVariants } from './callout'

// Walk every element inside the root, normalizing Tailwind's underscore
// spaces so contract CSS values can be matched as written.
function classString(root: Element): string {
  let out = root.className.replace(/_/g, ' ')
  for (const el of root.querySelectorAll('*')) out += ' ' + el.className.replace(/_/g, ' ')
  return out
}

function renderCallout(
  variant?: 'info' | 'warning' | 'error' | 'success',
  props: { icon?: false; className?: string; 'data-testid'?: string } = {},
) {
  return render(
    <Callout variant={variant} {...props}>
      <CalloutTitle>Heads up</CalloutTitle>
      <CalloutDescription>Something happened.</CalloutDescription>
    </Callout>,
  )
}

describe('Callout — role per variant (§2.6)', () => {
  it('info (the default) is role="status"', () => {
    const { container } = renderCallout()
    expect(container.querySelector('[data-slot]')).toHaveAttribute('role', 'status')
  })

  it('success is role="status"', () => {
    const { container } = renderCallout('success')
    expect(container.querySelector('[data-slot]')).toHaveAttribute('role', 'status')
  })

  it('warning is role="alert"', () => {
    const { container } = renderCallout('warning')
    expect(container.querySelector('[data-slot]')).toHaveAttribute('role', 'alert')
  })

  it('error is role="alert"', () => {
    const { container } = renderCallout('error')
    expect(container.querySelector('[data-slot]')).toHaveAttribute('role', 'alert')
  })
})

describe('Callout — icon contract', () => {
  it('each variant renders a default icon that is aria-hidden and coloured with its variant token', () => {
    for (const variant of ['info', 'warning', 'error', 'success'] as const) {
      const { container, unmount } = renderCallout(variant)
      const root = container.firstElementChild as HTMLElement
      const svg = root.querySelector('svg')
      expect(svg, `variant ${variant} should render a default icon`).not.toBeNull()
      expect(svg).toHaveAttribute('aria-hidden', 'true')
      expect(classString(root)).toContain(`text-[var(--color-${variant})]`)
      unmount()
    }
  })

  it('icon={false} renders no icon at all', () => {
    const { container } = renderCallout('info', { icon: false })
    expect((container.firstElementChild as HTMLElement).querySelector('svg')).toBeNull()
  })
})

describe('Callout — variant presentation (§2.6 Look)', () => {
  it('all variants share the base layout classes', () => {
    const { container } = renderCallout('info')
    const root = container.firstElementChild as HTMLElement
    expect(root).toHaveClass(
      'flex',
      'items-start',
      'w-full',
      'rounded-[var(--radius-large)]',
      'border',
      'text-[length:var(--type-body-compact-size)]',
      'bg-[var(--color-surface-1)]',
    )
    expect(root).toHaveClass('gap-[var(--space-2-5)]')
    expect(root).toHaveClass('px-[var(--space-2-5)]')
    expect(root).toHaveClass('py-[var(--space-2)]')
  })

  it('info and success keep the plain border token', () => {
    for (const variant of ['info', 'success'] as const) {
      const { container, unmount } = renderCallout(variant)
      const root = container.firstElementChild as HTMLElement
      expect(root).toHaveClass('border-[var(--color-border)]')
      unmount()
    }
  })

  it('warning mixes its border 45% over the border token', () => {
    const { container } = renderCallout('warning')
    const root = container.firstElementChild as HTMLElement
    expect(classString(root)).toMatch(
      /color-mix\(in srgb,\s*var\(--color-warning\)\s*45%,\s*var\(--color-border\)\)/,
    )
  })

  it('error mixes its border 50% over the border token and its background 8% over surface-1', () => {
    const { container } = renderCallout('error')
    const root = container.firstElementChild as HTMLElement
    expect(classString(root)).toMatch(
      /color-mix\(in srgb,\s*var\(--color-error\)\s*50%,\s*var\(--color-border\)\)/,
    )
    expect(classString(root)).toMatch(
      /color-mix\(in srgb,\s*var\(--color-error\)\s*8%,\s*var\(--color-surface-1\)\)/,
    )
  })

  // Skill rule 8: on an error fill the near-black primary passes contrast,
  // white fails — the contract forbids text-white here explicitly.
  it('error never carries text-white', () => {
    const { container } = renderCallout('error')
    expect(classString(container.firstElementChild as HTMLElement)).not.toContain('text-white')
  })

  it('pins its own forced-colors repaint (forced-colors:border-[CanvasText])', () => {
    const { container } = renderCallout('info')
    const root = container.firstElementChild as HTMLElement
    expect(root).toHaveClass('forced-colors:border-[CanvasText]')
  })
})

describe('Callout — title, description, actions', () => {
  it('title is semibold secondary; description is muted', () => {
    const { container } = render(
      <Callout>
        <CalloutTitle>Heads up</CalloutTitle>
        <CalloutDescription>Something happened.</CalloutDescription>
      </Callout>,
    )
    const root = container.firstElementChild as HTMLElement
    const title = screen.getByText('Heads up')
    const desc = screen.getByText('Something happened.')
    expect(title).toHaveClass('font-semibold')
    expect(title).toHaveClass('text-[var(--color-secondary)]')
    expect(desc).toHaveClass('text-[var(--color-muted)]')
    expect(root).toContainElement(title)
  })

  it('CalloutActions is the ml-auto self-center row with the contract gap', () => {
    const { container } = render(
      <Callout>
        <CalloutActions data-testid="actions">
          <Button type="button" data-testid="act-a">Retry</Button>
          <Button type="button" data-testid="act-b">Dismiss</Button>
        </CalloutActions>
      </Callout>,
    )
    const actions = container.querySelector('[data-testid="actions"]')!
    expect(actions).toHaveClass('ml-auto', 'self-center', 'flex', 'gap-[var(--space-1)]')
  })

  it('actions sit in DOM order and are enabled (jsdom proxy for the keyboard/browser manifest kinds)', () => {
    const { container } = render(
      <Callout variant="error">
        <CalloutActions>
          <Button type="button" data-testid="act-a">Retry</Button>
          <Button type="button" data-testid="act-b">Dismiss</Button>
        </CalloutActions>
      </Callout>,
    )
    const a = screen.getByTestId('act-a')
    const b = screen.getByTestId('act-b')
    expect(Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
    expect(a).toBeEnabled()
    expect(b).toBeEnabled()
  })
})

describe('Callout — exports and passthrough', () => {
  it('exports calloutVariants covering the four variants', () => {
    expect(typeof calloutVariants).toBe('function')
    for (const variant of ['info', 'warning', 'error', 'success']) {
      expect(calloutVariants({ variant })).toBeTruthy()
    }
  })

  it('className and data-testid pass through to the root', () => {
    // Passthrough class uses a token spacing value (closed scale, spacing lock).
    const { container } = renderCallout('info', { className: 'mt-[var(--space-2)]', 'data-testid': 'notice' })
    const root = container.firstElementChild as HTMLElement
    expect(root).toHaveClass('mt-[var(--space-2)]')
    expect(root).toHaveAttribute('data-testid', 'notice')
  })
})
