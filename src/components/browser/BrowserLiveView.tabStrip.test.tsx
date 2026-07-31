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

// D5: importOriginal so the real translateBrowserErrorMessage (now imported
// by BrowserLiveView for the D5 fix) stays live under this mock — only
// BrowserLiveWsConnection itself is replaced.
vi.mock('@/lib/browserLiveWs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/browserLiveWs')>()
  return {
    ...actual,
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
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so every connection double needs it.
          sendViewport: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

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

  it('highlights the active tab (aria-pressed) and not the inactive ones', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(1, [
      { index: 0, title: 'First', url: 'https://example.com' },
      { index: 1, title: 'Second', url: 'https://example.org' },
    ])

    expect(screen.getByTestId('browser-tab-0')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('browser-tab-1')).toHaveAttribute('aria-pressed', 'true')
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
    expect(screen.getByTestId('browser-tab-0')).toHaveAttribute('aria-pressed', 'true')

    // Backend re-broadcasts after the switch actually lands — the active
    // tab flips even though nothing else in the component wrote it.
    emitTabs(1, [
      { index: 0, title: 'First' },
      { index: 1, title: 'Second', active: true },
    ])

    expect(screen.getByTestId('browser-tab-0')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('browser-tab-1')).toHaveAttribute('aria-pressed', 'true')
  })

  it('reflects a tab closing (fewer tabs in the next frame) and never shows zero tabs', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])
    // No more role="tab" (a11y fix — see the tab-strip's own doc comment):
    // each tab chip is a plain button identified by its own testid.
    expect(screen.getAllByTestId(/^browser-tab-\d+$/)).toHaveLength(2)

    // Closing tab 1 — backend activates the remaining neighbour.
    emitTabs(0, [{ index: 0, title: 'First', active: true }])

    expect(screen.getAllByTestId(/^browser-tab-\d+$/)).toHaveLength(1)
    expect(screen.getByTestId('browser-tab-strip')).toBeInTheDocument()
  })

  it('disables the tab chip, tab-close, and new-tab buttons while disconnected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', active: true }])

    act(() => {
      callbacksRef.current?.onDisconnected?.()
    })

    expect(screen.getByTestId('browser-tab-0')).toBeDisabled()
    expect(screen.getByTestId('browser-tab-close-0')).toBeDisabled()
    expect(screen.getByTestId('browser-tab-new')).toBeDisabled()
  })

  // Reviewer finding F2 (a11y follow-up): the close ✕/new-tab ＋ buttons
  // were already gated on `disabled={!connected}`, but the clickable tab
  // chip itself used to be a `<div role="tab" aria-disabled tabIndex={0}>`
  // — focusable and "disabled" in name only, with a manual onKeyDown guard
  // standing in for real disabled semantics (an inert-focusable trap for
  // keyboard/AT users). The chip is now a genuine `<button disabled>`: a
  // native disabled button is dropped from the tab order AND does not
  // dispatch click at all (verified — see the sanity check this mirrors),
  // so there's no separate Enter/Space path left to test.
  it('disables the tab chip button (native disabled, not aria-disabled) and blocks clicks while disconnected', () => {
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
    expect(chip).toBeDisabled()
    expect(chip.className).toContain('cursor-not-allowed')
    expect(chip.className).not.toContain('cursor-pointer')

    fireEvent.click(chip)
    expect(mockSendTabAction).not.toHaveBeenCalled()
  })

  it('leaves the tab chip enabled and clickable once connected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'First', active: true },
      { index: 1, title: 'Second' },
    ])

    const chip = screen.getByTestId('browser-tab-1')
    expect(chip).not.toBeDisabled()
    expect(chip.className).toContain('cursor-pointer')

    fireEvent.click(chip)
    expect(mockSendTabAction).toHaveBeenCalledWith('switch', 1)
  })

  it('the tab strip container is role="group" (not role="tablist") with an accessible label', () => {
    // A11y audit fix, same precedent as CalendarToolbar.tsx's view switcher:
    // no roving-tabindex/aria-controls is implemented, so the ARIA tab
    // pattern must not be promised via role="tablist"/role="tab".
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', active: true }])

    expect(screen.getByRole('group', { name: 'Browser tabs' })).toBeInTheDocument()
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
  })

  it('the Close button is a sibling of the tab chip, not nested inside it, and keeps its own accessible name', () => {
    // A11y fix: role="tab" is children-presentational per the ARIA spec, so
    // a Close button NESTED inside a role="tab" element had its own
    // role/name stripped for assistive tech. Now that the chip is a plain
    // sibling button, Close must be reachable as its own named button.
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Only tab', active: true }])

    const chip = screen.getByTestId('browser-tab-0')
    const closeBtn = screen.getByRole('button', { name: 'Close tab: Only tab' })
    expect(closeBtn).toBeInTheDocument()
    // Sibling, not descendant.
    expect(chip.contains(closeBtn)).toBe(false)
    expect(closeBtn.contains(chip)).toBe(false)
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
