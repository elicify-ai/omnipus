// Unit tests for the /library pop-out route (library-spec.md D-4) — mirrors
// -browser-live.test.tsx's mocking approach: TanStack Router's
// createFileRoute is stubbed with a fixed `useSearch`, and LibraryExplorer
// itself is mocked (its own behaviour lives in LibraryExplorer.test.tsx).
//
// Covers: the `workspace` + `path` search params are threaded through as
// LibraryExplorer's `address` (both undefined → virtual root with nothing
// selected, the same contract as LibraryPanel's docked opener), every address
// change from the explorer is written back to the URL as a PUSH so the back
// button returns to the previously selected file (ADR-067 FR-012 / US-3),
// unsaved Library edits block a back-button navigation, and closing the tab
// (pagehide) announces the library pop-out-closed handoff signal.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'

let mockSearch: { workspace?: string; path?: string } = {}

const { mockNavigate, mockUseBlocker } = vi.hoisted(() => ({
  mockNavigate: vi.fn(),
  mockUseBlocker: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType; validateSearch?: unknown }) => ({
      ...opts,
      useSearch: () => mockSearch,
    }),
    useNavigate: () => mockNavigate,
    // The real useBlocker needs a live router; capturing the options is what
    // lets the guard test call shouldBlockFn the way the router would.
    useBlocker: (opts: unknown) => mockUseBlocker(opts),
  }
})

const { mockAnnounceLibraryPopoutClosed, mockAnnounceLibraryWorkspaceChanged, mockLibraryExplorerProps } = vi.hoisted(
  () => ({
    mockAnnounceLibraryPopoutClosed: vi.fn(),
    mockAnnounceLibraryWorkspaceChanged: vi.fn(),
    mockLibraryExplorerProps: vi.fn(),
  }),
)

vi.mock('@/lib/libraryHandoff', () => ({
  announceLibraryPopoutClosed: mockAnnounceLibraryPopoutClosed,
  announceLibraryWorkspaceChanged: mockAnnounceLibraryWorkspaceChanged,
}))

vi.mock('@/components/library/LibraryExplorer', () => ({
  LibraryExplorer: (props: {
    initialWorkspaceId?: string
    address?: { workspaceId?: string; path?: string }
    onAddressChange?: (next: { workspaceId?: string; path?: string }) => void
    className?: string
    onWorkspaceChange?: (workspaceId: string | null) => void
  }) => {
    mockLibraryExplorerProps(props)
    return <div data-testid="mock-library-explorer" />
  },
}))

import { setLibraryEditorDirty } from '@/components/library/preview/unsavedGuard'
import { Route } from './library'

// Route.component is the React component created by createFileRoute — cast
// through `unknown` first (mirrors -browser-live.test.tsx's identical
// extraction) since the REAL createFileRoute's generated Route type doesn't
// publicly expose `.component`, but the mocked factory above always attaches
// it (`{ ...opts, useSearch: ... }`, where `opts.component` is the real
// `LibraryRoute` function passed to `createFileRoute(...)(...)`).
const LibraryRoute = (Route as unknown as { component: React.ComponentType }).component

beforeEach(() => {
  mockAnnounceLibraryPopoutClosed.mockClear()
  mockAnnounceLibraryWorkspaceChanged.mockClear()
  mockLibraryExplorerProps.mockClear()
  mockNavigate.mockClear()
  mockUseBlocker.mockClear()
  mockSearch = {}
  setLibraryEditorDirty(false)
})

/** The props the route most recently handed LibraryExplorer. */
function explorerProps() {
  const calls = mockLibraryExplorerProps.mock.calls
  return calls[calls.length - 1][0] as {
    address?: { workspaceId?: string; path?: string }
    onAddressChange?: (next: { workspaceId?: string; path?: string }) => void
    onWorkspaceChange?: (workspaceId: string | null) => void
  }
}

describe('/library pop-out route', () => {
  it('renders LibraryExplorer scoped to the workspace search param when present', () => {
    mockSearch = { workspace: 'ws-1' }
    render(<LibraryRoute />)

    expect(screen.getByTestId('mock-library-explorer')).toBeInTheDocument()
    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(
      expect.objectContaining({
        address: { workspaceId: 'ws-1', path: undefined },
        className: 'absolute inset-0',
      }),
    )
  })

  it('renders LibraryExplorer at the virtual root when the workspace param is absent — same contract as the docked panel', () => {
    mockSearch = {}
    render(<LibraryRoute />)

    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(
      expect.objectContaining({ address: { workspaceId: undefined, path: undefined } }),
    )
  })

  it('announces the library pop-out-closed handoff on pagehide, for the exact workspace shown', () => {
    mockSearch = { workspace: 'ws-7' }
    render(<LibraryRoute />)

    window.dispatchEvent(new Event('pagehide'))

    expect(mockAnnounceLibraryPopoutClosed).toHaveBeenCalledWith('ws-7')
  })

  it('announces with undefined (virtual root) when no workspace was scoped', () => {
    mockSearch = {}
    render(<LibraryRoute />)

    window.dispatchEvent(new Event('pagehide'))

    expect(mockAnnounceLibraryPopoutClosed).toHaveBeenCalledWith(undefined)
  })

  // UAT fix: `handlePageHide` used to close over the `workspace` value the
  // tab was opened with, so closing the tab after navigating elsewhere
  // announced the STALE original workspace and the docked panel re-docked to
  // the wrong place. Deep-linking now also writes the param on every
  // workspace change, but the announcement deliberately still comes from
  // `onWorkspaceChange` and not from the param — a router navigation settles
  // a tick after the navigation itself, and at `pagehide` there is no later
  // tick. This test therefore drives `onWorkspaceChange` WITHOUT touching
  // `mockSearch`, which is exactly the case reading the URL would get wrong.
  it('announces the CURRENTLY-VIEWED workspace at pagehide time, even after in-tab navigation changed it since mount', () => {
    mockSearch = { workspace: 'ws-7' }
    render(<LibraryRoute />)

    const { onWorkspaceChange } = mockLibraryExplorerProps.mock.calls[0][0] as {
      onWorkspaceChange?: (workspaceId: string | null) => void
    }
    // Simulate the user navigating from the tab's initial workspace to a
    // different one purely inside LibraryExplorer — this does NOT touch the
    // route's `workspace` search param.
    onWorkspaceChange?.('ws-99')

    window.dispatchEvent(new Event('pagehide'))

    expect(mockAnnounceLibraryPopoutClosed).toHaveBeenCalledWith('ws-99')
  })

  it('announces undefined once in-tab navigation returns to the virtual root', () => {
    mockSearch = { workspace: 'ws-7' }
    render(<LibraryRoute />)

    const { onWorkspaceChange } = mockLibraryExplorerProps.mock.calls[0][0] as {
      onWorkspaceChange?: (workspaceId: string | null) => void
    }
    onWorkspaceChange?.(null)

    window.dispatchEvent(new Event('pagehide'))

    expect(mockAnnounceLibraryPopoutClosed).toHaveBeenCalledWith(undefined)
  })

  // UAT fix (Dana, re-verified v8 — "pop-out re-dock STILL does not restore
  // the workspace"): root-causing that regression required checking EVERY
  // link in the chain rather than re-guessing. This link — does the
  // callback fire on in-tab navigation, and does it publish CONTINUOUSLY
  // rather than only at `pagehide` teardown — is the one the first attempt
  // never actually verified with a test. `pagehide` + BroadcastChannel
  // delivery is asynchronous and a message posted during unload may never
  // arrive, so the workspace is now published the moment navigation
  // happens, not only when the tab is closing.
  describe('continuous workspace-changed broadcast (not only at pagehide teardown)', () => {
    it('announces the workspace-changed broadcast on the initial mount', () => {
      mockSearch = { workspace: 'ws-7' }
      render(<LibraryRoute />)

      const { onWorkspaceChange } = mockLibraryExplorerProps.mock.calls[0][0] as {
        onWorkspaceChange?: (workspaceId: string | null) => void
      }
      // LibraryExplorer's own effect fires onWorkspaceChange on mount too —
      // simulate that here since LibraryExplorer itself is mocked.
      onWorkspaceChange?.('ws-7')

      expect(mockAnnounceLibraryWorkspaceChanged).toHaveBeenCalledWith('ws-7')
    })

    it('announces EACH in-tab navigation immediately, well before any pagehide', () => {
      mockSearch = { workspace: 'ws-7' }
      render(<LibraryRoute />)

      const { onWorkspaceChange } = mockLibraryExplorerProps.mock.calls[0][0] as {
        onWorkspaceChange?: (workspaceId: string | null) => void
      }

      onWorkspaceChange?.('ws-99')
      expect(mockAnnounceLibraryWorkspaceChanged).toHaveBeenCalledWith('ws-99')
      // Not waiting for pagehide — the whole point of publishing
      // continuously is that the docked side already knows before teardown.
      expect(mockAnnounceLibraryPopoutClosed).not.toHaveBeenCalled()

      onWorkspaceChange?.('ws-other')
      expect(mockAnnounceLibraryWorkspaceChanged).toHaveBeenCalledWith('ws-other')
      expect(mockAnnounceLibraryWorkspaceChanged).toHaveBeenCalledTimes(2)
    })

    it('announces undefined (virtual root) as a workspace-changed broadcast too', () => {
      mockSearch = { workspace: 'ws-7' }
      render(<LibraryRoute />)

      const { onWorkspaceChange } = mockLibraryExplorerProps.mock.calls[0][0] as {
        onWorkspaceChange?: (workspaceId: string | null) => void
      }
      onWorkspaceChange?.(null)

      expect(mockAnnounceLibraryWorkspaceChanged).toHaveBeenCalledWith(undefined)
    })
  })

  // ── Deep-linking (ADR-067 FR-012 / US-3) ────────────────────────────────
  // The route is the only place that knows about URLs, so it is the only
  // place these can be asserted. LibraryExplorer's half — what it does with
  // an address it is handed — lives in LibraryExplorer.test.tsx.
  describe('deep-linking: the selected file is in the URL', () => {
    it('threads BOTH search params through as the explorer address, so a fresh load opens that file', () => {
      mockSearch = { workspace: 'ws-1', path: 'notes/plan.md' }
      render(<LibraryRoute />)

      expect(explorerProps().address).toEqual({ workspaceId: 'ws-1', path: 'notes/plan.md' })
    })

    it('writes a selected file back to the URL, keeping the workspace alongside it', () => {
      mockSearch = { workspace: 'ws-1' }
      render(<LibraryRoute />)

      explorerProps().onAddressChange?.({ workspaceId: 'ws-1', path: 'notes/plan.md' })

      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/library',
        search: { workspace: 'ws-1', path: 'notes/plan.md' },
      })
    })

    it('drops the path param when the selection is cleared, rather than leaving a URL pointing at a closed file', () => {
      mockSearch = { workspace: 'ws-1', path: 'notes/plan.md' }
      render(<LibraryRoute />)

      explorerProps().onAddressChange?.({ workspaceId: 'ws-1', path: undefined })

      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/library',
        search: { workspace: 'ws-1', path: undefined },
      })
    })

    it('does NOT pass replace, so each selected file is a back-button stop (US-3 AS-4)', () => {
      mockSearch = { workspace: 'ws-1' }
      render(<LibraryRoute />)

      explorerProps().onAddressChange?.({ workspaceId: 'ws-1', path: 'a.md' })

      const [[arg]] = mockNavigate.mock.calls as [[Record<string, unknown>]]
      // `replace: true` here would overwrite the current history entry, and
      // pressing back would skip past the previous file to whatever came
      // before the Library.
      expect(arg.replace).toBeUndefined()
    })

    it('stops passing initialWorkspaceId — two answers to "which workspace" is how the URL and the pane drift apart', () => {
      mockSearch = { workspace: 'ws-1' }
      render(<LibraryRoute />)

      expect(mockLibraryExplorerProps).not.toHaveBeenCalledWith(
        expect.objectContaining({ initialWorkspaceId: expect.anything() }),
      )
    })
  })

  // The explorer's own handlers guard in-app clicks; they cannot see the
  // browser's back button, which is a new way to lose an unsaved edit that
  // deep-linking itself introduces. The route blocks it.
  describe('unsaved-edit guard on browser navigation', () => {
    function shouldBlock(): boolean {
      const [[opts]] = mockUseBlocker.mock.calls as [[{ shouldBlockFn: () => boolean }]]
      return opts.shouldBlockFn()
    }

    it('does not block, and does not prompt, when nothing is unsaved', () => {
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
      render(<LibraryRoute />)

      expect(shouldBlock()).toBe(false)
      expect(confirmSpy).not.toHaveBeenCalled()
      confirmSpy.mockRestore()
    })

    it('blocks the navigation when the Library editor has unsaved edits and the operator chooses to stay', () => {
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
      render(<LibraryRoute />)
      setLibraryEditorDirty(true)

      expect(shouldBlock()).toBe(true)
      expect(confirmSpy).toHaveBeenCalled()
      confirmSpy.mockRestore()
    })

    it('lets the navigation through once the operator agrees to discard', () => {
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
      render(<LibraryRoute />)
      setLibraryEditorDirty(true)

      expect(shouldBlock()).toBe(false)
      confirmSpy.mockRestore()
    })

    it('leaves beforeunload to unsavedGuard.ts, so a reload prompts once and not twice', () => {
      render(<LibraryRoute />)

      const [[opts]] = mockUseBlocker.mock.calls as [[{ enableBeforeUnload?: boolean }]]
      expect(opts.enableBeforeUnload).toBe(false)
    })
  })
})
