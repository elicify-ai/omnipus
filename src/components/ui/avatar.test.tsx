import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Avatar, AvatarFallback, AvatarImage } from './avatar'

describe('Avatar presentation contract', () => {
  it.each(['sm', 'md', 'lg'] as const)('renders the %s size without adding interaction semantics', (size) => {
    const { container } = render(<Avatar size={size}><AvatarFallback>DP</AvatarFallback></Avatar>)
    expect(container.firstChild).not.toHaveAttribute('tabindex')
    expect(screen.getByText('DP')).toBeVisible()
  })

  it('preserves the image accessible name', () => {
    render(<Avatar><AvatarImage src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="Daniel" /></Avatar>)
    expect(screen.getByRole('img', { name: 'Daniel' })).toBeVisible()
  })

  it('supports an explicitly decorative image', () => {
    const { container } = render(<Avatar><AvatarImage alt="" src="avatar.png" /></Avatar>)
    expect(container.querySelector('img')).toHaveAttribute('alt', '')
  })
})
