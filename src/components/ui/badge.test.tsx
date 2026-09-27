import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Badge } from './badge'

describe('Badge presentation contract', () => {
  it('maps the default filled badge to a readable forced-colors pair with a contrasting boundary', () => {
    render(<Badge>State</Badge>)
    expect(screen.getByText('State')).toHaveClass(
      'forced-colors:border-[CanvasText]',
      'forced-colors:bg-[ButtonFace]',
      'forced-colors:text-[ButtonText]',
    )
  })

  it.each(['default', 'secondary', 'outline', 'success', 'error', 'destructive', 'warning', 'muted'] as const)(
    'renders the %s variant as non-interactive status text',
    (variant) => {
      render(<Badge variant={variant}>State</Badge>)
      expect(screen.getByText('State')).not.toHaveAttribute('role', 'button')
    },
  )

  // The Tooltip forced-colors check (`tooltip.stories.tsx`) embeds
  // `<Badge variant="warning">` and Storybook's automatic a11y reporter
  // scans it with `forced-colors: active` emulated: the "warning" variant's
  // author color (`text-[var(--color-warning)]` on a ~white forced Canvas)
  // measured 1.91:1, failing axe's `color-contrast` (serious) — WCAG AA
  // requires 4.5:1. Every coloured variant carried the same defect; only
  // `default` had a forced-colors repaint. Each status-tinted variant
  // (success/error/destructive/warning) now maps to the same `ButtonFace`/
  // `ButtonText` filled pair as `default`, and each neutral variant
  // (secondary/outline/muted) maps to a `Canvas`/`CanvasText` pair — so a
  // warning badge is never visually identical to a neutral one under
  // forced colors, distinguished by system-color fill, not hue.
  it.each(['success', 'error', 'destructive', 'warning'] as const)(
    'maps the %s status variant to the same readable forced-colors pair as default',
    (variant) => {
      render(<Badge variant={variant}>State</Badge>)
      expect(screen.getByText('State')).toHaveClass(
        'forced-colors:border-[CanvasText]',
        'forced-colors:bg-[ButtonFace]',
        'forced-colors:text-[ButtonText]',
      )
    },
  )

  it.each(['secondary', 'outline', 'muted'] as const)(
    'maps the %s neutral variant to a distinct Canvas/CanvasText forced-colors pair',
    (variant) => {
      render(<Badge variant={variant}>State</Badge>)
      expect(screen.getByText('State')).toHaveClass(
        'forced-colors:border-[CanvasText]',
        'forced-colors:bg-[Canvas]',
        'forced-colors:text-[CanvasText]',
      )
    },
  )

  it('renders the warning badge with a different forced-colors background fill than a neutral badge', () => {
    render(<Badge variant="warning">Auto — no sandbox</Badge>)
    render(<Badge variant="secondary">Neutral</Badge>)
    expect(screen.getByText('Auto — no sandbox')).toHaveClass('forced-colors:bg-[ButtonFace]')
    expect(screen.getByText('Neutral')).toHaveClass('forced-colors:bg-[Canvas]')
    expect(screen.getByText('Auto — no sandbox')).not.toHaveClass('forced-colors:bg-[Canvas]')
  })
})


describe('Badge error contrast contract', () => {
  it.each(['error', 'destructive'] as const)('uses the accessible label token while preserving the %s tint', (variant) => {
    render(<Badge variant={variant}>Attention</Badge>)
    expect(screen.getByText('Attention')).toHaveClass('text-[color:var(--badge-error-foreground)]', 'bg-[var(--color-error)]/20')
    expect(screen.getByText('Attention')).not.toHaveClass('text-[var(--color-error)]')
  })
})
