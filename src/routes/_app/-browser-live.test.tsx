// Popout route layout and close behavior; ownership belongs to the originating app.
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

const { mockBrowserLiveViewProps } = vi.hoisted(() => ({
  mockBrowserLiveViewProps: vi.fn(),
}))

const { mockAnnouncePanelTabPresence, mockStopPanelTabPresence } = vi.hoisted(() => ({
  mockAnnouncePanelTabPresence: vi.fn(),
  mockStopPanelTabPresence: vi.fn(),
}))

vi.mock('@/lib/panelTabPresence', () => ({
  announcePanelTabPresence: mockAnnouncePanelTabPresence,
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
  mockAnnouncePanelTabPresence.mockReturnValue({ update: vi.fn(), stop: mockStopPanelTabPresence })
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

describe('BrowserLiveRoute — owner-controlled handover', () => {
  it('closes its own window without broadcasting a premature restore', () => {
    render(<BrowserLiveRoute />)
    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(window.close).toHaveBeenCalledExactlyOnceWith()
    expect(mockNavigate).toHaveBeenCalledExactlyOnceWith({ to: '/' })
    expect(mockAnnouncePanelTabPresence).toHaveBeenCalledWith({
      panelId: 'browser',
      sessionId: 's1',
      agentId: 'a1',
    })
  })
  it('ends presence on route unmount without triggering owner restoration', () => {
    const mounted = render(<BrowserLiveRoute />)
    fireEvent(window, new Event('pagehide'))
    expect(window.close).not.toHaveBeenCalled()
    mounted.unmount()
    expect(mockStopPanelTabPresence).toHaveBeenCalledOnce()
    expect(mockNavigate).not.toHaveBeenCalled()
  })
  it('does not mount a viewer without session and agent', () => {
    mockSearch = {}
    render(<BrowserLiveRoute />)
    expect(screen.queryByTestId('mock-browser-live-view')).not.toBeInTheDocument()
    expect(screen.getByText(/Missing session or agent/)).toBeInTheDocument()
  })
})
