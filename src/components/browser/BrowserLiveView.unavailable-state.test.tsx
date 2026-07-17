// BrowserLiveView.unavailable-state.test.tsx — Test 21 (live-browser-video-
// streaming spec, TDD plan). FR-007/FR-018/FR-020/US-5: when the live view
// concludes it cannot/will not produce video for this viewer (no
// VideoDecoder, a bring-up timeout, or a declined negotiation — all surfaced
// via browserLiveWs's `onUnavailable` callback), the panel MUST show a
// GENERIC message, with chrome controls (header, omnibox) still operable,
// and MUST NOT fall back to the legacy JPEG (`<img>`/canvas-with-screencast-
// content) or any synthetic ("A1") rendering.
//
// Mocks BrowserLiveWsConnection the same way every sibling BrowserLiveView
// test file does (capturing the callbacks object passed to its constructor),
// matching BrowserLiveView.takeTheWheel.test.tsx / .mouseMoveThrottle.test.tsx.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useChatStore } from '@/store/chat'

const { mockSendControl, mockSendInput, mockSendTabAction, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  mockSendTabAction: vi.fn(() => true),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

vi.mock('@/lib/browserLiveWs', () => ({
  BrowserLiveWsConnection: vi.fn().mockImplementation(
    function (_sessionId: string, _agentId: string, callbacks: BrowserLiveWsCallbacks) {
      callbacksRef.current = callbacks
      return {
        connect: vi.fn(),
        detach: vi.fn(),
        close: vi.fn(),
        sendInput: mockSendInput,
        sendControl: mockSendControl,
        sendTabAction: mockSendTabAction,
        isConnected: true,
      }
    },
  ),
}))

import { BrowserLiveView } from './BrowserLiveView'

const initialChatState = useChatStore.getState()

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

afterEach(() => {
  vi.restoreAllMocks()
  useChatStore.setState(initialChatState, true)
})

describe('BrowserLiveView — unavailable state (FR-007/FR-018/FR-020)', () => {
  it('renders the generic FR-007 message when onUnavailable fires (e.g. no VideoDecoder in this browser)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" onClose={vi.fn()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onUnavailable?.('no-video-decoder')
    })

    expect(screen.getByTestId('browser-live-unavailable')).toHaveTextContent('Live view needs a video-capable browser')
  })

  it('renders the SAME generic message regardless of the internal reason (bring-up timeout vs. declined negotiation) — the specific cause never leaks to the UI (O-3)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onUnavailable?.('video-decoder-configure-failed')
    })

    const message = screen.getByTestId('browser-live-unavailable')
    expect(message).toHaveTextContent('Live view needs a video-capable browser')
    expect(message.textContent).not.toMatch(/decoder|configure|timeout|kill.switch/i)
  })

  it('SF-H1: a mid-stream fatal video decode error (browserLiveWs.ts drops to onUnavailable(\'video-decode-error\')) shows the same generic unavailable banner rather than freezing the last painted frame forever', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      // A live stream was already up — proven by a screencast frame — before
      // the fatal decode error arrives, unlike the other cases above which
      // fire before any picture ever painted.
      callbacksRef.current?.onScreencast?.({
        type: 'browser_screencast',
        session_id: 's1',
        seq: 1,
        data: 'AAAA',
        width: 1280,
        height: 720,
      })
    })
    expect(screen.getByTestId('browser-live-frame')).toBeInTheDocument()

    act(() => {
      callbacksRef.current?.onUnavailable?.('video-decode-error')
    })

    const message = screen.getByTestId('browser-live-unavailable')
    expect(message).toHaveTextContent('Live view needs a video-capable browser')
    expect(message.textContent).not.toMatch(/decode|codec|closed/i)
    // The prior live frame surface must be gone, not left frozen underneath.
    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()
  })

  it('never renders the live frame/canvas surface (no JPEG, no A1 fallback) while unavailable', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onUnavailable?.('bring-up-timeout')
    })

    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()
    expect(document.querySelector('canvas')).not.toBeInTheDocument()
    expect(document.querySelector('img[alt="Live browser session"]')).not.toBeInTheDocument()
  })

  it('keeps chrome controls operable while unavailable: address bar, back/refresh, and Close all render and respond', () => {
    const onClose = vi.fn()
    render(<BrowserLiveView sessionId="s1" agentId="a1" onClose={onClose} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onUnavailable?.('bring-up-timeout')
    })

    const addressBar = screen.getByLabelText('Address bar') as HTMLInputElement
    expect(addressBar).not.toBeDisabled()
    fireEvent.change(addressBar, { target: { value: 'example.com' } })
    expect(addressBar.value).toBe('example.com')

    expect(screen.getByLabelText('Go back')).not.toBeDisabled()
    expect(screen.getByLabelText('Refresh page')).not.toBeDisabled()

    fireEvent.click(screen.getByLabelText('Close live browser panel'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('recovers from the unavailable state once a real screencast frame arrives (a fresh connection proving video does work after all)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onUnavailable?.('bring-up-timeout')
    })
    expect(screen.getByTestId('browser-live-unavailable')).toBeInTheDocument()

    act(() => {
      callbacksRef.current?.onScreencast?.({
        type: 'browser_screencast',
        session_id: 's1',
        seq: 1,
        data: 'AAAA',
        width: 1280,
        height: 720,
      })
    })

    expect(screen.queryByTestId('browser-live-unavailable')).not.toBeInTheDocument()
    expect(screen.getByTestId('browser-live-frame')).toBeInTheDocument()
  })

  it('clears the unavailable state on disconnect so a reconnect starts clean rather than showing a stale verdict', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onUnavailable?.('bring-up-timeout')
    })
    expect(screen.getByTestId('browser-live-unavailable')).toBeInTheDocument()

    act(() => {
      callbacksRef.current?.onDisconnected?.()
    })

    expect(screen.queryByTestId('browser-live-unavailable')).not.toBeInTheDocument()
    expect(screen.getByText('Reconnecting…')).toBeInTheDocument()
  })

  it('does not render the unavailable message before any onUnavailable call (ordinary connecting spinner shows instead)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })

    expect(screen.queryByTestId('browser-live-unavailable')).not.toBeInTheDocument()
    expect(screen.getByText('Waiting for the first frame…')).toBeInTheDocument()
  })
})
