// SidePanelShell.browserNoShare.test.tsx — side-panel-shell-spec.md SP-37
// (gate-round-2 finding N3, founder ruling 2026-09-28): the Browser panel is
// NOT shareable — neither the docked panel content (the REAL
// BrowserLivePanel, docked exactly as the registry docks it, mirroring
// GREEN registry.tsx::BrowserPanelContent) nor the shell header for browser
// offers any share / copy-link affordance, and no header action writes the
// clipboard. Oracle: SP-37 + §12 #17's "no share or copy-link affordance".
//
// MOUNT PROOF (the pin's honesty gate): before asserting absence, the pack
// proves the REAL BrowserLiveView mounted docked and RAN its live-connect
// flow — the process-edge WebSocket was constructed and it sent its
// browser_attach frame. On the wave-0 tree the panel reads the RETIRED
// browserPanel slice, so docked-under-the-shell it cannot attach — both
// tests fail there with the STATED docking gap (never a vacuous absence
// assertion), and at the GREEN head the view attaches, making the absence
// assertions real. Failability of the absence itself is proven by one
// production mutation at the GREEN head (a "Copy link" button injected into
// the docked panel), reverted immediately.

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ComponentType } from 'react'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import { BrowserLivePanel } from '@/components/browser/BrowserLivePanel'
import type { PanelContentProps, PanelDefinition } from '@/components/panel-shell/types'

/** Wave-0 BrowserLivePanel takes no props; GREEN takes { shellProps }. The
 * structural cast keeps this file compiling on both trees and passing the
 * shell's props exactly as the registry does. */
const ShellBrowserPanel = BrowserLivePanel as unknown as ComponentType<{
  shellProps?: PanelContentProps
}>

function browserContent(props: PanelContentProps) {
  return <ShellBrowserPanel shellProps={props} />
}

/** The gateway stand-in at the process edge (same technique as the
 * cross-account pack): records frames, opens on demand. */
class AttachWebSocket {
  static last: AttachWebSocket | null = null
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  onopen: (() => void) | null = null
  onclose: ((ev: { code: number }) => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  sent: string[] = []
  readyState = 1
  constructor(public url: string) {
    AttachWebSocket.last = this
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.readyState = 3
  }
  serverOpen() {
    this.onopen?.()
  }
}
const originalWS = window.WebSocket

/** MIN-001: the shell measures its row via ResizeObserver — stubbed with a
 * controllable double, as in the other shell packs. */
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

beforeEach(() => {
  AttachWebSocket.last = null
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
  vi.stubGlobal('WebSocket', AttachWebSocket)
  vi.stubGlobal('ResizeObserver', RowRO)
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: null,
    guardPending: false,
    historyPushed: false,
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  Object.defineProperty(window, 'WebSocket', {
    configurable: true,
    writable: true,
    value: originalWS,
  })
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: null,
    guardPending: false,
    historyPushed: false,
  })
})

function renderShell() {
  const browserDef: PanelDefinition = {
    id: 'browser',
    title: 'Browser',
    content: browserContent,
    fullScreen: {
      toSearch: () => ({ session: 'session-1', agent: 'agent-1' }),
      fromSearch: () => ({ sessionId: 'session-1', agentId: 'agent-1' }),
    },
  }
  render(
    <SidePanelShell
      panels={[browserDef]}
      username="dana"
      chat={<textarea data-testid="chat-input" defaultValue="chat" />}
    />,
  )
}

/** Prove the real panel mounted docked and ran its live-connect flow —
 * WebSocket constructed + browser_attach sent (the process edge the real
 * gateway would answer). */
async function mountDockedBrowserPanel(): Promise<void> {
  act(() => {
    usePanelShellStore.getState().openPanel('browser', { sessionId: 's1', agentId: 'share-1' })
  })
  expect(screen.getByTestId('side-panel')).toBeInTheDocument()
  await waitFor(() => {
    if (AttachWebSocket.last === null) {
      throw new Error(
        'BLOCKED: the REAL BrowserLivePanel could not dock under the shell and run its live-connect flow ' +
          '(no WebSocket constructed) — required by §8.1/SP-4 docking (the wave-0 panel still reads the ' +
          'retired browserPanel slice; SP-37 pin needs the §8.1 single-slice docking)',
      )
    }
  })
  act(() => {
    AttachWebSocket.last!.serverOpen()
  })
  await waitFor(() => {
    if (!AttachWebSocket.last!.sent.some((f) => f.includes('"type":"browser_attach"'))) {
      throw new Error(
        'BLOCKED: the docked BrowserLivePanel never sent its browser_attach frame — the SP-37 ' +
          'absence assertions need the real live-view flow to have RUN (§8.1 docking)',
      )
    }
  })
}

describe('SP-37 — the Browser panel offers no share affordance', () => {
  let writeText: ReturnType<typeof vi.fn>
  let clipboardDefined = false

  beforeEach(() => {
    writeText = vi.fn()
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard')
    clipboardDefined = original !== undefined
    Object.defineProperty(window.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    if (clipboardDefined && original?.set) original.set.call(window.navigator, { writeText })
  })

  afterEach(() => {
    if (clipboardDefined) {
      Reflect.deleteProperty(window.navigator, 'clipboard')
    }
  })

  it('RED/BLOCKED — the docked REAL BrowserLivePanel renders no share / copy-link control (SP-37)', async () => {
    renderShell()
    await mountDockedBrowserPanel()
    // SP-37: no share / copy-link affordance — queried by role AND name
    // across the docked panel AND the shell header.
    const shareControls = screen.queryAllByRole('button', { name: /share|copy link/i })
    expect(shareControls).toEqual([])
  })

  it('RED/BLOCKED — no shell-header browser action writes the clipboard (SP-37)', async () => {
    renderShell()
    await mountDockedBrowserPanel()
    // EVERY shell-header action, driven by index (the DOM refreshes as the
    // panel closes/re-opens): a successful Expand closes the docked panel
    // (SP-12) as does the Close click, so the loop re-opens between actions.
    const openSpy = vi
      .spyOn(window, 'open')
      .mockReturnValue({
        closed: false,
        close: () => {},
        focus: () => {},
        opener: null,
        location: { replace: () => {} },
      } as unknown as Window)
    const headerButtons = () => within(screen.getByTestId('side-panel-header')).getAllByRole('button')
    const initialCount = headerButtons().length
    for (let i = 0; i < initialCount; i++) {
      let header = screen.queryByTestId('side-panel-header')
      let btn = header ? within(header).queryAllByRole('button')[i] : undefined
      if (!btn) {
        // A previous action closed the panel — re-open to expose the rest.
        act(() => {
          usePanelShellStore.getState().openPanel('browser', { sessionId: 's1', agentId: 'share-1' })
        })
        header = screen.getByTestId('side-panel-header')
        btn = within(header).queryAllByRole('button')[i]
      }
      if (!btn) continue
      fireEvent.click(btn)
      expect(writeText).not.toHaveBeenCalled()
    }
    expect(writeText).not.toHaveBeenCalled()
    openSpy.mockRestore()
  })
})
