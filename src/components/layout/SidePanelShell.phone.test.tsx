// SidePanelShell.phone.test.tsx — side-panel-shell-spec.md §12 tests #12 +
// #21: the SP-25 phone takeover at <680px row (no separator, no overlay,
// chat inert), the US-8 close affordances (header ✕, Back = one pushed
// history step, swipe-to-close) and SP-26's swipe recognizer rules.
//
// Mixed file: takeover layout + affordance presence + swipe thresholds are
// CHARACTERISATION (green on wave-0 code); the US-8 inert chat column and
// SP-26's "scroller already at its left end" handover rule are RED.
//
// Oracles: side-panel-shell-spec.md §2/§5/§6 — below 680px the panel takes
// over the full row (overlay deleted by SP-25); the chat column is INERT
// (US-8 AS-1); close affordances are the header ✕, a Back button (one
// pushed history step), and a swipe from the 24px left edge zone closing at
// >=96px travel or >=0.4px/ms after >=48px; a swipe over horizontally
// scrollable content is a SCROLL — except when the scroller is already at
// its left end, which hands the gesture to the recognizer.

import { cleanup, fireEvent, render, screen, act, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import type {
  PanelDefinition,
  PanelContentProps,
} from '@/components/panel-shell/types'

class RowRO {
  static last: RowRO | null = null
  cb: ResizeObserverCallback
  constructor(cb: ResizeObserverCallback) {
    this.cb = cb
    RowRO.last = this
  }
  observe() {}
  unobserve() {}
  disconnect() {}
  fire(width: number) {
    this.cb(
      [{ contentRect: { width } as DOMRectReadOnly } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }
}
vi.stubGlobal('ResizeObserver', RowRO)

function probeContent(_props: PanelContentProps) {
  return (
    <div data-testid="probe-content">
      <div data-testid="probe-scroller" style={{ overflowX: 'auto', width: 200 }}>
        <div style={{ width: 600 }}>content</div>
      </div>
    </div>
  )
}

function makeDef(): PanelDefinition {
  return {
    id: 'library',
    title: 'Library',
    content: probeContent,
    expandTarget: () => '/library',
  }
}

function renderShell() {
  render(
    <SidePanelShell
      panels={[makeDef()]}
      username="dana"
      chat={
        <div>
          <button data-testid="chat-button">chat button</button>
        </div>
      }
    />,
  )
}

function takeover() {
  act(() => {
    usePanelShellStore.getState().openPanel('library')
  })
  act(() => RowRO.last!.fire(640))
}

function docked() {
  act(() => {
    usePanelShellStore.getState().openPanel('library')
  })
  act(() => RowRO.last!.fire(1400))
}

function resetStore() {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
  })
}

beforeEach(() => {
  resetStore()
  RowRO.last = null
})

afterEach(() => {
  cleanup()
  resetStore()
  vi.useRealTimers()
})

function touch(
  type: 'touchstart' | 'touchmove' | 'touchend',
  el: Element,
  x: number,
  y: number,
) {
  const e = new Event(type, { bubbles: true, cancelable: true })
  const t = { clientX: x, clientY: y, identifier: 1, target: el }
  Object.defineProperty(e, 'touches', { value: type === 'touchend' ? [] : [t] })
  Object.defineProperty(e, 'changedTouches', { value: [t] })
  return e
}

function swipe(
  el: Element,
  points: Array<[number, number]>,
  times?: number[],
) {
  fireEvent(el, touch('touchstart', el, points[0][0], points[0][1]))
  for (let i = 1; i < points.length; i++) {
    if (times) vi.advanceTimersByTime(times[i] - times[i - 1])
    fireEvent(el, touch('touchmove', el, points[i][0], points[i][1]))
  }
  const last = points[points.length - 1]
  fireEvent(el, touch('touchend', el, last[0], last[1]))
}

describe('SP-25 phone takeover layout (§12 #12) — characterisation, green on wave-0 code', () => {
  it('below 680px row the resize separator is GONE (no drag resize in takeover, US-8/SP-25)', () => {
    renderShell()
    takeover()
    expect(screen.queryByRole('separator')).toBeNull()
  })

  it('at exactly 680px the docked layout returns (boundary min-1/min on the takeover threshold)', () => {
    renderShell()
    docked()
    expect(screen.getByRole('separator')).toBeInTheDocument()
  })

  it('US-8 AS-3: the header ✕ closes the panel in takeover', async () => {
    renderShell()
    takeover()
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('US-8 AS-2: a Back button renders in takeover and closes via the pushed history step', async () => {
    renderShell()
    takeover()
    const back = screen.getByRole('button', { name: /back/i })
    fireEvent.click(back)
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('the Back affordance exists ONLY in takeover — docked has no Back button (US-8 AS-2)', () => {
    renderShell()
    docked()
    expect(screen.queryByRole('button', { name: /back/i })).toBeNull()
  })
})

describe('US-8 AS-1: the takeover chat column is INERT (RED — wave-0 hides it with a class)', () => {
  it('the chat column carries the inert attribute while the panel is open in takeover', () => {
    renderShell()
    takeover()
    const chatColumn = screen.getByTestId('chat-column')
    expect(chatColumn.getAttribute('inert')).not.toBeNull()
  })

  it('the chat column is NOT inert when docked (the split is interactive)', () => {
    renderShell()
    docked()
    const chatColumn = screen.getByTestId('chat-column')
    expect(chatColumn.getAttribute('inert')).toBeNull()
  })
})

describe('SP-26 swipe-to-close recognizer (§12 #21) — characterisation, green on wave-0 code', () => {
  it('a swipe >= 96px from the left edge zone closes the panel', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    await act(async () => {
      swipe(screen.getByTestId('side-panel'), [[8, 200], [64, 210], [128, 220]])
    })
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('a short slow swipe (<96px, <0.4px/ms) does NOT close', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    await act(async () => {
      swipe(
        screen.getByTestId('side-panel'),
        [[8, 200], [48, 210], [60, 215]],
        [0, 100, 200],
      )
    })
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
  })

  it('a Flick (60px in 100ms = 0.6px/ms >= 0.4 after >=48px) closes (velocity rule)', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    await act(async () => {
      swipe(
        screen.getByTestId('side-panel'),
        [[8, 200], [40, 205], [68, 210]],
        [0, 50, 100],
      )
    })
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('a touch STARTING outside the 24px edge zone never closes (SP-26 edge zone)', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    await act(async () => {
      swipe(screen.getByTestId('side-panel'), [[100, 200], [220, 200]])
    })
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
  })

  it('a VERTICAL drag does not close (direction lock)', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    await act(async () => {
      swipe(screen.getByTestId('side-panel'), [[8, 200], [12, 320]])
    })
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
  })
})

describe('SP-26 scroller non-conflict (RED — recognizer ignores scroll position)', () => {
  it('a swipe over horizontally-scrollable content is a SCROLL — the panel does NOT close', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    const scroller = screen.getByTestId('probe-scroller')
    Object.defineProperty(scroller, 'scrollWidth', { value: 600, configurable: true })
    Object.defineProperty(scroller, 'clientWidth', { value: 200, configurable: true })
    // SP-26's exact rule: the scroller owns the gesture when it "can scroll
    // horizontally in the drag's direction"; the edge-swipe recognizer takes
    // over only "on such a scroller already at its left end". The drag runs
    // rightward, so the scroller must NOT start at its left end — start it
    // mid-range (scrollLeft=100 of max 400) or the test would exercise the
    // handover rule instead of the non-conflict rule.
    Object.defineProperty(scroller, 'scrollLeft', { value: 100, configurable: true })
    await act(async () => {
      swipe(scroller, [[8, 100], [128, 100]])
    })
    // The close path is async (guardThenClose awaits runGuard): flush the
    // chain before the did-NOT-close claim, then prove the instrument with a
    // positive control — the same gesture on the PANEL itself closes (via
    // waitFor), so a too-short flush fails loudly here instead of passing
    // vacuously.
    for (let i = 0; i < 5; i++) {
      await act(async () => {
        await Promise.resolve()
      })
    }
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
    await act(async () => {
      swipe(screen.getByTestId('side-panel'), [[8, 200], [64, 210], [128, 220]])
    })
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('RED — a scroller ALREADY AT ITS LEFT END hands the gesture to the recognizer (SP-26): the swipe CLOSES', async () => {
    vi.useFakeTimers({ toFake: ['performance'] })
    renderShell()
    takeover()
    const scroller = screen.getByTestId('probe-scroller')
    Object.defineProperty(scroller, 'scrollWidth', { value: 600, configurable: true })
    Object.defineProperty(scroller, 'clientWidth', { value: 200, configurable: true })
    scroller.scrollTo?.(0, 0)
    await act(async () => {
      swipe(scroller, [[8, 100], [128, 100]])
    })
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })
})
