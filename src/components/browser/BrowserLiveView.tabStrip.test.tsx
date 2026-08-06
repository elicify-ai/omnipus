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

// Regression coverage for the address bar going stale (operator report,
// 2026-08-03: "back button and refresh button do not work").
//
// They DID work. Measured on v53: pressing Back moved the page and the tab
// title to example.com while the address bar still read
// en.wikipedia.org/wiki/Octopus — so the buttons looked broken because the bar
// contradicted the page. Cause: setUrlInput was called in exactly ONE place,
// the omnibox's own submit handler, so the bar only ever reflected what the
// USER typed. Every other navigation — Back, Refresh, agent-driven, in-page
// links — left it behind.
//
// The bar now follows the active tab's url from the same browser_tabs frame the
// strip renders from, so the two can never disagree.
describe('BrowserLiveView — address bar follows the active tab', () => {
  const addressBar = () => screen.getByLabelText('Address bar') as HTMLInputElement

  it('shows the active tab url without the user typing anything', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Example Domain', url: 'https://example.com/', active: true }])

    expect(addressBar().value).toBe('https://example.com/')
  })

  it('updates when navigation happens without the omnibox — the Back/Refresh case', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Octopus', url: 'https://en.wikipedia.org/wiki/Octopus', active: true }])
    expect(addressBar().value).toBe('https://en.wikipedia.org/wiki/Octopus')

    // Back: the server reports the tab now sits on the previous entry.
    emitTabs(0, [{ index: 0, title: 'Example Domain', url: 'https://example.com/', active: true }])
    expect(addressBar().value).toBe('https://example.com/')
  })

  it('follows the active tab when the user switches tabs', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const tabs = [
      { index: 0, title: 'Example Domain', url: 'https://example.com/' },
      { index: 1, title: 'Octopus', url: 'https://en.wikipedia.org/wiki/Octopus' },
    ]
    emitTabs(0, tabs)
    expect(addressBar().value).toBe('https://example.com/')

    emitTabs(1, tabs)
    expect(addressBar().value).toBe('https://en.wikipedia.org/wiki/Octopus')
  })

  // The guard that keeps this fix from becoming the FIRST bug reported in this
  // series ("the URL bar clears itself while I type").
  it('never overwrites a url the user is midway through typing', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Example Domain', url: 'https://example.com/', active: true }])

    const bar = addressBar()
    fireEvent.focus(bar)
    fireEvent.change(bar, { target: { value: 'https://my-half-typed-ur' } })

    // A tabs frame lands mid-typing (a background title refresh, the agent
    // navigating another tab, a periodic re-broadcast).
    emitTabs(0, [{ index: 0, title: 'Something Else', url: 'https://unrelated.example/', active: true }])

    expect(bar.value).toBe('https://my-half-typed-ur')
  })

  it('resumes following the active tab once the user leaves the bar', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const bar = addressBar()
    fireEvent.focus(bar)
    fireEvent.change(bar, { target: { value: 'typing…' } })
    fireEvent.blur(bar)

    emitTabs(0, [{ index: 0, title: 'Example Domain', url: 'https://example.com/', active: true }])
    expect(bar.value).toBe('https://example.com/')
  })

  // about:blank is what a not-yet-navigated tab reports; showing it to a user
  // looking at the Omnipus start page is noise.
  it('does not display the blank-tab placeholder', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'Omnipus Browser', url: 'about:blank', active: true }])

    expect(addressBar().value).toBe('')
  })
})

// Header consolidation (operator direction, 2026-08-04). Four chrome rows
// became two: tabs share the top strip with the window controls, everything
// else rides the toolbar. These pin the two invariants that make that safe.
describe('BrowserLiveView — consolidated two-row header', () => {
  // The trap in this refactor. Close and Pop-out moved INTO the tab row, so if
  // that row inherited the tab strip's `tabs.length > 0` guard, an empty tab
  // list would take the only way to close the panel with it.
  it('keeps Close and Pop-out reachable when the tab list is empty', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" onClose={() => {}} onPopOut={() => {}} />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'One', url: 'https://example.com' }])
    expect(screen.getByTestId('browser-tab-strip')).toBeInTheDocument()

    // The tab list arrives empty — the strip itself may go, the controls may not.
    emitTabs(0, [])

    expect(screen.queryByTestId('browser-tab-strip')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Close live browser panel')).toBeInTheDocument()
    expect(screen.getByLabelText('Pop out')).toBeInTheDocument()
  })

  // Both rows are FIXED height. The panel pushes its own box as the remote
  // viewport, so a header that changes height forces a full capture rebuild —
  // the measured regression that made the handback hint always-mounted. Folding
  // that hint onto the toolbar keeps it horizontal for exactly this reason.
  it('never changes header row count or height when the drive state changes', () => {
    const { container } = render(<BrowserLiveView sessionId="s1" agentId="a1" onClose={() => {}} />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'One', url: 'https://example.com' }])

    const col = container.querySelector('[data-testid="browser-live-frame"]')?.closest('div')?.parentElement
    const rowsIdle = [...(col?.children ?? [])].length
    const hint = screen.getByTestId('browser-live-handback-hint')
    expect(hint).toHaveClass('invisible')

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    // Same element, same row count — it becomes visible in place rather than
    // mounting a row and displacing the frame.
    expect(screen.getByTestId('browser-live-handback-hint')).toBe(hint)
    expect(hint).not.toHaveClass('invisible')
    expect([...(col?.children ?? [])].length).toBe(rowsIdle)
  })

  // The consolidation itself: chrome above the frame is two rows, not four.
  it('renders exactly two chrome rows above the live frame', () => {
    const { container } = render(<BrowserLiveView sessionId="s1" agentId="a1" onClose={() => {}} />)
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'One', url: 'https://example.com' }])

    const root = container.firstElementChild as HTMLElement
    const kids = [...root.children]
    const frameIdx = kids.findIndex((k) => k.querySelector('[data-testid="browser-live-frame"]') || k.getAttribute('data-testid') === 'browser-live-frame')
    expect(frameIdx).toBe(2)
    expect(kids[0].querySelector('[data-testid="browser-tab-strip"]')).toBeTruthy()
    expect(kids[1].querySelector('input[aria-label="Address bar"]')).toBeTruthy()
  })
})

// Regression: the consolidated toolbar collapsed the address bar to 23px.
//
// Measured on UAT v59, not theorised — row B was 575px wide and its fixed
// children summed to 581px, so the `min-w-0 flex-1` form was flexed to zero and
// the field became unusable. jsdom performs no layout, so this cannot assert a
// pixel width; it asserts the structural guarantee that replaced the arithmetic
// coincidence — a min-width floor on the field, and that the handback hint (the
// 185px offender) no longer sits in a chrome row at all.
describe('BrowserLiveView — the address bar cannot be squeezed out', () => {
  it('gives the address field a min-width floor and keeps the hint off the toolbar', () => {
    const { container } = render(
      <BrowserLiveView sessionId="s1" agentId="a1" onClose={() => {}} onPopOut={() => {}} canAnnotate />,
    )
    connectAndFrame()
    emitTabs(0, [{ index: 0, title: 'One', url: 'https://example.com' }])

    const form = container.querySelector('form')
    expect(form?.className).toMatch(/min-w-\[\d+px\]/)
    expect(form?.className).not.toMatch(/min-w-0/)

    // The hint overlays the frame; it must not be a child of either chrome row.
    const rows = [...(container.firstElementChild?.children ?? [])].slice(0, 2)
    for (const row of rows) {
      expect(row.querySelector('[data-testid="browser-live-handback-hint"]')).toBeNull()
    }
    expect(screen.getByTestId('browser-live-handback-hint')).toBeInTheDocument()
  })
})
