// F1 (founder ruling): a message @-sent to a stopped agent shows a "held"
// tick - waiting, not failed - until the agent runs again.
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UserMessageDeliveryStatus } from './ConnectionStatus'

describe('F1: a message held for a stopped agent', () => {
  it('shows the waiting label with a clock, passive, no retry action', () => {
    const { container } = render(<UserMessageDeliveryStatus state="held" agentName="Mia" latest />)
    expect(screen.getByTestId('user-message-delivery-status')).toHaveAttribute('role', 'status')
    expect(screen.getByLabelText('Waiting — delivered when Mia runs again')).toBeInTheDocument()
    expect(screen.queryByTestId('user-message-retry')).toBeNull()
    expect(container.querySelector('svg[aria-hidden="true"]')).not.toBeNull()
  })

  it('names the agent it waits for', () => {
    render(<UserMessageDeliveryStatus state="held" agentName="Ava" latest />)
    expect(screen.getByLabelText('Waiting — delivered when Ava runs again')).toBeInTheDocument()
  })

  it('negative control: received keeps its own label', () => {
    render(<UserMessageDeliveryStatus state="received" agentName="Mia" latest />)
    expect(screen.queryByLabelText(/Waiting/)).toBeNull()
    expect(screen.getByLabelText('Received')).toBeInTheDocument()
  })
})
