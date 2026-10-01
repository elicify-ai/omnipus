import { useEffect } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import type { PanelContentProps } from './types'

vi.mock('@/lib/panelTabPresence', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/panelTabPresence')>()
  return {
    ...actual,
    switchToPanelTab: vi.fn(() => {
      if (focusResult === 'absent') existingResult = null
      return focusResult
    }),
    getPanelTabPresence: vi.fn(() => [{ panelId: 'library', workspaceId: 'ws-1' }]),
    resolveExistingPanelTab: vi.fn(() => existingResult),
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

let registeredExpandContext: (() => PanelContentProps['context']) | null = null
let focusResult: 'absent' | 'failed' = 'absent'
let existingResult: 'affordance' | null = 'affordance'

function ToastFixture() {
  const toasts = useUiStore((state) => state.toasts)
  return toasts.map((toast) => (
    <div key={toast.id}>
      {toast.message}
      {toast.action && (
        <Button variant="ghost" onClick={toast.action.onClick}>{toast.action.label}</Button>
      )}
    </div>
  ))
}

function shellProps(context: PanelContentProps['context']): PanelContentProps {
  return {
    context,
    presentation: 'docked',
    close: () => useUiStore.getState().closePanel(),
    expand: () => {},
    registerExpandContext: (getter) => {
      registeredExpandContext = getter
    },
    onWidthSettle: () => {},
  }
}

beforeEach(() => {
  registeredExpandContext = null
  focusResult = 'absent'
  existingResult = 'affordance'
  useUiStore.setState({ activePanel: null, toasts: [] })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useUiStore.setState({ activePanel: null, toasts: [] })
})

describe('SP-18 focus fallback', () => {
  it('PanelTabPresenceBridge restores locally only after the remote presence is absent', () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastFixture />
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
    expect(screen.queryByRole('button', { name: 'Open here' })).not.toBeInTheDocument()
  })

  it('keeps the dock closed and gives an honest manual-switch message when live focus fails', () => {
    focusResult = 'failed'
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastFixture />
      </>,
    )
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })

    fireEvent.click(screen.getByRole('button', { name: 'Switch' }))

    expect(useUiStore.getState().activePanel).toBeNull()
    expect(screen.getByText(/switch to that tab manually/i)).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Open here' })).not.toBeInTheDocument()
  })

  it('Library supplies its current identity context to the generic shell', async () => {
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    render(
      <>
        <PanelTabPresenceBridge />
        <LibraryPanel shellProps={shellProps({ workspaceId: 'ws-1' })} />
        <ToastFixture />
      </>,
    )
    await waitFor(() => expect(registeredExpandContext).not.toBeNull())
    expect(registeredExpandContext?.()).toEqual({ workspaceId: 'ws-1' })
  })

  it('Browser supplies its owner context to the generic shell', async () => {
    const context = { sessionId: 'session-1', agentId: 'agent-1' }
    act(() => {
      useUiStore.getState().openPanel('browser', context)
    })
    render(
      <>
        <PanelTabPresenceBridge />
        <BrowserLivePanel shellProps={shellProps(context)} />
        <ToastFixture />
      </>,
    )
    await waitFor(() => expect(registeredExpandContext).not.toBeNull())
    expect(registeredExpandContext?.()).toEqual(context)
  })
})
