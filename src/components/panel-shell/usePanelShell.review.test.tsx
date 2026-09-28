import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelDefinition } from './types'
import { usePanelShell, usePanelShellHistory } from './usePanelShell'
import { usePanelShellStore } from './panelShellStore'

function Probe() {
  return null
}

const library: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: Probe,
  expandTarget: () => '/library',
}

beforeEach(() => {
  localStorage.clear()
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
  usePanelShellStore.getState().closePanel()
})

describe('usePanelShell review regressions', () => {
  it('hydrates remembered width when production opens through the store', async () => {
    localStorage.setItem('panel-width:dana:library:ws-1', '612')
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })

    renderHook(() => usePanelShell([library], 'dana'))

    await waitFor(() => expect(usePanelShellStore.getState().panelWidth).toBe(612))
  })

  it('re-pushes the phone takeover entry when Back is declined by the leave guard', async () => {
    const guarded: PanelDefinition = {
      ...library,
      beforeLeave: async () => false,
    }
    const push = vi.spyOn(window.history, 'pushState')
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    renderHook(() => {
      const shell = usePanelShell([guarded], 'dana')
      usePanelShellHistory({ enabled: true, guardThenClose: shell.guardThenClose })
    })
    await waitFor(() => expect(usePanelShellStore.getState().historyPushed).toBe(true))
    push.mockClear()

    act(() => window.dispatchEvent(new PopStateEvent('popstate')))

    await waitFor(() => expect(usePanelShellStore.getState().historyPushed).toBe(true))
    expect(push).toHaveBeenCalledTimes(1)
    expect(usePanelShellStore.getState().activePanel?.id).toBe('library')
  })

  it('collapses the phone takeover history entry when the layout becomes docked', async () => {
    const back = vi.spyOn(window.history, 'back').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    const { rerender } = renderHook(
      ({ enabled }) => {
        const shell = usePanelShell([library], 'dana')
        usePanelShellHistory({ enabled, guardThenClose: shell.guardThenClose })
      },
      { initialProps: { enabled: true } },
    )
    await waitFor(() => expect(usePanelShellStore.getState().historyPushed).toBe(true))

    rerender({ enabled: false })

    await waitFor(() => expect(back).toHaveBeenCalledTimes(1))
    expect(usePanelShellStore.getState().historyPushed).toBe(false)
    expect(usePanelShellStore.getState().activePanel?.id).toBe('library')
  })

  it('collapses the phone takeover history entry after a non-shell close', async () => {
    const back = vi.spyOn(window.history, 'back').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    renderHook(() => {
      const shell = usePanelShell([library], 'dana')
      usePanelShellHistory({ enabled: true, guardThenClose: shell.guardThenClose })
    })
    await waitFor(() => expect(usePanelShellStore.getState().historyPushed).toBe(true))

    act(() => usePanelShellStore.getState().closePanel())

    await waitFor(() => expect(back).toHaveBeenCalledTimes(1))
    expect(usePanelShellStore.getState().historyPushed).toBe(false)
  })

  it('logs unexpected popup errors and distinguishes them from a blocked popup', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(window, 'open').mockImplementation(() => {
      throw new Error('window subsystem failed')
    })
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    const { result } = renderHook(() => usePanelShell([library], 'dana'))

    let outcome: Awaited<ReturnType<typeof result.current.requestExpand>> | undefined
    await act(async () => {
      outcome = await result.current.requestExpand()
    })

    expect(outcome).toBe('error')
    expect(error).toHaveBeenCalledWith('[side-panel] Expand failed', expect.any(Error))
    expect(usePanelShellStore.getState().activePanel?.id).toBe('library')
  })
})
