// BrowserLiveView.tabStrip.test.tsx — ADR-041 D4 (live-panel tab strip)
// coverage: rendering the strip from a `browser_tabs` frame, the active-tab
// highlight, switch/close/open actions calling `sendTabAction` with the
// right shape, and the strip reconciling to a fresh `browser_tabs` frame.
// Mocks BrowserLiveWsConnection the same way the sibling BrowserLiveView
// test files do (controlToggle/takeTheWheel/annotateAndBar/mouseMoveThrottle).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

const { mockSendTabAction, mockSendControl, mockSendInput, mockConnect, mockDetach, mockClose, callbacksRef } =
  vi.hoisted(() => ({
    mockSendTabAction: vi.fn(() => true),
    mockSendControl: vi.fn(() => true),
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
        sendTabAction: mockSendTabAction,
        isConnected: true,
      }
    },
  ),
}))

import { BrowserLiveView } from './BrowserLiveView'

function connectAndFrame() {
  act(() => {
    callbacksRef.current?.onConnected?.()
    callbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq: 1,
      data: 'AAAA',
      width: 1280,
      height: 720,
    })
  })
}

function emitTabs(activeIndex: number, tabs: Array<{ index: number; title?: string; url?: string; active?: boolean }>) {
  act(() => {
    callbacksRef.current?.onTabs?.({
      type: 'browser_tabs',
      session_id: 's1',
      active_index: activeIndex,
      tabs,
    })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

describe('BrowserLiveView — tab strip (ADR-041 D4)', () => {
  it('renders nothing before any browser_tabs frame has arrived', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.queryByTestId('browser-tab-strip')).not.toBeInTheDocument()
  })

  it('renders a strip entry per tab, even for a single tab', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', url: 'https://example.com', active: true }])

    expect(screen.getByTestId('browser-tab-strip')).toBeInTheDocument()
    expect(screen.getByTestId('browser-tab-0')).toHaveTextContent('Only tab')
  })

  it('highlights the active tab (aria-selected) and not the inactive ones', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(1, [
      { index: 0, title: 'First', url: 'https://example.com' },
      { index: 1, title: 'Second', url: 'https://example.org' },
    ])

    expect(screen.getByTestId('browser-tab-0')).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByTestId('browser-tab-1')).toHaveAttribute('aria-selected', 'true')
  })

  it('falls back to the URL hostname when title is absent', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, url: 'https://sub.example.com/path?x=1', active: true }])

    expect(screen.getByTestId('browser-tab-0')).toHaveTextContent('sub.example.com')
  })

  it('falls back to "New tab" when both title and url are absent', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, active: true }])

    expect(screen.getByTestId('browser-tab-0')).toHaveTextContent('New tab')
  })

  it('clicking a tab calls sendTabAction("switch", index)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    fireEvent.click(screen.getByTestId('browser-tab-1'))
    expect(mockSendTabAction).toHaveBeenCalledWith('switch', 1)
  })

  it('clicking a tab\'s close ✕ calls sendTabAction("close", index) without also switching to it', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    fireEvent.click(screen.getByTestId('browser-tab-close-1'))
    expect(mockSendTabAction).toHaveBeenCalledWith('close', 1)
    expect(mockSendTabAction).not.toHaveBeenCalledWith('switch', 1)
  })

  it('clicking the ＋ new-tab button calls sendTabAction("open") with no index', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', active: true }])

    fireEvent.click(screen.getByTestId('browser-tab-new'))
    expect(mockSendTabAction).toHaveBeenCalledWith('open')
  })

  it('reconciles the strip to a fresh browser_tabs frame (e.g. after a switch/close/open)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])
    expect(screen.getByTestId('browser-tab-0')).toHaveAttribute('aria-selected', 'true')

    // Backend re-broadcasts after the switch actually lands — the active
    // tab flips even though nothing else in the component wrote it.
    emitTabs(1, [
      { index: 0, title: 'First' },
      { index: 1, title: 'Second', active: true },
    ])

    expect(screen.getByTestId('browser-tab-0')).toHaveAttribute('aria-selected', 'false')
    expect(screen.getByTestId('browser-tab-1')).toHaveAttribute('aria-selected', 'true')
  })

  it('reflects a tab closing (fewer tabs in the next frame) and never shows zero tabs', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])
    expect(screen.getAllByRole('tab')).toHaveLength(2)

    // Closing tab 1 — backend activates the remaining neighbour.
    emitTabs(0, [{ index: 0, title: 'First', active: true }])

    expect(screen.getAllByRole('tab')).toHaveLength(1)
    expect(screen.getByTestId('browser-tab-strip')).toBeInTheDocument()
  })

  it('disables tab-close and new-tab buttons while disconnected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', active: true }])

    act(() => {
      callbacksRef.current?.onDisconnected?.()
    })

    expect(screen.getByTestId('browser-tab-close-0')).toBeDisabled()
    expect(screen.getByTestId('browser-tab-new')).toBeDisabled()
  })
})
