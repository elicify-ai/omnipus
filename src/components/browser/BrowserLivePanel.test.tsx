// BrowserLivePanel.test.tsx — ADR-039 UAT-fix delta coverage (reviewer
// finding F6): this delta's real fixes (non-modal Sheet so chat stays
// clickable, auto-close + strengthened hand-to-agent hint, canAnnotate
// opt-in from the docked panel) had ZERO tests before this file.
//
// BrowserLiveView itself is mocked — its own behaviour (WS lifecycle,
// control toggle, annotate mode, etc.) is already covered by
// BrowserLiveView.*.test.tsx. This file exercises ONLY what BrowserLivePanel
// itself is responsible for: how it wires the Sheet and what it passes down.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { useUiStore } from '@/store/ui'

const mockBrowserLiveViewProps = vi.fn()

vi.mock('./BrowserLiveView', () => ({
  BrowserLiveView: (props: {
    sessionId: string
    agentId: string
    onClose?: () => void
    onPopOut?: () => void
    onHandToAgent?: () => void
    canAnnotate?: boolean
  }) => {
    mockBrowserLiveViewProps(props)
    return (
      <div data-testid="mock-browser-live-view">
        <span data-testid="mock-can-annotate">{String(props.canAnnotate)}</span>
        {props.onHandToAgent && (
          <button type="button" onClick={props.onHandToAgent}>
            mock-hand-to-agent
          </button>
        )}
        {props.onClose && (
          <button type="button" onClick={props.onClose}>
            mock-close
          </button>
        )}
        {props.onPopOut && (
          <button type="button" onClick={props.onPopOut}>
            mock-pop-out
          </button>
        )}
      </div>
    )
  },
}))

import { BrowserLivePanel } from './BrowserLivePanel'

beforeEach(() => {
  mockBrowserLiveViewProps.mockClear()
  useUiStore.setState({ browserPanel: null, composerPrefill: null, toasts: [] })
})

describe('BrowserLivePanel', () => {
  it('renders nothing when browserPanel is closed (null)', () => {
    render(<BrowserLivePanel />)
    expect(screen.queryByTestId('mock-browser-live-view')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders BrowserLiveView keyed to the open (session, agent) pair, with canAnnotate=true', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    expect(screen.getByTestId('mock-browser-live-view')).toBeInTheDocument()
    expect(screen.getByTestId('mock-can-annotate')).toHaveTextContent('true')
    expect(mockBrowserLiveViewProps).toHaveBeenCalledWith(
      expect.objectContaining({ sessionId: 'sess-1', agentId: 'agent-1', canAnnotate: true }),
    )
  })

  // UAT finding FE-3(b): Radix's default modal Dialog sets
  // `body{pointer-events:none}` while open, which made the chat pane behind
  // this panel LOOK usable but be 100% unclickable. The Sheet is rendered
  // with modal={false} (SheetContent gets aria-modal={false} to match) so a
  // screen reader / a11y tooling doesn't report this as blocking modal
  // content.
  it('renders the dialog as non-modal (aria-modal="false")', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    expect(screen.getByRole('dialog')).toHaveAttribute('aria-modal', 'false')
  })

  // Radix's non-modal Dialog.Content still dismisses on ANY outside
  // pointerdown/focus by default (see BrowserLivePanel.tsx's doc comment) —
  // without onInteractOutside's preventDefault, the very first click on the
  // chat composer behind the panel would immediately close it. Mirrors the
  // established outside-interaction test pattern in alert-dialog.test.tsx.
  it('does not close on an outside pointer interaction (onInteractOutside is prevented)', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    fireEvent.pointerDown(document.body)
    fireEvent.pointerUp(document.body)
    fireEvent.click(document.body)

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess-1', agentId: 'agent-1' })
  })

  describe('onHandToAgent (UAT findings FE-2 + FE-3(a))', () => {
    it('sets the strengthened composer-prefill hint, toasts, and auto-closes the panel', () => {
      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      fireEvent.click(screen.getByRole('button', { name: 'mock-hand-to-agent' }))

      // FE-2: the hint must spell out WHICH tools to reach for first (a weak
      // "Continue from the current page: " previously made the agent claim
      // it had no browser open at all).
      expect(useUiStore.getState().composerPrefill).toBe(
        "I've left a page open in your live browser session. Use your browser tools (take a screenshot and/or read the page text) to see what's currently loaded, then continue from there: ",
      )
      expect(useUiStore.getState().toasts.some((t) => /hint was added to the chat composer/i.test(t.message))).toBe(true)
      // FE-3(a): auto-close so focus lands back on the now-usable composer.
      expect(useUiStore.getState().browserPanel).toBeNull()
    })
  })

  describe('onPopOut', () => {
    it('mirrors the sessionStorage auth token into localStorage and opens the hash-routed pop-out URL', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
      sessionStorage.setItem('omnipus_auth_token', 'tok-123')
      localStorage.removeItem('omnipus_auth_token')

      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))

      expect(localStorage.getItem('omnipus_auth_token')).toBe('tok-123')
      expect(openSpy).toHaveBeenCalledWith(
        expect.stringMatching(/^\/#\/browser-live\?session=sess-1&agent=agent-1$/),
        '_blank',
        'noopener,noreferrer',
      )

      openSpy.mockRestore()
      sessionStorage.clear()
      localStorage.clear()
    })
  })

  it('closes via the panel close callback (onClose -> closeBrowserPanel)', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(useUiStore.getState().browserPanel).toBeNull()
  })
})
