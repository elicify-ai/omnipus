import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Separator } from './separator'

describe('Separator semantics', () => {
  it('is decorative by default', () => {
    const { container } = render(<Separator />)
    expect(container.firstChild).toHaveAttribute('role', 'none')
  })

  it('announces a non-decorative vertical separator', () => {
    const { getByRole } = render(<Separator decorative={false} orientation="vertical" />)
    expect(getByRole('separator')).toHaveAttribute('aria-orientation', 'vertical')
  })
})
