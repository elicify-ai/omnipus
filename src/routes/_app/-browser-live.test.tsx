// Unit tests for the /browser-live pop-out route (BrowserLiveRoute) —
// BUG 1 + BUG 2 fixes (live UAT re-run 2026-07-28).
//
// BUG 1: this route must pass `fillContainer` to BrowserLiveView so the
// media element fills the pop-out window instead of staying capped at
// intrinsic size (see BrowserLiveView.fillContainer.test.tsx for the
// sizing/coordinate-mapping proof itself — this file only proves the ROUTE
// wires the prop through).
//
// BUG 2: closing the pop-out (via the in-app Close button OR a native
// tab-close, which never runs any in-app handler) must announce a
// same-origin `popout-closed` signal for the EXACT (session, agent) this
// pop-out was showing, so BrowserLivePanel.tsx can re-dock itself — see
// BrowserLivePanel.test.tsx's "restores itself" tests for the consumer side.
//
// TanStack Router is mocked the same way -sessions.$sessionId.test.tsx does:
// createFileRoute's returned "Route" object needs a stubbed `useSearch` (the
// real one requires a full RouterProvider tree neither test needs).

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, screen } from '@testing-library/react'

const mockNavigate = vi.fn()
let mockSearch: { session?: string; agent?: string } = { session: 's1', agent: 'a1' }

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType; validateSearch?: unknown }) => ({
      ...opts,
      useSearch: () => mockSearch,
    }),
    useNavigate: () => mockNavigate,
  }
})

const { mockAnnouncePopoutClosed, mockBrowserLiveViewProps } = vi.hoisted(() => ({
  mockAnnouncePopoutClosed: vi.fn(),
  mockBrowserLiveViewProps: vi.fn(),
}))

vi.mock('@/lib/browserLiveHandoff', () => ({
  announcePopoutClosed: mockAnnouncePopoutClosed,
}))

vi.mock('@/components/browser/BrowserLiveView', () => ({
  BrowserLiveView: (props: { onClose?: () => void; fillContainer?: boolean; sessionId: string; agentId: string }) => {
    mockBrowserLiveViewProps(props)
    return (
      <div data-testid="mock-browser-live-view">
        {props.onClose && (
          <button type="button" onClick={props.onClose}>
            mock-close
          </button>
        )}
      </div>
    )
  },
}))

import { Route } from './browser-live'

// Route.component is the React component created by createFileRoute — cast
// through `unknown` first (mirrors -sessions.$sessionId.test.tsx's identical
// extraction) since the REAL createFileRoute's generated Route type doesn't
// publicly expose `.component`, but the mocked factory above always attaches
// it (`{ ...opts, useSearch: ... }`, where `opts.component` is the real
// `BrowserLiveRoute` function passed to `createFileRoute(...)(...)`).
const BrowserLiveRoute = (Route as unknown as { component: React.ComponentType }).component

beforeEach(() => {
  vi.clearAllMocks()
  mockSearch = { session: 's1', agent: 'a1' }
  // The real onClose handler calls window.close() — jsdom's real
  // implementation actually tears down the window (poisoning `document` for
  // every subsequent test in this file), so it's stubbed the same way a real
  // browser-opened, `window.open`'d pop-out's close would just... close,
  // with no further JS observable from this side either way.
  vi.spyOn(window, 'close').mockImplementation(() => {})
})

describe('BrowserLiveRoute — BUG 1: fillContainer wiring', () => {
  it('passes fillContainer to BrowserLiveView so the pop-out fills the window', () => {
    render(<BrowserLiveRoute />)
    expect(mockBrowserLiveViewProps).toHaveBeenCalledWith(
      expect.objectContaining({ sessionId: 's1', agentId: 'a1', fillContainer: true }),
    )
  })

  it('never passes canAnnotate (annotate stays unsupported in the pop-out, FE-4)', () => {
    render(<BrowserLiveRoute />)
    const calledProps = mockBrowserLiveViewProps.mock.calls[0]?.[0] as Record<string, unknown>
    expect('canAnnotate' in calledProps).toBe(false)
  })
})

describe('BrowserLiveRoute — BUG 2: announce pop-out-closed hand-off', () => {
  it('announces popout-closed for the exact (session, agent) when the in-app Close button is clicked', () => {
    render(<BrowserLiveRoute />)
    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(mockAnnouncePopoutClosed).toHaveBeenCalledWith('s1', 'a1')
  })

  it('also announces on a native tab-close (pagehide) even if the in-app Close button was never clicked', () => {
    const { unmount } = render(<BrowserLiveRoute />)
    expect(mockAnnouncePopoutClosed).not.toHaveBeenCalled()

    // Simulates the browser firing `pagehide` on a native tab-close/Cmd+W —
    // no click, no onClose call, nothing but the window teardown event.
    fireEvent(window, new Event('pagehide'))
    expect(mockAnnouncePopoutClosed).toHaveBeenCalledWith('s1', 'a1')

    unmount()
  })

  it('removes the pagehide listener on unmount (no stale announce after the route itself unmounts cleanly)', () => {
    const { unmount } = render(<BrowserLiveRoute />)
    unmount()
    mockAnnouncePopoutClosed.mockClear()
    fireEvent(window, new Event('pagehide'))
    expect(mockAnnouncePopoutClosed).not.toHaveBeenCalled()
  })

  it('does not announce when session/agent search params are missing (nothing meaningful to hand off)', () => {
    mockSearch = {}
    render(<BrowserLiveRoute />)
    fireEvent(window, new Event('pagehide'))
    expect(mockAnnouncePopoutClosed).not.toHaveBeenCalled()
  })
})
