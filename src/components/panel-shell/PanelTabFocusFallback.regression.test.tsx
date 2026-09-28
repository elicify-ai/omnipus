import { useEffect } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastContainer } from '@/components/ui/toast-container'
import { useUiStore } from '@/store/ui'
import type { PanelContentProps } from './types'

vi.mock('@/lib/panelTabPresence', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/panelTabPresence')>()
  return {
    ...actual,
    focusPanelTab: vi.fn(() => false),
    getPanelTabPresence: vi.fn(() => [{ panelId: 'library', workspaceId: 'ws-1' }]),
    resolveExistingPanelTab: vi.fn(() => 'affordance' as const),
    resolvePanelOpen: vi.fn(() => ({ kind: 'affordance' as const })),
    resolveRegisteredPanelOpen: vi.fn(() => ({ kind: 'affordance' as const })),
    startPanelTabPresenceMonitor: vi.fn(() => () => {}),
  }
})

vi.mock('@/components/browser/BrowserLiveView', () => ({
  BrowserLiveView: () => <div data-testid="browser-view" />,
}))

vi.mock('@/components/library/LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    initialWorkspaceId?: string
    onWorkspaceChange?: (workspaceId: string | null) => void
  }) => {
    useEffect(() => {
      props.onWorkspaceChange?.(props.initialWorkspaceId ?? null)
    }, [])
    return <div data-testid="library-explorer" />
  },
}))

import { BrowserLivePanel } from '@/components/browser/BrowserLivePanel'
import { LibraryPanel } from '@/components/library/LibraryPanel'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'

let registeredExpand: (() => boolean) | null = null

function shellProps(context: PanelContentProps['context']): PanelContentProps {
  return {
    context,
    close: () => useUiStore.getState().closePanel(),
    expand: () => {},
    registerExpand: (action) => {
      registeredExpand = action
    },
    onWidthSettle: () => {},
  }
}

function invokeExpandAndClose(): void {
  act(() => {
    if (registeredExpand?.()) useUiStore.getState().closePanel()
  })
}

beforeEach(() => {
  registeredExpand = null
  useUiStore.setState({ activePanel: null, toasts: [] })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useUiStore.setState({ activePanel: null, toasts: [] })
})

describe('failed already-open focus reopens the panel locally', () => {
  it('PanelTabPresenceBridge restores the panel when its Switch action cannot focus', () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastContainer />
      </>,
    )
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    expect(useUiStore.getState().activePanel).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Switch' }))

    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'ws-1' },
    })
  })

  it('Library Expand restores its dock when the other tab cannot be focused', async () => {
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    render(
      <>
        <PanelTabPresenceBridge />
        <LibraryPanel shellProps={shellProps({ workspaceId: 'ws-1' })} />
        <ToastContainer />
      </>,
    )
    await waitFor(() => expect(registeredExpand).not.toBeNull())
    invokeExpandAndClose()
    expect(useUiStore.getState().activePanel).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Switch' }))

    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'ws-1' },
    })
  })

  it('Browser Expand restores its dock when the other tab cannot be focused', async () => {
    const context = { sessionId: 'session-1', agentId: 'agent-1' }
    act(() => {
      useUiStore.getState().openPanel('browser', context)
    })
    render(
      <>
        <PanelTabPresenceBridge />
        <BrowserLivePanel shellProps={shellProps(context)} />
        <ToastContainer />
      </>,
    )
    await waitFor(() => expect(registeredExpand).not.toBeNull())
    invokeExpandAndClose()
    expect(useUiStore.getState().activePanel).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Switch' }))

    expect(useUiStore.getState().activePanel).toEqual({
      id: 'browser',
      context,
    })
  })
})
