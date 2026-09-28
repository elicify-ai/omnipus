import { useEffect } from 'react'
import { act, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'
import type { PanelContentProps, PanelContext } from '@/components/panel-shell/types'

const explorerProps = vi.fn()
vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    initialWorkspaceId?: string
    address?: Record<string, unknown>
    onAddressChange?: (address: Record<string, unknown>) => void
    onWorkspaceChange?: (workspaceId: string | null) => void
    onSelectionChange?: (selection: { path: string | null; folder: string }) => void
    onClose?: () => void
    onPopOut?: () => void
    layout?: string
  }) => {
    explorerProps(props)
    useEffect(() => {
      props.onWorkspaceChange?.(props.initialWorkspaceId ?? null)
      props.onSelectionChange?.({ path: 'Notes/Current.md', folder: 'Notes' })
    }, [])
    return <div data-testid="mock-library-explorer" />
  },
}))

import { LibraryPanel } from './LibraryPanel'

let currentContext: (() => PanelContext) | null = null

function shellProps(
  presentation: PanelContentProps['presentation'],
  context: PanelContext,
): PanelContentProps {
  return {
    context,
    presentation,
    close: vi.fn(),
    expand: vi.fn(),
    registerExpandContext: (getter) => {
      currentContext = getter
    },
    onWidthSettle: vi.fn(),
  }
}

beforeEach(() => {
  currentContext = null
  explorerProps.mockClear()
  useUiStore.setState({ activePanel: null, toasts: [] })
})

describe('LibraryPanel shared presentation', () => {
  it('renders nothing while the Library is closed', () => {
    render(<LibraryPanel />)
    expect(screen.queryByTestId('mock-library-explorer')).not.toBeInTheDocument()
  })

  it('renders the store-owned dock as an aside without panel-specific actions', () => {
    render(<LibraryPanel />)
    act(() => useUiStore.getState().openPanel('library', { workspaceId: 'workspace-a' }))

    expect(screen.getByTestId('library-panel-docked').tagName).toBe('ASIDE')
    const props = explorerProps.mock.calls.at(-1)?.[0]
    expect(props).toEqual(expect.objectContaining({ initialWorkspaceId: 'workspace-a' }))
    expect(props.onClose).toBeUndefined()
    expect(props.onPopOut).toBeUndefined()
  })

  it('reports the docked explorer current selection to the shell', async () => {
    const props = shellProps('docked', { workspaceId: 'workspace-a' })
    act(() => useUiStore.getState().openPanel('library', { workspaceId: 'workspace-a' }))
    render(<LibraryPanel shellProps={props} />)

    await waitFor(() => expect(currentContext?.()).toEqual({
      workspaceId: 'workspace-a',
      path: 'Notes/Current.md',
      folder: undefined,
    }))
  })

  it('renders the same explorer full screen from the decoded selection', () => {
    const context = { workspaceId: 'workspace-a', path: 'Notes/Current.md' }
    render(<LibraryPanel shellProps={shellProps('fullscreen', context)} />)

    expect(screen.getByTestId('library-panel-fullscreen')).toBeInTheDocument()
    expect(explorerProps).toHaveBeenLastCalledWith(expect.objectContaining({
      address: context,
      layout: 'split',
    }))
    expect(currentContext?.()).toEqual(context)
  })
})
