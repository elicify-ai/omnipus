// Wave 1 (side-panel-shell-spec.md §8.1/§3.1): the retired libraryPanel slice was
// translated to the single activePanel slice below, and the re-dock-on-popout-close
// scenarios follow MAJ-006 (empty-slot only) — see the marked test.
// LibraryPanel.test.tsx — docked <aside> + pop-out/re-dock handoff coverage
// (library-spec.md D-4). LibraryExplorer itself is mocked — its own behaviour
// is covered by LibraryExplorer.test.tsx. This file exercises ONLY what
// LibraryPanel itself is responsible for: the docked <aside>, the props it
// passes down, the C4 "pop-out carries the selection and closes the
// slide-out" behaviour, and the safety-net re-dock on a pop-out-closed
// broadcast.

import { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { act } from 'react'
import { useUiStore } from '@/store/ui'
import { announceLibraryPopoutClosed, announceLibraryWorkspaceChanged } from '@/lib/libraryHandoff'
import type { PanelContentProps } from '@/components/panel-shell/types'

vi.mock('@/lib/constants', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/constants')>()),
  generateId: () => 'library-popout-test',
}))

const mockLibraryExplorerProps = vi.fn()

// The mock's own "current selection" — settable per test via
// `setMockLiveSelection` so a test can simulate the docked panel having
// navigated somewhere (a different workspace, a browsed folder, an open
// file) before the operator clicks pop-out. Defaults to "nothing selected,
// at the initial workspace", matching the real LibraryExplorer's own
// initial-mount report.
let mockLiveWorkspaceId: string | null | undefined
let mockLiveSelection: { path: string | null; folder: string } = {
  path: null,
  folder: '',
}
let registeredExpand: (() => boolean) | null = null

vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    initialWorkspaceId?: string
    onClose?: () => void
    onPopOut?: () => void
    onWorkspaceChange?: (workspaceId: string | null) => void
    onSelectionChange?: (selection: { path: string | null; folder: string }) => void
  }) => {
    mockLibraryExplorerProps(props)
    // Mirrors the real component's onWorkspaceChange/onSelectionChange
    // effects, which fire on every mount (including the very first) — see
    // LibraryExplorer.tsx. `mockLiveWorkspaceId` defaults to
    // `initialWorkspaceId` unless a test overrides it via
    // `setMockLiveWorkspaceId` to simulate in-panel navigation.
    useEffect(() => {
      props.onWorkspaceChange?.(mockLiveWorkspaceId ?? props.initialWorkspaceId ?? null)
      props.onSelectionChange?.(mockLiveSelection)
    }, [])
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
import type { WorkspacePanelContext } from '@/components/panel-shell/types'

beforeEach(() => {
  mockLibraryExplorerProps.mockClear()
  useUiStore.setState({ activePanel: null, toasts: [] })
  mockLiveWorkspaceId = undefined
  mockLiveSelection = { path: null, folder: '' }
  registeredExpand = null
})

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

function renderShellHostedLibrary(context: WorkspacePanelContext) {
  act(() => {
    useUiStore.getState().openPanel('library', context)
  })
  return render(<LibraryPanel shellProps={shellProps(context)} />)
}

function invokeShellExpand(): boolean {
  let opened = false
  act(() => {
    opened = registeredExpand?.() ?? false
    if (opened) useUiStore.getState().closePanel()
  })
  return opened
}

function mockPopup() {
  const popup = {
    closed: false,
    opener: {},
    focus: vi.fn(),
    close: vi.fn(),
  }
  popup.close.mockImplementation(() => {
    popup.closed = true
  })
  const openSpy = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
  return { openSpy, popup }
}

describe('LibraryPanel (always-docked)', () => {
  it('renders nothing when libraryPanel is closed (null)', () => {
    render(<LibraryPanel />)
    expect(screen.queryByTestId('mock-library-explorer')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-panel-docked')).not.toBeInTheDocument()
  })

  it('renders LibraryExplorer inside a docked <aside> at the virtual root when opened with no workspace — never a Sheet dialog', () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openPanel('library', {})
    })

    const docked = screen.getByTestId('library-panel-docked')
    expect(docked.tagName).toBe('ASIDE')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('mock-library-explorer')).toBeInTheDocument()
    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(expect.objectContaining({ initialWorkspaceId: undefined }))
  })

  it('opens scoped to a workspace when the chat/header-bar entry point passes a workspaceId (D-3)', () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-42' })
    })

    expect(screen.getByTestId('library-panel-docked')).toBeInTheDocument()
    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(expect.objectContaining({ initialWorkspaceId: 'ws-42' }))
  })

  it('leaves Close and Expand out of LibraryExplorer because the shell header owns them (§2.2)', () => {
    render(<LibraryPanel />)
    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })

    const props = mockLibraryExplorerProps.mock.calls.at(-1)?.[0]
    expect(props?.onClose).toBeUndefined()
    expect(props?.onPopOut).toBeUndefined()
  })

  describe('shell-registered Expand (C4 — carries the current selection)', () => {
    it('opens the hash-routed pop-out URL with the current workspace in the query string', () => {
      const { openSpy, popup } = mockPopup()
      renderShellHostedLibrary({ workspaceId: 'ws-1' })

      expect(invokeShellExpand()).toBe(true)

      expect(openSpy).toHaveBeenCalledWith('/#/library?workspace=ws-1&popout=library-popout-test', '_blank')
      expect(popup.opener).toBeNull()
      // C4: the slide-out closes now that the fullscreen tab shows the same
      // place — see LibraryPanel.tsx's module doc "C4 UPDATE" note for why
      // this reverses the panel's old "never close on pop-out" behaviour.
      expect(useUiStore.getState().activePanel).toBeNull()
      expect(screen.queryByTestId('library-panel-docked')).not.toBeInTheDocument()

      vi.mocked(window.open).mockRestore()
    })

    it('opens the pop-out with no query string at the virtual root, nothing selected', () => {
      const { openSpy } = mockPopup()
      renderShellHostedLibrary({})

      invokeShellExpand()

      expect(openSpy).toHaveBeenCalledWith('/#/library?popout=library-popout-test', '_blank')

      vi.mocked(window.open).mockRestore()
    })

    it('carries a selected FILE as `path` in the pop-out URL', () => {
      const { openSpy } = mockPopup()
      mockLiveSelection = {
        path: '01-Areas/CRM/notes.md',
        folder: '01-Areas/CRM',
      }

      renderShellHostedLibrary({ workspaceId: 'ws-1' })
      invokeShellExpand()

      // `path` wins over `folder` — a selected file already implies its own
      // folder (LibraryAddress/`selectedDir`), so only one needs to travel.
      expect(openSpy).toHaveBeenCalledWith('/#/library?workspace=ws-1&path=01-Areas%2FCRM%2Fnotes.md&popout=library-popout-test', '_blank')

      vi.mocked(window.open).mockRestore()
    })

    it('carries a browsed FOLDER as `folder` in the pop-out URL when nothing is selected', () => {
      const { openSpy } = mockPopup()
      mockLiveSelection = { path: null, folder: '01-Areas/CRM' }

      renderShellHostedLibrary({ workspaceId: 'ws-1' })
      invokeShellExpand()

      expect(openSpy).toHaveBeenCalledWith(
        '/#/library?workspace=ws-1&folder=01-Areas%2FCRM&popout=library-popout-test',
        '_blank',
      )

      vi.mocked(window.open).mockRestore()
    })

    it('carries the workspace the operator actually navigated to in the docked panel, not the one it was opened with', () => {
      const { openSpy } = mockPopup()
      // Opened at the virtual root, but the operator drilled into ws-live
      // inside the docked panel without closing it — libraryPanel.workspaceId
      // (the store's OPEN-TIME value) never learns about that on its own.
      mockLiveWorkspaceId = 'ws-live'

      renderShellHostedLibrary({})
      invokeShellExpand()

      expect(openSpy).toHaveBeenCalledWith(
        '/#/library?workspace=ws-live&popout=library-popout-test',
        '_blank',
      )

      vi.mocked(window.open).mockRestore()
    })
  })

  describe('re-docking / re-targeting on pop-out close', () => {
    it('re-opens the docked panel for the workspace a pop-out announces closing, when nothing is currently docked', async () => {
      mockPopup()
      renderShellHostedLibrary({ workspaceId: 'ws-opened' })
      invokeShellExpand()

      act(() => {
        announceLibraryPopoutClosed('ws-99')
      })

      await waitFor(() => {
        expect(useUiStore.getState().activePanel).toEqual({
          id: 'library',
          context: { workspaceId: 'ws-99' },
        })
      })
      expect(screen.getByTestId('library-panel-docked')).toBeInTheDocument()
    })

    // UAT fix (Dana, re-verified v8): this is the tester's EXACT repro — pop
    // out from "My Workspace" (docked panel stays open the whole time),
    // navigate the pop-out to a different workspace, close it. The docked
    // panel must now re-target to that workspace, not silently keep showing
    // the stale one. The OLD behaviour ("does NOT clobber an already-docked
    // panel") was itself the root cause of the bug this fix wave was asked
    // to root-cause — it made the re-dock a no-op in exactly the scenario
    // that matters, regardless of whether the message-passing chain worked.
    // Wave 1 REVERSES the unconditional re-target (side-panel-shell-spec.md
    // MAJ-006 + MAJ-208: a pop-out's close re-docks ONLY into an EMPTY slot —
    // the §8.3 handle registry adds the opener-only half in the shell
    // wiring). An already-open panel — Library OR Browser, SP-7's single
    // slot — is never clobbered by the handoff. (The Dana UAT fix above
    // still holds for the empty-slot case: the continuously-known workspace
    // is what re-docks.)
    it('does NOT clobber an already-open panel when a pop-out announces closing', async () => {
      mockPopup()
      renderShellHostedLibrary({ workspaceId: 'ws-opened' })
      invokeShellExpand()
      act(() => {
        useUiStore.getState().openPanel('browser', { sessionId: 's1', agentId: 'a1' })
      })

      act(() => {
        announceLibraryPopoutClosed('ws-other')
      })

      await new Promise((resolve) => setTimeout(resolve, 10))
      expect(useUiStore.getState().activePanel).toEqual({
        id: 'browser',
        context: { sessionId: 's1', agentId: 'a1' },
      })
    })

    // Verifies the actual root-cause fix rather than just the guard removal:
    // the continuously-published `workspace-changed` broadcast (posted on
    // every in-tab navigation — see libraryHandoff.ts) is what the docked
    // panel actually applies at close time, NOT whatever payload the
    // `popout-closed` message itself happens to carry. This matters because
    // `pagehide` + BroadcastChannel is not a reliable delivery moment; the
    // continuous stream is the real source of truth.
    it('applies the CONTINUOUSLY-known workspace at close time, even when popout-closed itself carries a different/stale value (empty slot — MAJ-006)', async () => {
      mockPopup()
      renderShellHostedLibrary({ workspaceId: 'ws-opened' })
      invokeShellExpand()

      act(() => {
        announceLibraryWorkspaceChanged('ws-99')
      })
      act(() => {
        // Simulates a stale/lost payload at the unreliable pagehide moment —
        // the listener must prefer the continuously-known 'ws-99', not this.
        announceLibraryPopoutClosed('ws-stale')
      })

      await waitFor(() => {
        expect(useUiStore.getState().activePanel).toEqual({
          id: 'library',
          context: { workspaceId: 'ws-99' },
        })
      })
    })

    it('falls back to the popout-closed payload when no continuous workspace-changed broadcast was ever received (empty slot — MAJ-006)', async () => {
      mockPopup()
      renderShellHostedLibrary({ workspaceId: 'ws-opened' })
      invokeShellExpand()

      act(() => {
        announceLibraryPopoutClosed('ws-42')
      })

      await waitFor(() => {
        expect(useUiStore.getState().activePanel).toEqual({
          id: 'library',
          context: { workspaceId: 'ws-42' },
        })
      })
    })

    it('re-targets to the virtual root (undefined) when the pop-out closed there (empty slot — MAJ-006)', async () => {
      mockPopup()
      renderShellHostedLibrary({ workspaceId: 'ws-opened' })
      invokeShellExpand()

      act(() => {
        announceLibraryWorkspaceChanged(undefined)
      })
      act(() => {
        announceLibraryPopoutClosed(undefined)
      })

      await waitFor(() => {
        expect(useUiStore.getState().activePanel).toEqual({
          id: 'library',
          context: { workspaceId: undefined },
        })
      })
    })

    it('stops listening once unmounted — no re-dock after the panel itself is gone', async () => {
      const { unmount } = render(<LibraryPanel />)
      unmount()

      act(() => {
        announceLibraryPopoutClosed('ws-after-unmount')
      })
      await new Promise((resolve) => setTimeout(resolve, 10))
      expect(useUiStore.getState().activePanel).toBeNull()
    })
  })
})
