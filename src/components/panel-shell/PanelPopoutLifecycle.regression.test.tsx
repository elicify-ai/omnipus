import { useEffect } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { announceLibraryPopoutClosed, announceLibraryWorkspaceChanged } from '@/lib/libraryHandoff'
import { useUiStore } from '@/store/ui'
import type { PanelContentProps, PanelDefinition } from './types'
import { SidePanelShell } from './SidePanelShell'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'

vi.mock('@/components/browser/BrowserLiveView', () => ({
  BrowserLiveView: () => <div data-testid="browser-view" />,
}))

vi.mock('@/components/library/LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    initialWorkspaceId?: string
    onWorkspaceChange?: (workspaceId: string | null) => void
    onSelectionChange?: (selection: { path: string | null; folder: string }) => void
  }) => {
    useEffect(() => {
      props.onWorkspaceChange?.(props.initialWorkspaceId ?? null)
      props.onSelectionChange?.({ path: null, folder: '' })
    }, [])
    return <div data-testid="library-explorer" />
  },
}))

import { BrowserLivePanel } from '@/components/browser/BrowserLivePanel'
import { LibraryPanel } from '@/components/library/LibraryPanel'

class RowResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

vi.stubGlobal('ResizeObserver', RowResizeObserver)

const browserDefinition: PanelDefinition = {
  id: 'browser',
  title: 'Browser',
  content: (props: PanelContentProps) => <BrowserLivePanel shellProps={props} />,
  expandTarget: () => '/#/browser-live?session=session-secret&agent=agent-secret',
}

const libraryDefinition: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: (props: PanelContentProps) => <LibraryPanel shellProps={props} />,
  expandTarget: () => '/#/library',
  beforeLeave: async () => true,
}

function popup() {
  const child = {
    closed: false,
    opener: {} as Window | null,
    close: vi.fn(),
    focus: vi.fn(),
    location: { replace: vi.fn() },
  }
  child.close.mockImplementation(() => {
    child.closed = true
  })
  return child
}

function renderShell(definition: PanelDefinition) {
  return render(
    <>
      <PanelTabPresenceBridge />
      <SidePanelShell
        panels={[definition]}
        username="dana"
        chat={<div data-testid="chat">chat</div>}
      />
    </>,
  )
}

beforeEach(() => {
  useUiStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
    toasts: [],
  })
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
  useUiStore.getState().closePanel()
})

describe('app-owned pop-out lifecycle survives SidePanelShell content unmount', () => {
  it('Browser Expand keeps the child alive and re-docks when that child closes', async () => {
    vi.useFakeTimers()
    const child = popup()
    vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    act(() => {
      useUiStore.getState().openPanel('browser', {
        sessionId: 'session-secret',
        agentId: 'agent-secret',
      })
    })
    renderShell(browserDefinition)

    fireEvent.click(screen.getByRole('button', { name: 'Expand Browser panel' }))
    await act(async () => {})

    expect(useUiStore.getState().activePanel).toBeNull()
    expect(child.close).not.toHaveBeenCalled()
    child.closed = true
    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'browser',
      context: { sessionId: 'session-secret', agentId: 'agent-secret' },
    })
  })

  it('Library Expand follows the child last workspace after the shell unmounts its content', async () => {
    const child = popup()
    vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'workspace-a' })
    })
    renderShell(libraryDefinition)

    fireEvent.click(screen.getByRole('button', { name: 'Expand Library panel' }))
    await waitFor(() => expect(useUiStore.getState().activePanel).toBeNull())
    expect(child.close).not.toHaveBeenCalled()

    act(() => {
      announceLibraryWorkspaceChanged('workspace-b')
      announceLibraryPopoutClosed('workspace-a')
    })
    await waitFor(() => {
      expect(useUiStore.getState().activePanel).toEqual({
        id: 'library',
        context: { workspaceId: 'workspace-b' },
      })
    })
  })

  it('Browser logs the outer handover failure before restoring the dock', async () => {
    const child = popup()
    child.location.replace.mockImplementation(() => {
      throw new Error('navigation denied')
    })
    vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    act(() => {
      useUiStore.getState().openPanel('browser', {
        sessionId: 'session-secret',
        agentId: 'agent-secret',
      })
    })
    renderShell(browserDefinition)

    fireEvent.click(screen.getByRole('button', { name: 'Expand Browser panel' }))
    await waitFor(() => expect(useUiStore.getState().activePanel?.id).toBe('browser'))

    expect(error).toHaveBeenCalledTimes(1)
  })
})
