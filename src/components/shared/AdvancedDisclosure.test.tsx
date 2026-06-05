/**
 * AdvancedDisclosure.test.tsx — Issue #316 ACs.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdvancedDisclosure } from './AdvancedDisclosure'

describe('AdvancedDisclosure', () => {
  it('renders the title in the trigger button', () => {
    render(
      <AdvancedDisclosure title="Advanced options">
        <p>hidden content</p>
      </AdvancedDisclosure>,
    )
    expect(screen.getByRole('button', { name: /advanced options/i })).toBeInTheDocument()
  })

  it('defaults to closed — children are NOT in the document', () => {
    render(
      <AdvancedDisclosure>
        <p data-testid="child">hidden child</p>
      </AdvancedDisclosure>,
    )
    expect(screen.queryByTestId('child')).not.toBeInTheDocument()
  })

  it('mounts children only after expanding', async () => {
    const user = userEvent.setup()
    render(
      <AdvancedDisclosure title="More">
        <p data-testid="child">revealed content</p>
      </AdvancedDisclosure>,
    )
    expect(screen.queryByTestId('child')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /more/i }))
    expect(screen.getByTestId('child')).toBeInTheDocument()
  })

  it('collapses again when triggered a second time', async () => {
    const user = userEvent.setup()
    render(
      <AdvancedDisclosure title="Toggle">
        <p data-testid="child">content</p>
      </AdvancedDisclosure>,
    )
    const trigger = screen.getByRole('button', { name: /toggle/i })
    await user.click(trigger)
    expect(screen.getByTestId('child')).toBeInTheDocument()
    await user.click(trigger)
    expect(screen.queryByTestId('child')).not.toBeInTheDocument()
  })

  it('shows summary text in collapsed header when provided', () => {
    render(
      <AdvancedDisclosure title="Advanced" summary="Safe defaults applied">
        <p>content</p>
      </AdvancedDisclosure>,
    )
    expect(screen.getByText('Safe defaults applied')).toBeInTheDocument()
  })

  it('hides summary when disclosure is open', async () => {
    const user = userEvent.setup()
    render(
      <AdvancedDisclosure title="Advanced" summary="Safe defaults applied">
        <p>content</p>
      </AdvancedDisclosure>,
    )
    expect(screen.getByText('Safe defaults applied')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /advanced/i }))
    expect(screen.queryByText('Safe defaults applied')).not.toBeInTheDocument()
  })

  it('starts open when defaultOpen={true}', () => {
    render(
      <AdvancedDisclosure defaultOpen>
        <p data-testid="child">open by default</p>
      </AdvancedDisclosure>,
    )
    expect(screen.getByTestId('child')).toBeInTheDocument()
  })

  it('sets aria-expanded correctly', async () => {
    const user = userEvent.setup()
    render(
      <AdvancedDisclosure title="Section">
        <p>content</p>
      </AdvancedDisclosure>,
    )
    const trigger = screen.getByRole('button', { name: /section/i })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await user.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
  })
})
