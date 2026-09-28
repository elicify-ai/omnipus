// SidePanelShell.browserSettle.test.tsx — side-panel-shell-spec.md §12 row 9b
// (F-B3, CHECK part B): "Browser under shell: width change triggers the
// existing settle/handover with visible status" — FR-015's ONLY mapping
// (MAJ-011 fix); MIN-205 puts the settle 300ms after the last resize so a
// fast drag/keystroke burst never floods the remote viewport.
//
// Oracle (FR-015, verbatim): "The Browser panel's resize MUST drive the
// existing remote-viewport handover triggered on SETTLE (300ms after the
// last resize keystroke — MIN-205 ...), with its visible status and Retry
// ..., never a silent stall."
//
// The §8.1 settle channel is a SUBSCRIBE function (GREEN
// src/components/panel-shell/types.ts:112::PanelContentProps.onWidthSettle —
// "(listener: ((px: number) => void) | null) => void", "Subscribe to settled
// divider widths. Browser uses the notification to start its existing
// remote-viewport handover only after resize settles."): the panel REGISTERS
// a listener in an effect and unregisters with null on unmount; the shell
// invokes it once per settle (GREEN SidePanelShell.tsx separator onCommit —
// settleWidth(px) then registered.listener(px)).
//
// Batch-8 rewrite (squad-lead-verified defect in the previous probe): it
// READ props.onWidthSettle as a direct-call value and never registered a
// listener, so settleCalls could never be written and the pack could never
// pass. This probe subscribes the way the real Browser panel does — register
// in an effect, record the settled px into both the module array (the
// assertion oracle) and React state (so the VISIBLE status re-renders),
// unregister with null on unmount — and keeps every original assertion:
// flood guard mid-burst, settle-once 300ms after the last change, real width
// (SP-17 floor), visible status, second burst settles again.
//
// RED on this pre-GREEN tree (stated form): wave-0's PanelContentProps does
// not carry onWidthSettle, so the probe renders a loud BLOCKED marker
// (browser-settle-blocked) and every test fails at requireSettleChannel()
// naming the missing channel — never an unexplained TypeError from a
// missing prop.

import { useEffect, useState } from 'react'
import { cleanup, fireEvent, render, screen, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import type { PanelDefinition, PanelContentProps } from '@/components/panel-shell/types'

/** The §8.1 settle channel's real shape (GREEN
 * src/components/panel-shell/types.ts:112::PanelContentProps.onWidthSettle):
 * a SUBSCRIBE function — register a settle listener, or pass null to
 * unregister. Typed structurally here because wave-0 PanelContentProps
 * predates the member; the signature is identical to GREEN's, so the
 * intersection below compiles on both trees without loosening anything. */
type WidthSettleSubscribe = (listener: ((px: number) => void) | null) => void

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

// The settle notifications the shell owes the panel (FR-015/9b), recorded by
// the probe's registered listener.
let settleCalls: number[] = []

function browserProbe(props: PanelContentProps & { onWidthSettle?: WidthSettleSubscribe }) {
  // React state so the VISIBLE status re-renders on each settle (the module
  // array alone would update assertions but never the rendered output).
  const [settled, setSettled] = useState<number | null>(null)
  const [blocked, setBlocked] = useState(false)

  useEffect(() => {
    const register = props.onWidthSettle
    if (typeof register !== 'function') {
      setBlocked(true)
      return
    }
    const listener = (px: number) => {
      settleCalls.push(px)
      setSettled(px)
    }
    register(listener)
    // §8.1 unregistration: null on unmount.
    return () => register(null)
  }, [props.onWidthSettle])

  if (blocked) {
    // Loud, stated absence — the shell passed no subscribe channel (wave-0).
    // The browser-settle-blocked marker is what requireSettleChannel() sees.
    return (
      <div data-testid="browser-probe">
        <output data-testid="browser-settle-blocked" role="alert">
          BLOCKED: shell provided no onWidthSettle subscribe channel
        </output>
      </div>
    )
  }

  return (
    <div data-testid="browser-probe">
      {settled !== null && (
        // FR-015's "visible status ... never a silent stall": once the
        // settle/handover fires, the panel SHOWS it.
        <output data-testid="browser-handover-status" role="status">
          Remote viewport handover at {settled}px
        </output>
      )}
    </div>
  )
}

/** Stated gate: fail naming the missing settle channel, never with a
 * downstream TypeError or a vacuous pass. */
function requireSettleChannel() {
  if (screen.queryByTestId('browser-settle-blocked') !== null) {
    throw new Error(
      'BLOCKED: the shell provides no onWidthSettle subscribe channel to panel content — ' +
        'required by FR-015/§12 9b (GREEN src/components/panel-shell/types.ts:112::PanelContentProps.onWidthSettle: ' +
        '"(listener: ((px: number) => void) | null) => void" — the panel registers a settle listener; ' +
        'the Browser handover starts only on that notification)',
    )
  }
  expect(screen.queryByTestId('browser-settle-blocked')).toBeNull()
}

function makeBrowserDef(): PanelDefinition {
  return {
    id: 'browser',
    title: 'Browser',
    content: browserProbe,
    fullScreen: {
      toSearch: () => ({ session: 'session-1', agent: 'agent-1' }),
      fromSearch: () => ({ sessionId: 'session-1', agentId: 'agent-1' }),
    },
  }
}

function renderShell() {
  render(
    <SidePanelShell panels={[makeBrowserDef()]} username="dana" chat={<div>chat</div>} />,
  )
}

function openDocked() {
  act(() => {
    usePanelShellStore.getState().openPanel('browser', { sessionId: 's1', agentId: 'mia' })
  })
  act(() => RowRO.last!.fire(1400))
}

function resetStore() {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: null,
    guardPending: false,
    historyPushed: false,
  })
}

beforeEach(() => {
  settleCalls = []
  resetStore()
  RowRO.last = null
})

afterEach(() => {
  cleanup()
  resetStore()
  vi.useRealTimers()
})

describe('§12 9b — Browser settle/handover on width change (FR-015, MIN-205)', () => {
  it('RED — a resize burst settles ONCE, 300ms after the last change, with the handover status VISIBLE', () => {
    vi.useFakeTimers()
    renderShell()
    openDocked()

    // Stated gate first: without the subscribe channel nothing below could
    // ever observe a settle (this is the pre-GREEN failure point).
    requireSettleChannel()

    // Before any resize: no handover status (instrument proof — the
    // assertion below is not vacuously satisfiable by a static label).
    expect(screen.queryByTestId('browser-handover-status')).toBeNull()

    // A fast keystroke burst on the REAL separator: each 16px step
    // re-schedules the settle; nothing commits mid-burst.
    const separator = screen.getByTestId('panel-resize-separator')
    fireEvent.focus(separator)
    fireEvent.keyDown(separator, { key: 'ArrowLeft' })
    fireEvent.keyDown(separator, { key: 'ArrowLeft' })
    fireEvent.keyDown(separator, { key: 'ArrowRight' })

    // Nothing has settled inside the burst window.
    act(() => {
      vi.advanceTimersByTime(100)
    })
    expect(settleCalls).toEqual([])

    // SETTLE: 300ms after the LAST change the shell commits and must notify
    // the panel exactly once (MIN-205: a fast burst never floods).
    act(() => {
      vi.advanceTimersByTime(220)
    })

    // 1. The settle/handover fired exactly once…
    expect(settleCalls.length).toBe(1)
    // …with a real panel width (SP-17 floor: panel >= 320px).
    const settledWidth = settleCalls[0]
    expect(typeof settledWidth).toBe('number')
    expect(settledWidth).toBeGreaterThanOrEqual(320)
    // 2. …with the VISIBLE status the spec requires (never a silent stall).
    expect(screen.getByTestId('browser-handover-status')).toHaveTextContent(
      `Remote viewport handover at ${settledWidth}px`,
    )
  })

  it('RED — a second, later resize settles AGAIN (each settle notifies; memory stays live)', () => {
    vi.useFakeTimers()
    renderShell()
    openDocked()
    requireSettleChannel()
    const separator = screen.getByTestId('panel-resize-separator')
    fireEvent.focus(separator)

    // First burst → settle.
    fireEvent.keyDown(separator, { key: 'ArrowLeft' })
    act(() => {
      vi.advanceTimersByTime(320)
    })
    expect(settleCalls.length).toBe(1)

    // Second burst after the settle → a SECOND notification (the handover
    // is driven per settle, not once per open).
    fireEvent.keyDown(separator, { key: 'ArrowLeft' })
    act(() => {
      vi.advanceTimersByTime(320)
    })
    expect(settleCalls.length).toBe(2)
    expect(screen.getByTestId('browser-handover-status')).toBeInTheDocument()
  })
})
