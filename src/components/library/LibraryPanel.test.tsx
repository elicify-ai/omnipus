// LibraryPanel.test.tsx — docked <aside> + pop-out/re-dock handoff coverage
// (library-spec.md D-4). LibraryExplorer itself is mocked — its own behaviour
// is covered by LibraryExplorer.test.tsx. This file exercises ONLY what
// LibraryPanel itself is responsible for: the docked <aside>, the props it
// passes down, the deliberate "pop-out does not close the docked panel"
// choice, and the safety-net re-dock on a pop-out-closed broadcast.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { useUiStore } from '@/store/ui'
import { announceLibraryPopoutClosed } from '@/lib/libraryHandoff'

const mockLibraryExplorerProps = vi.fn()

vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    initialWorkspaceId?: string
    onClose?: () => void
    onPopOut?: () => void
  }) => {
    mockLibraryExplorerProps(props)
    return (
      <div data-testid="mock-library-explorer">
        {props.onClose && (
          <button type="button" tabIndex={0} onClick={props.onClose}>
            mock-close
          </button>
        )}
        {props.onPopOut && (
          <button type="button" tabIndex={0} onClick={props.onPopOut}>
            mock-pop-out
          </button>
        )}
      </div>
    )
  },
}))

import { LibraryPanel } from './LibraryPanel'

beforeEach(() => {
  mockLibraryExplorerProps.mockClear()
  useUiStore.setState({ libraryPanel: null, toasts: [] })
})

describe('LibraryPanel (always-docked)', () => {
  it('renders nothing when libraryPanel is closed (null)', () => {
    render(<LibraryPanel />)
    expect(screen.queryByTestId('mock-library-explorer')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-panel-docked')).not.toBeInTheDocument()
  })

  it('renders LibraryExplorer inside a docked <aside> at the virtual root when opened with no workspace — never a Sheet dialog', () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openLibraryPanel()
    })

    const docked = screen.getByTestId('library-panel-docked')
    expect(docked.tagName).toBe('ASIDE')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('mock-library-explorer')).toBeInTheDocument()
    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(
      expect.objectContaining({ initialWorkspaceId: undefined }),
    )
  })

  it('opens scoped to a workspace when the chat/header-bar entry point passes a workspaceId (D-3)', () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openLibraryPanel('ws-42')
    })

    expect(screen.getByTestId('library-panel-docked')).toBeInTheDocument()
    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(
      expect.objectContaining({ initialWorkspaceId: 'ws-42' }),
    )
  })

  it('closes via the panel close callback (onClose -> closeLibraryPanel)', () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openLibraryPanel('ws-1')
    })

    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(useUiStore.getState().libraryPanel).toBeNull()
    expect(screen.queryByTestId('library-panel-docked')).not.toBeInTheDocument()
  })

  describe('onPopOut (deliberately does NOT close the docked panel)', () => {
    it('opens the hash-routed pop-out URL with the current workspace in the query string', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)

      render(<LibraryPanel />)
      act(() => {
        useUiStore.getState().openLibraryPanel('ws-1')
      })

      fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))

      expect(openSpy).toHaveBeenCalledWith('/#/library?workspace=ws-1', '_blank', 'noopener,noreferrer')

      // DELIBERATE difference from BrowserLivePanel: the docked panel stays
      // open — there is no exclusive control lock to hand over (see
      // LibraryPanel.tsx's module doc comment).
      expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-1' })
      expect(screen.getByTestId('library-panel-docked')).toBeInTheDocument()

      openSpy.mockRestore()
    })

    it('opens the pop-out with no query string at the virtual root', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)

      render(<LibraryPanel />)
      act(() => {
        useUiStore.getState().openLibraryPanel()
      })

      fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))

      expect(openSpy).toHaveBeenCalledWith('/#/library', '_blank', 'noopener,noreferrer')

      openSpy.mockRestore()
    })
  })

  describe('re-docking on pop-out close (safety net)', () => {
    it('re-opens the docked panel for the workspace a pop-out announces closing, when nothing is currently docked', async () => {
      render(<LibraryPanel />)
      expect(useUiStore.getState().libraryPanel).toBeNull()
      expect(screen.queryByTestId('library-panel-docked')).not.toBeInTheDocument()

      act(() => {
        announceLibraryPopoutClosed('ws-99')
      })

      await waitFor(() => {
        expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-99' })
      })
      expect(screen.getByTestId('library-panel-docked')).toBeInTheDocument()
    })

    it('does NOT clobber an already-docked panel with a pop-out-closed broadcast', async () => {
      render(<LibraryPanel />)
      act(() => {
        useUiStore.getState().openLibraryPanel('ws-current')
      })
      expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-current' })

      act(() => {
        announceLibraryPopoutClosed('ws-other')
      })
      await new Promise((resolve) => setTimeout(resolve, 10))
      // Unchanged — both surfaces were legitimately open at once; the
      // broadcast is a no-op while something is already docked.
      expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-current' })
    })

    it('stops listening once unmounted — no re-dock after the panel itself is gone', async () => {
      const { unmount } = render(<LibraryPanel />)
      unmount()

      act(() => {
        announceLibraryPopoutClosed('ws-after-unmount')
      })
      await new Promise((resolve) => setTimeout(resolve, 10))
      expect(useUiStore.getState().libraryPanel).toBeNull()
    })
  })
})
