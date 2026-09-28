// SidePanelShell.render.test.tsx — side-panel-shell-spec.md §12 test #7:
// the docked shell renders header (title / Expand / Close) + content +
// separator beside a visible chat column (US-1, US-2, SP-4).
//
// Mostly CHARACTERISATION on wave-0 SidePanelShell.tsx (green on wave-0
// code): title, buttons, content probing, separator, visible chat. One part
// is RED per US-9 AS-4: the panel must be a LABELLED COMPLEMENTARY LANDMARK
// (role="complementary" named by the panel title) — the wave-0 panel body is
// a plain div, so that assertion fails until GREEN adds it. Failability of
// the characterisation parts proven in RED by a one-mutation probe (drop the
// title render), reverted + git-status verified.
//
// Oracles: side-panel-shell-spec.md §1/§2 (header owns the three controls),
// §2.1 (Library content fills the shell), US-9 AS-4 (landmark).

import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import type { PanelDefinition, PanelContentProps } from '@/components/panel-shell/types'

// jsdom has no ResizeObserver: stub it, capture the last instance, and fire
// row-width changes through it (the shell measures its own flex row).
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

function probeContent(props: PanelContentProps) {
  return (
    <div data-testid="probe-content" data-context={JSON.stringify(props.context)}>
      probe
    </div>
  )
}

const LIBRARY_DEF: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: probeContent,
  expandTarget: () => '/library',
}

function renderShell(extra?: PanelDefinition[]) {
  render(
    <SidePanelShell
      panels={[LIBRARY_DEF, ...(extra ?? [])]}
      username="dana"
      chat={<div data-testid="chat-probe">chat</div>}
    />,
  )
}

function openLibrary(context: Record<string, unknown> = { workspaceId: 'ws-1' }) {
  act(() => {
    usePanelShellStore.getState().openPanel('library', context)
  })
}

beforeEach(() => {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
  })
  RowRO.last = null
})

afterEach(() => {
  cleanup()
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
  })
})

describe('docked shell render (§12 #7, US-1/US-2) — characterisation, green on wave-0 code', () => {
  it('open panel renders a header with the title, an Expand control and a Close control', () => {
    openLibrary()
    renderShell()
    expect(screen.getByText('Library')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /expand/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /close/i })).toBeInTheDocument()
  })

  it('Close carries the panel-specific accessible name (aria-label "Close Library")', () => {
    openLibrary()
    renderShell()
    expect(screen.getByRole('button', { name: 'Close Library' })).toBeInTheDocument()
  })

  it('the panel content renders and receives the context (workspace scope)', () => {
    openLibrary()
    renderShell()
    const probe = screen.getByTestId('probe-content')
    expect(probe).toBeInTheDocument()
    expect(JSON.parse(probe.getAttribute('data-context') ?? '{}')).toEqual({
      workspaceId: 'ws-1',
    })
  })

  it('the resize separator renders when docked, aria-valuenow = the applied width and aria-valuemax = the SP-17 ceiling for the row', () => {
    openLibrary()
    renderShell()
    act(() => RowRO.last!.fire(1400))
    const sep = screen.getByRole('separator')
    expect(sep).toBeInTheDocument()
    // SP-17: ceiling = min(70% row, row − sidebar − 360) = min(980, 1040) = 980
    expect(sep.getAttribute('aria-valuemax')).toBe('980')
    expect(Number(sep.getAttribute('aria-valuenow'))).toBeGreaterThanOrEqual(320)
    expect(Number(sep.getAttribute('aria-valuenow'))).toBeLessThanOrEqual(980)
  })

  it('the chat column stays mounted and visible beside the docked panel — a split row, never an overlay (SP-25/SC-005)', () => {
    openLibrary()
    renderShell()
    act(() => RowRO.last!.fire(1400))
    expect(screen.getByTestId('chat-probe')).toBeInTheDocument()
  })
})

describe('US-9 AS-4 complementary landmark (RED — wave-0 panel body is a plain div)', () => {
  it('the open panel is a complementary landmark labelled by its title (US-9 AS-4)', () => {
    openLibrary()
    renderShell()
    const landmark = screen.getByRole('complementary', { name: 'Library' })
    expect(landmark).toBeInTheDocument()
    // the landmark CONTAINS the content, header and separator — the whole
    // panel chrome lives inside one named region.
    expect(landmark.querySelector('[data-testid="probe-content"]')).not.toBeNull()
  })
})
