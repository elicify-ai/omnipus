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

  // Reviewer finding F2: the close ✕/new-tab ＋ buttons were already gated
  // on `disabled={!connected}`, but the clickable tab chip itself had no
  // such gate — clicking it while the WS was reconnecting silently no-op'd
  // (sendTabAction returning false, discarded). The chip must now be
  // non-interactive (aria-disabled, no pointer cursor, click/Enter/Space
  // no-op) while disconnected.
  it('marks the tab chip aria-disabled and non-clickable while disconnected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    act(() => {
      callbacksRef.current?.onDisconnected?.()
    })

    const chip = screen.getByTestId('browser-tab-1')
    expect(chip).toHaveAttribute('aria-disabled', 'true')
    expect(chip.className).toContain('cursor-not-allowed')
    expect(chip.className).not.toContain('cursor-pointer')

    fireEvent.click(chip)
    expect(mockSendTabAction).not.toHaveBeenCalled()

    fireEvent.keyDown(chip, { key: 'Enter' })
    expect(mockSendTabAction).not.toHaveBeenCalled()
  })

  it('leaves the tab chip clickable (not aria-disabled) once connected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    const chip = screen.getByTestId('browser-tab-1')
    expect(chip).toHaveAttribute('aria-disabled', 'false')
    expect(chip.className).toContain('cursor-pointer')

    fireEvent.click(chip)
    expect(mockSendTabAction).toHaveBeenCalledWith('switch', 1)
  })
})

describe('BrowserLiveView — tab strip actions take the wheel (ADR-041 D4 / F1)', () => {
  // Reviewer finding F1: the backend only honours `browser_tab_action` when
  // this connection holds the control lock, or nobody controls (idle) — a
  // merely-watching viewer's tab action would be rejected. Every tab-strip
  // handler must call takeWheelIfNeeded() (send control:take) BEFORE
  // sendTabAction, exactly like the omnibox does before navigating.
  it('switching a tab while idle sends control:take before browser_tab_action', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    fireEvent.click(screen.getByTestId('browser-tab-1'))

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendTabAction).toHaveBeenCalledWith('switch', 1)
    const takeOrder = mockSendControl.mock.invocationCallOrder[0]
    const switchOrder = mockSendTabAction.mock.invocationCallOrder[0]
    expect(takeOrder).toBeLessThan(switchOrder)
  })

  it('closing a tab sends control:take before browser_tab_action', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    fireEvent.click(screen.getByTestId('browser-tab-close-1'))

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendTabAction).toHaveBeenCalledWith('close', 1)
    const takeOrder = mockSendControl.mock.invocationCallOrder[0]
    const closeOrder = mockSendTabAction.mock.invocationCallOrder[0]
    expect(takeOrder).toBeLessThan(closeOrder)
  })

  it('opening a new tab sends control:take before browser_tab_action', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', active: true }])

    fireEvent.click(screen.getByTestId('browser-tab-new'))

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendTabAction).toHaveBeenCalledWith('open')
    const takeOrder = mockSendControl.mock.invocationCallOrder[0]
    const openOrder = mockSendTabAction.mock.invocationCallOrder[0]
    expect(takeOrder).toBeLessThan(openOrder)
  })

  // Reviewer finding F2: a failed sendTabAction (e.g. dead/reconnecting
  // transport) must surface a toast rather than silently discarding the
  // boolean return.
  it('surfaces a toast when sendTabAction fails', async () => {
    const { useUiStore } = await import('@/store/ui')
    useUiStore.setState({ toasts: [] })
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])
    mockSendTabAction.mockReturnValueOnce(false)

    fireEvent.click(screen.getByTestId('browser-tab-1'))

    expect(useUiStore.getState().toasts.some((t) => /could not switch tabs/i.test(t.message))).toBe(true)
  })
})
