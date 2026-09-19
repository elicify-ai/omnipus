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
})


describe('Badge error contrast contract', () => {
  it.each(['error', 'destructive'] as const)('uses the accessible label token while preserving the %s tint', (variant) => {
    render(<Badge variant={variant}>Attention</Badge>)
    expect(screen.getByText('Attention')).toHaveClass('text-[var(--badge-error-foreground)]', 'bg-[var(--color-error)]/20')
    expect(screen.getByText('Attention')).not.toHaveClass('text-[var(--color-error)]')
  })
})
