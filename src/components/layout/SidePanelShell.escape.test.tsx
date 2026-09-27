// SidePanelShell.escape.test.tsx — side-panel-shell-spec.md §12 test #13:
// Escape layering (US-6/US-7, SP-19, MIN-208) + focus (US-9 AS-3, MIN-002).
//
// Mixed file: SP-19's Browser exception, the discard-dialog precedence, the
// guard-honouring close and focus-return-to-trigger are CHARACTERISATION
// (green on wave-0 code); the MIN-208 input-method guards (rewritten after
// CHECK F1: they passed vacuously against the async close path — each now
// flushes the async chain and ends with a positive-control close) and the
// US-9 AS-3 focus-into-panel on open are RED.
//
// Oracles: side-panel-shell-spec.md §2/§5 — Escape closes the focused panel
// unless the Browser panel (SP-19) or the discard dialog holds it; input
// methods that already consumed the event are ignored; focus enters the
// panel on open and returns to the trigger on close.

import { cleanup, fireEvent, render, screen, act, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
import type {
  PanelDefinition,
  PanelContentProps,
} from '@/components/panel-shell/types'

vi.mock('@/components/library/preview/unsavedGuard', () => ({
  getDiscardConfirmDialogOpen: vi.fn(() => false),
}))

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
  return <div data-testid="probe-content">probe</div>
}

function makeDef(
  id: 'library' | 'browser',
  beforeLeave?: () => Promise<boolean>,
): PanelDefinition {
  return {
    id,
    title: id === 'library' ? 'Library' : 'Browser',
    content: probeContent,
    expandTarget: () => (id === 'library' ? '/library' : '/browser-live'),
    ...(beforeLeave ? { beforeLeave } : {}),
  }
}

function renderShell(panels: PanelDefinition[]) {
  render(
    <>
      <button data-panel-trigger="library">Library trigger</button>
      <SidePanelShell
        panels={panels}
        username="dana"
        chat={<div data-testid="chat-probe">chat</div>}
      />
    </>,
  )
}

function openPanel(id: 'library' | 'browser', context: Record<string, unknown> = {}) {
  act(() => {
    usePanelShellStore.getState().openPanel(id, context)
  })
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
  vi.mocked(getDiscardConfirmDialogOpen).mockReturnValue(false)
  resetStore()
  RowRO.last = null
})

afterEach(() => {
  cleanup()
  resetStore()
})

describe('Escape layering (§12 #13, US-6/US-7/SP-19) — characterisation, green on wave-0 code', () => {
  it('Escape closes the open Library panel when the event originates inside the shell', async () => {
    renderShell([makeDef('library')])
    openPanel('library')
    fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('SP-19: Escape NEVER closes the Browser panel (its live content owns Escape)', async () => {
    renderShell([makeDef('library'), makeDef('browser')])
    openPanel('browser', { sessionId: 's1', agentId: 'a1' })
    await act(async () => {
      fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
    })
    expect(usePanelShellStore.getState().activePanel?.id).toBe('browser')
  })

  it('Escape is IGNORED while the library discard-confirm dialog is open (US-7/US-6 layering)', async () => {
    renderShell([makeDef('library')])
    openPanel('library')
    vi.mocked(getDiscardConfirmDialogOpen).mockReturnValue(true)
    await act(async () => {
      fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
    })
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
  })

  it('Escape close honours the beforeLeave guard: a DECLINED guard keeps the panel open', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(false)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library')
    await act(async () => {
      fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
    })
    expect(beforeLeave).toHaveBeenCalledTimes(1)
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
  })

  it('Escape close honours the beforeLeave guard: an ACCEPTED guard closes', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(true)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library')
    fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })
})

describe('MIN-208 input-method guards (RED — onShellKeyDown checks only key/dialog/browser)', () => {
  // The close path is ASYNC: onShellKeyDown → shell.requestClose() →
  // guardThenClose awaits runGuard before it closes. A synchronous
  // `expect(...).not.toBeNull()` right after fireEvent ran BEFORE the close
  // landed and passed vacuously — commit 226746310 recorded these two as
  // "2 RED", but the tree actually passed them (8 passed / 1 failed; the
  // only RED was US-9 AS-3 focus-into-panel). Each test therefore (a) flushes
  // the async close chain before asserting nothing closed, and (b) ends with
  // a POSITIVE CONTROL — a plain Escape through the same async path, proven
  // to close — so a future flush that is too short fails loudly here instead
  // of passing vacuously again.
  async function flushCloseChain() {
    for (let i = 0; i < 5; i++) {
      await act(async () => {
        await Promise.resolve()
      })
    }
  }

  function fireGuardedEscape(target: HTMLElement, init: (ev: KeyboardEvent) => void) {
    const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    init(ev)
    fireEvent(target, ev)
  }

  it('an Escape the page already handled (defaultPrevented) must NOT close the panel (MIN-208)', async () => {
    renderShell([makeDef('library')])
    openPanel('library')
    const panel = screen.getByTestId('side-panel')
    fireGuardedEscape(panel, (ev) => ev.preventDefault())
    await flushCloseChain()
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
    // Positive control: a plain Escape through the SAME async close path.
    fireEvent.keyDown(panel, { key: 'Escape' })
    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
  })

  it('an Escape during IME composition (isComposing) must NOT close the panel (MIN-208)', async () => {
    renderShell([makeDef('library')])
    openPanel('library')
    const panel = screen.getByTestId('side-panel')
    fireGuardedEscape(panel, (ev) => {
      Object.defineProperty(ev, 'isComposing', { value: true })
    })
    await flushCloseChain()
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
    // Positive control: a plain Escape through the SAME async close path.
    fireEvent.keyDown(panel, { key: 'Escape' })
    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
  })
})

describe('focus management (US-9 AS-3 / MIN-002)', () => {
  it('RED — focus moves INTO the panel on open (US-9 AS-3: the panel header receives focus)', async () => {
    renderShell([makeDef('library')])
    openPanel('library')
    const panel = screen.getByTestId('side-panel')
    await waitFor(() => {
      expect(panel.contains(document.activeElement)).toBe(true)
    })
  })

  it('focus returns to the opening trigger on close (MIN-002)', async () => {
    renderShell([makeDef('library')])
    const trigger = screen.getByText('Library trigger')
    act(() => {
      trigger.focus()
    })
    openPanel('library')
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    await waitFor(() => {
      expect(document.activeElement).toBe(trigger)
    })
  })
})
