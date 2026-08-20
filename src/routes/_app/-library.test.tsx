// Unit tests for the /library pop-out route (library-spec.md D-4) — mirrors
// -browser-live.test.tsx's mocking approach: TanStack Router's
// createFileRoute is stubbed with a fixed `useSearch`, and LibraryExplorer
// itself is mocked (its own behaviour lives in LibraryExplorer.test.tsx).
//
// Covers: the `workspace` search param is threaded through as
// `initialWorkspaceId` (undefined → virtual root, same contract as
// LibraryPanel's docked opener), and closing the tab (pagehide) announces the
// library pop-out-closed handoff signal.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'

let mockSearch: { workspace?: string } = {}

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType; validateSearch?: unknown }) => ({
      ...opts,
      useSearch: () => mockSearch,
    }),
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
    className?: string
    onWorkspaceChange?: (workspaceId: string | null) => void
  }) => {
    mockLibraryExplorerProps(props)
    return <div data-testid="mock-library-explorer" />
  },
}))

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
  mockSearch = {}
})

describe('/library pop-out route', () => {
  it('renders LibraryExplorer scoped to the workspace search param when present', () => {
    mockSearch = { workspace: 'ws-1' }
    render(<LibraryRoute />)

    expect(screen.getByTestId('mock-library-explorer')).toBeInTheDocument()
    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(
      expect.objectContaining({ initialWorkspaceId: 'ws-1', className: 'absolute inset-0' }),
    )
  })

  it('renders LibraryExplorer at the virtual root when the workspace param is absent — same contract as the docked panel', () => {
    mockSearch = {}
    render(<LibraryRoute />)

    expect(mockLibraryExplorerProps).toHaveBeenCalledWith(
      expect.objectContaining({ initialWorkspaceId: undefined }),
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

  // UAT fix: the `workspace` search param only reflects what the tab was
  // OPENED with — LibraryExplorer's in-tab navigation (e.g. drilling from
  // the virtual root into a different workspace) is its own internal state,
  // never synced back to the URL. Before this fix, `handlePageHide` closed
  // over the initial `workspace` value, so closing the tab after navigating
  // elsewhere announced the STALE original workspace and the docked panel
  // re-docked to the wrong place.
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
})
