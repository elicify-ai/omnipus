// SidePanelShell.guard.test.tsx — side-panel-shell-spec.md §12 #8/#8b +
// Expand + MAJ-209 + #9b (FR-015 settle reaching the content).
//
// RED items: Expand neither runs beforeLeave nor closes the docked panel in
// wave 0 (requestExpand just window.open()s), and PanelContentProps
// {context, close, expand} carries no width-settle channel (FR-015's
// browser handover trigger). CHARACTERISATION: header-Close guard paths,
// cancelled-clean (#8b), MAJ-209 blocked-popup error surface.
//
// Oracles: side-panel-shell-spec.md §8.1 (store/URL/content move only after
// beforeLeave resolves true), SP-12 (Expand closes the source panel),
// FR-013/CRIT-001 (every close path gates), MAJ-209 (window.open
// synchronously in the gesture; blocked -> visible error, panel stays),
// FR-015 + MIN-205 (a settle write doubles as the browser handover trigger).

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

let capturedProps: PanelContentProps | null = null

function probeContent(props: PanelContentProps) {
  capturedProps = props
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
    fullScreen: {
      toSearch: () => ({}),
      fromSearch: () => id === 'browser'
        ? { sessionId: 's1', agentId: 'a1' }
        : {},
    },
    ...(beforeLeave ? { beforeLeave } : {}),
  }
}

function renderShell(panels: PanelDefinition[]) {
  render(
    <SidePanelShell
      panels={panels}
      username="dana"
      chat={<div data-testid="chat-probe">chat</div>}
    />,
  )
}

function openPanel(id: 'library' | 'browser', context: Record<string, unknown> = {}) {
  act(() => {
    usePanelShellStore.getState().openPanel(id, context)
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
  resetStore()
  RowRO.last = null
  capturedProps = null
  vi.spyOn(window, 'open').mockReturnValue(null)
})

afterEach(() => {
  cleanup()
  resetStore()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('guard paths on the header Close (§12 #8) — characterisation, green on wave-0 code', () => {
  it('Close with an ACCEPTED guard: beforeLeave runs, then the panel closes', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(true)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library')
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    await waitFor(() => {
      expect(beforeLeave).toHaveBeenCalledTimes(1)
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('Close with a DECLINED guard: the panel STAYS open', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(false)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library')
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    await waitFor(() => expect(beforeLeave).toHaveBeenCalledTimes(1))
    await act(async () => {})
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
  })

  it('CLOSE is a no-op while a guard is already pending (FR-012: one gate at a time)', async () => {
    let resolveGuard: (v: boolean) => void = () => {}
    const beforeLeave = vi.fn().mockReturnValue(
      new Promise<boolean>((resolve) => {
        resolveGuard = resolve
      }),
    )
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library')
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    await act(async () => {
      resolveGuard(true)
    })
    expect(beforeLeave).toHaveBeenCalledTimes(1)
  })
})

describe('#8b cancelled transition leaves everything untouched (FR-013) — characterisation', () => {
  it('a DECLINED guard leaves activePanel, panelWidth and the mounted content exactly as they were', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(false)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library', { workspaceId: 'ws-1' })
    const before = usePanelShellStore.getState().activePanel
    const widthBefore = usePanelShellStore.getState().panelWidth
    const elBefore = screen.getByTestId('probe-content')
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
    await waitFor(() => expect(beforeLeave).toHaveBeenCalledTimes(1))
    await act(async () => {})
    expect(usePanelShellStore.getState().activePanel).toEqual(before)
    expect(usePanelShellStore.getState().panelWidth).toBe(widthBefore)
    expect(screen.getByTestId('probe-content')).toBe(elBefore)
  })
})

describe('Expand (SP-12 + FR-013 + MAJ-209)', () => {
  it('RED — expand runs the outgoing beforeLeave BEFORE window.open (MAJ-209 sync-in-gesture order)', async () => {
    const order: string[] = []
    const beforeLeave = vi.fn(async () => {
      order.push('guard')
      return true
    })
    vi.spyOn(window, 'open').mockImplementation(() => {
      order.push('open')
      return null
    })
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library', { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: /expand/i }))
    await waitFor(() => expect(order).toEqual(['guard', 'open']))
  })

  it('RED — a declined expand guard keeps the docked panel open and never opens a pop-out', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(false)
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library', { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: /expand/i }))
    await waitFor(() => expect(beforeLeave).toHaveBeenCalledTimes(1))
    await act(async () => {})
    expect(usePanelShellStore.getState().activePanel).not.toBeNull()
    expect(openSpy).not.toHaveBeenCalled()
  })

  it('RED — an accepted expand guard CLOSES the docked panel after the pop-out opens (SP-12)', async () => {
    const beforeLeave = vi.fn().mockResolvedValue(true)
    vi.spyOn(window, 'open').mockReturnValue({
      closed: false,
      opener: {},
      close: vi.fn(),
      focus: vi.fn(),
      location: { replace: vi.fn() },
    } as unknown as Window)
    renderShell([makeDef('library', beforeLeave)])
    openPanel('library', { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: /expand/i }))
    await waitFor(() => {
      expect(usePanelShellStore.getState().activePanel).toBeNull()
    })
  })

  it('MAJ-209: a BLOCKED popup shows the visible error and the panel stays — characterisation', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    renderShell([makeDef('browser')])
    openPanel('browser', { sessionId: 's1', agentId: 'a1' })
    fireEvent.click(screen.getByRole('button', { name: /expand/i }))
    await act(async () => {})
    expect(screen.getByTestId('panel-expand-error')).toBeInTheDocument()
    expect(usePanelShellStore.getState().activePanel?.id).toBe('browser')
  })
})

describe('#9b — a width settle must REACH the panel content (FR-015 browser handover trigger) — RED', () => {
  it('a keyboard resize settle notifies the content with the settled width (FR-015 + MIN-205)', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout'] })
    renderShell([makeDef('browser')])
    openPanel('browser', { sessionId: 's1', agentId: 'a1' })
    const sep = screen.getByRole('separator')
    fireEvent.keyDown(sep, { key: 'ArrowLeft' })
    vi.advanceTimersByTime(300)
    await act(async () => {})
    const props = capturedProps as unknown as Record<string, unknown> | null
    expect(props).not.toBeNull()
    const settle = props?.onWidthSettle ?? props?.settleWidth
    expect(typeof settle).toBe('function')
  })
})
