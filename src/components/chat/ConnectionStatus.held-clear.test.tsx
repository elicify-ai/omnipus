// R2-S3-03: a /clear the server holds shows the queued look, but while the
// connection is up the wording must not talk about being "back online".
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { useConnectionStore } from '@/store/connection'
import { UserMessageDeliveryStatus } from './ConnectionStatus'

const setConnected = (isConnected: boolean) => act(() => { useConnectionStore.setState({ isConnected } as never) })
beforeEach(() => setConnected(true))

describe('queued /clear wording', () => {
  it('connected: says the clear is waiting for the agent, not for connectivity', () => {
    render(<UserMessageDeliveryStatus state="queued" agentName="Mia" latest isClearCommand />)
    expect(screen.getByLabelText('Waiting — your /clear is applied when Mia runs it')).toBeInTheDocument()
    expect(screen.queryByLabelText(/online|automatically/)).toBeNull()
  })

  it('offline: keeps the connectivity wording', () => {
    setConnected(false)
    render(<UserMessageDeliveryStatus state="queued" agentName="Mia" latest isClearCommand />)
    expect(screen.getByLabelText('Not sent yet, will be sent automatically')).toBeInTheDocument()
  })

  it('negative control: an ordinary queued message keeps the connectivity wording even when connected', () => {
    render(<UserMessageDeliveryStatus state="queued" agentName="Mia" latest />)
    expect(screen.getByLabelText('Not sent yet, will be sent automatically')).toBeInTheDocument()
  })
})
