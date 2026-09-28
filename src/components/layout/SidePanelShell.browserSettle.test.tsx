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
// Driven entirely through the REAL shell + REAL ResizeSeparator: the
// Browser content probe records the settle notification the shell gives the
// panel (onWidthSettle — the shell→panel width handover; only a stub today,
// which is exactly what this pack is red for) and renders it as the VISIBLE
// status the spec requires. Wave-0's PanelContentProps does not carry
// onWidthSettle yet, so it is typed structurally — the probe renders the
// status only if the shell actually notifies it, so a shell that never
// notifies fails loudly instead of passing vacuously.
//
// RED on this pre-GREEN tree (assertion form): the shell never notifies the
// panel of a settled width, so no status ever renders and the settle
// notification is never observed.

import { cleanup, fireEvent, render, screen, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import type { PanelDefinition, PanelContentProps } from '@/components/panel-shell/types'

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

// The settle notification the shell owes the panel (FR-015/9b) — typed
// structurally because wave-0 PanelContentProps predates it.
let settleCalls: number[] = []

function browserProbe(props: PanelContentProps & { onWidthSettle?: (px: number) => void }) {
  const settled = settleCalls.length > 0 ? settleCalls[settleCalls.length - 1] : null
  return (
    <div data-testid="browser-probe">
      {settled !== null && (
        // FR-015's "visible status ... never a silent stall": once the
        // settle/handover fires, the panel SHOWS it.
        <output data-testid="browser-handover-status" role="status">
          Remote viewport handover at {settled}px
        </output>
      )}
      {/* keep the prop referenced so a type-level regression is visible */}
      <span hidden>{typeof props.onWidthSettle}</span>
    </div>
  )
}

function makeBrowserDef(): PanelDefinition {
  return {
    id: 'browser',
    title: 'Browser',
    content: browserProbe,
    expandTarget: () => '/browser-live',
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
    panelWidth: -1,
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
