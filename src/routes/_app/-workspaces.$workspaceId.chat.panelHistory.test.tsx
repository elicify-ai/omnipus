// -workspaces.$workspaceId.chat.panelHistory.test.tsx — side-panel-shell-spec.md
// §12 #15 (SP-22, MAJ-206) plus the full-page-route regression.
//
// RED: the wave-0 shell never projects `panel` onto the chat URL (read
// SidePanelShell.tsx / usePanelShell.ts — history.pushState only in phone
// takeover, no replaceState, no `panel` search). Desktop open/close must
// REPLACE, never push. Back/Forward must re-project the store's panel.
//
// The full-page route modules (library, team, calendar) still export a
// component — characterisation that wave 1 did not delete them (US-5.4).

import { cleanup, render, act } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import type { PanelDefinition } from '@/components/panel-shell/types'
import { Route as LibraryRoute } from './library'
import { Route as TeamRoute } from './workspaces.$workspaceId.team'
import { Route as CalendarRoute } from './workspaces.$workspaceId.calendar'

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

function probe() {
  return <div data-testid="probe-content" />
}

const library: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: probe,
  expandTarget: () => '/library',
}

function urlsOf(spy: { mock: { calls: unknown[][] } }): string[] {
  return spy.mock.calls.map((call) => String(call[2] ?? ''))
}

afterEach(() => {
  cleanup()
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
  })
  vi.restoreAllMocks()
})

describe('desktop panel history is REPLACE (§12 #15, SP-22, MAJ-206)', () => {
  it('RED — opening a panel on a docked row REPLACES the URL with ?panel=library and does not push', () => {
    const replace = vi.spyOn(window.history, 'replaceState')
    const push = vi.spyOn(window.history, 'pushState')
    render(
      <SidePanelShell panels={[library]} username="dana" chat={<div>chat</div>} />,
    )
    act(() => {
      usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    act(() => RowRO.last!.fire(1400))
    expect(urlsOf(replace).some((u) => u.includes('panel=library'))).toBe(true)
    expect(push).not.toHaveBeenCalled()
  })

  it('RED — closing the panel REPLACES the URL so panel is gone, still without a push', async () => {
    render(
      <SidePanelShell panels={[library]} username="dana" chat={<div>chat</div>} />,
    )
    act(() => {
      usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    act(() => RowRO.last!.fire(1400))
    const replace = vi.spyOn(window.history, 'replaceState')
    const push = vi.spyOn(window.history, 'pushState')
    act(() => {
      usePanelShellStore.getState().closePanel()
    })
    await act(async () => {})
    const written = urlsOf(replace)
    expect(written.length).toBeGreaterThan(0)
    expect(written.some((u) => u.includes('panel='))).toBe(false)
    expect(push).not.toHaveBeenCalled()
  })

  it('RED — a popstate with a stale panel value re-projects the STORE panel (MAJ-206)', () => {
    render(
      <SidePanelShell panels={[library]} username="dana" chat={<div>chat</div>} />,
    )
    act(() => {
      usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    act(() => RowRO.last!.fire(1400))
    const replace = vi.spyOn(window.history, 'replaceState')
    window.dispatchEvent(new PopStateEvent('popstate', { state: { panel: 'calendar' } }))
    expect(usePanelShellStore.getState().activePanel?.id).toBe('library')
    expect(urlsOf(replace).some((u) => u.includes('panel=library'))).toBe(true)
  })
})

describe('full-page routes still exist (US-5.4) — characterisation, green on wave-0 code', () => {
  it('library, team and calendar routes still export a page component', () => {
    expect(typeof LibraryRoute.options.component).toBe('function')
    expect(typeof TeamRoute.options.component).toBe('function')
    expect(typeof CalendarRoute.options.component).toBe('function')
  })
})
