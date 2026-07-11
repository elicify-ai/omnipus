// BrowserLiveView.controlToggle.test.tsx — ADR-038 D5/D6 take/release control
// toggle behaviour. Mocks BrowserLiveWsConnection entirely (unit-tests the
// component's reaction to callbacks, not the real WS transport — that's
// covered separately in src/lib/browserLiveWs.test.ts).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

const { mockSendControl, mockSendInput, mockConnect, mockDetach, mockClose, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(),
  mockSendInput: vi.fn(),
  mockConnect: vi.fn(),
  mockDetach: vi.fn(),
  mockClose: vi.fn(),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

vi.mock('@/lib/browserLiveWs', () => ({
  BrowserLiveWsConnection: vi.fn().mockImplementation(
    function (_sessionId: string, _agentId: string, callbacks: BrowserLiveWsCallbacks) {
      callbacksRef.current = callbacks
      return {
        connect: mockConnect,
        detach: mockDetach,
        close: mockClose,
        sendInput: mockSendInput,
        sendControl: mockSendControl,
        isConnected: true,
      }
    },
  ),
}))

import { BrowserLiveView } from './BrowserLiveView'

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

describe('BrowserLiveView — control toggle', () => {
  it('starts in the "Connecting…" pill state with the take-control button disabled', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Connecting…')
    expect(screen.getByRole('button', { name: /take control/i })).toBeDisabled()
  })

  it('enables Take control once onConnected fires, and clicking it sends control:take', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })

    const button = screen.getByRole('button', { name: /take control/i })
    expect(button).not.toBeDisabled()

    fireEvent.click(button)
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })

  it('reflects a browser_status "controlling" frame as "You\'re driving" / Release control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent("You're driving")

    const button = screen.getByRole('button', { name: /release control/i })
    fireEvent.click(button)
    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  it('reverts to "Agent driving" once status moves back to released', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Agent driving')
    expect(screen.getByRole('button', { name: /take control/i })).toBeInTheDocument()
  })

  it('shows an error pill + message when a server error frame arrives', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onError?.('session not found')
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Error')
    expect(screen.getByText('session not found')).toBeInTheDocument()
  })

  it('calls detach() then close() on unmount so the backend engine can ref-count down', () => {
    const { unmount } = render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    unmount()
    expect(mockDetach).toHaveBeenCalledTimes(1)
    expect(mockClose).toHaveBeenCalledTimes(1)
  })

  it('renders the Pop out / Close buttons only when the corresponding callback prop is provided', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    expect(screen.queryByRole('button', { name: /pop out/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /close live browser panel/i })).not.toBeInTheDocument()

    rerender(<BrowserLiveView sessionId="s1" agentId="a1" onPopOut={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByRole('button', { name: /pop out/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /close live browser panel/i })).toBeInTheDocument()
  })
})
