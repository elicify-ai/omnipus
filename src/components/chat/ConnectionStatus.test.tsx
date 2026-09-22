import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  AssistantConnectionStatus,
  ChatConnectionNotice,
  ChatConnectionStatusLine,
  UserMessageDeliveryStatus,
  deriveConnectionDisplay,
} from './ConnectionStatus'
import { useConnectionStore } from '@/store/connection'

afterEach(() => {
  vi.useRealTimers()
  useConnectionStore.setState({
    isConnected: true,
    reconnectPhase: null,
    reconnectAttempt: 0,
    disconnectedAt: null,
    reconnectedAt: null,
    lastDisconnectDurationMs: null,
    lastDisconnectWasTerminal: false,
    disconnectedAssistantMessageId: null,
  })
})

describe('UserMessageDeliveryStatus', () => {
  it.each([
    ['queued', 'Not sent yet, will be sent automatically', "Not sent yet — it will be sent automatically as soon as you're back online."],
    ['received', 'Received', 'Received by Omnipus. Mia will pick it up after finishing the current step.'],
    ['working', 'Mia is working on it', 'Mia is working on this.'],
  ] as const)('renders the %s icon with its exact accessible label and tooltip', (state, label, tooltip) => {
    render(<UserMessageDeliveryStatus state={state} agentName="Mia" latest />)

    const icon = screen.getByRole('button', { name: label })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toHaveTextContent(tooltip)
  })

  it('keeps failed text and exposes the exact retry action without error colours', () => {
    const onRetry = vi.fn()
    const { container } = render(
      <UserMessageDeliveryStatus state="failed" agentName="Mia" latest onRetry={onRetry} />,
    )

    expect(screen.getByText("Couldn't be sent")).toBeVisible()
    const retry = screen.getByRole('button', { name: "Couldn't be sent. Try again, button" })
    fireEvent.focus(retry)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      "This message couldn't be delivered. Your text is kept — click to try again.",
    )
    fireEvent.click(retry)
    expect(onRetry).toHaveBeenCalledOnce()
    expect(container.innerHTML).not.toMatch(/color-(?:error|warning)|\bred-|\borange-|\bamber-/)
  })
})

describe('AssistantConnectionStatus', () => {
  it('shows the exact quiet continuation state after a long drop', () => {
    render(<AssistantConnectionStatus state="paused" agentName="Mia" />)

    expect(screen.getByTestId('assistant-connection-status')).toHaveTextContent(
      'Mia is still working on this — the rest appears when you\'re connected again.',
    )
    const icon = screen.getByRole('button', {
      name: 'Mia is still working. The rest appears when the connection is back.',
    })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'Your connection dropped. Mia keeps working in the background; nothing is lost.',
    )
  })

  it('shows Generate again when the answer can no longer resume', () => {
    const onGenerateAgain = vi.fn()
    render(
      <AssistantConnectionStatus
        state="unfinished"
        agentName="Mia"
        onGenerateAgain={onGenerateAgain}
      />,
    )

    expect(screen.getByText("This answer couldn't be finished")).toBeVisible()
    const generate = screen.getByRole('button', { name: "Answer couldn't be finished. Generate again, button" })
    fireEvent.focus(generate)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'The connection was gone for too long to continue this answer. Generate it again to get a complete reply.',
    )
    fireEvent.click(generate)
    expect(onGenerateAgain).toHaveBeenCalledOnce()
  })
})

describe('ChatConnectionStatusLine', () => {
  it.each([
    [
      'offline',
      'No internet connection. Your agents keep working.',
      'No internet connection. Your agents keep working.',
      "Your device isn't connected to the internet. Omnipus reconnects on its own when it's back — your messages and your agents' work are kept.",
    ],
    [
      'unreachable',
      "Can't reach Omnipus right now. Your agents keep working.",
      "Can't reach Omnipus right now. Your agents keep working.",
      "Omnipus isn't responding at the moment. It will keep trying on its own — your messages and your agents' work are kept.",
    ],
  ] as const)('renders the exact %s copy, screen-reader label, and tooltip', (state, copy, label, tooltip) => {
    render(<ChatConnectionStatusLine state={state} />)
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent(copy)
    const icon = screen.getByRole('button', { name: label })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toHaveTextContent(tooltip)
  })

  it('renders the exact recovery confirmation', () => {
    render(<ChatConnectionStatusLine state="back" />)
    expect(screen.getByRole('status')).toHaveTextContent('Up to date')
  })
})

describe('deriveConnectionDisplay', () => {
  const base = {
    isConnected: false,
    reconnectPhase: 'reconnecting' as const,
    disconnectedAt: 1_000,
    reconnectedAt: null,
    lastDisconnectDurationMs: null,
    lastDisconnectWasTerminal: false,
    hasInterruptedAnswer: true,
    deviceOnline: true,
  }

  it('shows nothing for a drop shorter than 15 seconds', () => {
    expect(deriveConnectionDisplay({ ...base, now: 15_999 })).toEqual({ answer: 'hidden', chat: 'hidden' })
  })

  it('shows only the answer continuation state at 15 seconds', () => {
    expect(deriveConnectionDisplay({ ...base, now: 16_000 })).toEqual({ answer: 'paused', chat: 'hidden' })
  })

  it('distinguishes device offline from an unreachable gateway at two minutes', () => {
    expect(deriveConnectionDisplay({ ...base, now: 121_000, deviceOnline: false }).chat).toBe('offline')
    expect(deriveConnectionDisplay({ ...base, now: 121_000, deviceOnline: true }).chat).toBe('unreachable')
  })

  it('escalates immediately when retries give up', () => {
    expect(deriveConnectionDisplay({ ...base, now: 2_000, reconnectPhase: 'gave_up' })).toEqual({
      answer: 'unfinished',
      chat: 'unreachable',
    })
  })

  it('shows Up to date only after a state that was visible, and only for two seconds', () => {
    const recovered = {
      ...base,
      isConnected: true,
      reconnectPhase: null,
      disconnectedAt: null,
      reconnectedAt: 50_000,
      lastDisconnectDurationMs: 15_000,
    }
    expect(deriveConnectionDisplay({ ...recovered, now: 51_999 })).toEqual({ answer: 'hidden', chat: 'back' })
    expect(deriveConnectionDisplay({ ...recovered, now: 52_000 })).toEqual({ answer: 'hidden', chat: 'hidden' })
    expect(deriveConnectionDisplay({ ...recovered, now: 51_000, lastDisconnectDurationMs: 14_999 })).toEqual({
      answer: 'hidden',
      chat: 'hidden',
    })
  })
})

describe('ChatConnectionNotice timing', () => {
  it('waits two minutes, then shows the calm line and a two-second recovery confirmation', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-22T00:00:00Z'))
    const disconnectedAt = Date.now()
    useConnectionStore.setState({
      isConnected: false,
      reconnectPhase: 'reconnecting',
      disconnectedAt,
      disconnectedAssistantMessageId: 'assistant-1',
    })

    render(<ChatConnectionNotice />)
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()

    act(() => vi.advanceTimersByTime(120_000))
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent(
      "Can't reach Omnipus right now. Your agents keep working. · Try again",
    )

    act(() => {
      useConnectionStore.setState({
        isConnected: true,
        reconnectPhase: null,
        disconnectedAt: null,
        reconnectedAt: Date.now(),
        lastDisconnectDurationMs: 120_000,
      })
    })
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent('Up to date')

    act(() => vi.advanceTimersByTime(2_000))
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
  })
})
