// A /clear shows its pending look ONLY while the server says it is held
// (message_status held, sent when the target agent is stopped). While it is
// merely in flight on a connected chat there is no status chip at all; offline
// the standard "not sent yet" copy is true and stays.
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { useConnectionStore } from '@/store/connection'
import { UserMessageDeliveryStatus } from './ConnectionStatus'
import { RowDeliveryStatus } from './RowDeliveryStatus'

const APPROVED = 'Not sent yet, will be sent automatically'
const setConnected = (isConnected: boolean) => act(() => { useConnectionStore.setState({ isConnected } as never) })
beforeEach(() => setConnected(true))

describe('/clear pending copy', () => {
  it('held: shows the approved text, and the hover says the current reply is being stopped first', () => {
    render(<UserMessageDeliveryStatus state="held" agentName="Mia" latest isClearCommand />)
    expect(screen.getByLabelText(APPROVED)).toBeInTheDocument()
    expect(screen.queryByLabelText(/Waiting/)).toBeNull()
    fireEvent.focus(screen.getByLabelText(APPROVED))
    expect(screen.getByRole('tooltip').textContent).toBe(
      "Stopping Mia's current reply first. Your /clear is applied as soon as it has stopped. Nothing has been cleared yet.",
    )
  })

  it('in flight on a connected chat (queued, not held): no chip and no "Waiting" label', () => {
    render(<UserMessageDeliveryStatus state="queued" agentName="Mia" latest isClearCommand />)
    expect(screen.queryByTestId('user-message-delivery-status')).toBeNull()
    expect(screen.queryByLabelText(/Waiting|Not sent yet/)).toBeNull()
  })

  it('offline: the standard not-sent-yet copy is true and stays', () => {
    setConnected(false)
    render(<UserMessageDeliveryStatus state="queued" agentName="Mia" latest isClearCommand />)
    expect(screen.getByLabelText(APPROVED)).toBeInTheDocument()
  })

  it('negative control: an ordinary queued message keeps the standard copy while connected', () => {
    render(<UserMessageDeliveryStatus state="queued" agentName="Mia" latest />)
    expect(screen.getByLabelText(APPROVED)).toBeInTheDocument()
  })

  it('negative control: an @-held message still reads "Waiting - delivered when ..."', () => {
    render(<UserMessageDeliveryStatus state="held" agentName="Mia" latest />)
    expect(screen.getByLabelText('Waiting — delivered when Mia runs again')).toBeInTheDocument()
  })

  it('a held /clear row names the chat\'s own agent, never "the addressed agent"', () => {
    render(<RowDeliveryStatus content="/clear" state="held" agentName="Jim" latest isClearCommand />)
    expect(screen.getByTestId('user-message-delivery-status').textContent ?? '').not.toMatch(/addressed agent/)
    expect(screen.getByLabelText(APPROVED)).toBeInTheDocument()
  })
})
