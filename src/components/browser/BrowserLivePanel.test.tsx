// BrowserLivePanel.test.tsx — ADR-039 UAT-fix delta coverage (reviewer
// finding F6) PLUS ADR-040 D4 Pin/side-by-side coverage.
//
// BrowserLiveView itself is mocked — its own behaviour (WS lifecycle,
// control toggle, annotate mode, etc.) is already covered by
// BrowserLiveView.*.test.tsx. This file exercises ONLY what BrowserLivePanel
// itself is responsible for: how it wires the Sheet (unpinned) / docked
// <aside> (pinned) and what it passes down.

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
    canAnnotate?: boolean
    isPinned?: boolean
    onTogglePin?: () => void
  }) => {
    mockBrowserLiveViewProps(props)
    return (
      <div data-testid="mock-browser-live-view">
        <span data-testid="mock-can-annotate">{String(props.canAnnotate)}</span>
        <span data-testid="mock-is-pinned">{String(props.isPinned)}</span>
        {props.onTogglePin && (
          <button type="button" onClick={props.onTogglePin}>
            mock-toggle-pin
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
  useUiStore.setState({ browserPanel: null, browserPanelPinned: false, toasts: [] })
})

describe('BrowserLivePanel', () => {
  it('renders nothing when browserPanel is closed (null), unpinned', () => {
    render(<BrowserLivePanel />)
    expect(screen.queryByTestId('mock-browser-live-view')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
  })

  // ADR-040 D4: a pinned PREFERENCE with no open session must still render
  // nothing — pin only takes effect once a panel is actually open.
  it('renders nothing when browserPanel is closed (null), even if browserPanelPinned is true', () => {
    useUiStore.setState({ browserPanelPinned: true })
    render(<BrowserLivePanel />)
    expect(screen.queryByTestId('mock-browser-live-view')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
  })

  describe('unpinned (default) — overlay Sheet', () => {
    it('renders BrowserLiveView keyed to the open (session, agent) pair, with canAnnotate=true, isPinned=false', () => {
      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
      expect(screen.getByTestId('mock-browser-live-view')).toBeInTheDocument()
      expect(screen.getByTestId('mock-can-annotate')).toHaveTextContent('true')
      expect(screen.getByTestId('mock-is-pinned')).toHaveTextContent('false')
      expect(mockBrowserLiveViewProps).toHaveBeenCalledWith(
        expect.objectContaining({ sessionId: 'sess-1', agentId: 'agent-1', canAnnotate: true, isPinned: false }),
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
  })

  describe('pinned — docked layout (ADR-040 D4)', () => {
    it('renders BrowserLiveView inside a docked <aside>, not a Sheet dialog, with isPinned=true', () => {
      useUiStore.setState({ browserPanelPinned: true })
      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      const docked = screen.getByTestId('browser-live-panel-docked')
      expect(docked.tagName).toBe('ASIDE')
      expect(screen.getByTestId('mock-browser-live-view')).toBeInTheDocument()
      expect(screen.getByTestId('mock-is-pinned')).toHaveTextContent('true')
      expect(mockBrowserLiveViewProps).toHaveBeenCalledWith(
        expect.objectContaining({ sessionId: 'sess-1', agentId: 'agent-1', canAnnotate: true, isPinned: true }),
      )
    })

    it('passes onTogglePin, and clicking it flips browserPanelPinned in the store', () => {
      useUiStore.setState({ browserPanelPinned: true })
      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      fireEvent.click(screen.getByRole('button', { name: 'mock-toggle-pin' }))
      expect(useUiStore.getState().browserPanelPinned).toBe(false)
    })

    // Toggling pin OFF while a session is open switches this component from
    // the docked <aside> back to the Sheet — a real remount of the (mocked)
    // BrowserLiveView is an accepted ADR-040 D4 tradeoff (see
    // BrowserLivePanel.tsx's module doc comment), but the (session, agent)
    // pair and the panel's open-ness must both survive the flip.
    it('toggling pin off (while open) switches to the Sheet, keeping the same (session, agent) open', () => {
      useUiStore.setState({ browserPanelPinned: true })
      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })
      expect(screen.getByTestId('browser-live-panel-docked')).toBeInTheDocument()

      act(() => {
        useUiStore.getState().toggleBrowserPanelPinned()
      })

      expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess-1', agentId: 'agent-1' })
    })
  })

  // ADR-040 D1/D2: the three-verb "Take control / Release control / Hand to
  // agent" cluster is retired — `onHandToAgent` must no longer exist on the
  // props passed to BrowserLiveView (it's removed from the prop interface
  // entirely, not merely left unset).
  it('no longer passes onHandToAgent to BrowserLiveView (ADR-040 D1 removal)', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    const calledProps = mockBrowserLiveViewProps.mock.calls[0]?.[0] as Record<string, unknown>
    expect(calledProps).toBeDefined()
    expect('onHandToAgent' in calledProps).toBe(false)
  })

  describe('onPopOut', () => {
    it('mirrors the sessionStorage auth token into localStorage and opens the hash-routed pop-out URL (unpinned)', () => {
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

    it('does NOT wire onPopOut when pinned (docked layout) — Pop-out makes no sense from an already-docked panel', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
      useUiStore.setState({ browserPanelPinned: true })

      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      // BrowserLiveView gates its own Pop-out affordance purely on
      // `onPopOut`'s presence — omitting the prop while pinned hides it.
      expect(screen.queryByRole('button', { name: 'mock-pop-out' })).not.toBeInTheDocument()
      const calledProps = mockBrowserLiveViewProps.mock.calls[0]?.[0] as Record<string, unknown>
      expect(calledProps).toBeDefined()
      expect('onPopOut' in calledProps).toBe(false)
      expect(openSpy).not.toHaveBeenCalled()

      openSpy.mockRestore()
      localStorage.clear()
    })
  })

  it('closes via the panel close callback (onClose -> closeBrowserPanel), unpinned', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(useUiStore.getState().browserPanel).toBeNull()
  })

  it('closes via the panel close callback (onClose -> closeBrowserPanel), pinned', () => {
    useUiStore.setState({ browserPanelPinned: true })
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(useUiStore.getState().browserPanel).toBeNull()
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
  })

  // UAT finding (leaked/duplicate close button): a live tester's DOM dump
  // found TWO visible, clickable close controls stacked in the panel's
  // top-right — the intended custom `aria-label="Close live browser panel"`
  // button (rendered by BrowserLiveView's own header, wired to `onClose` ->
  // this file's `mock-close` stand-in) AND Radix SheetContent's own
  // unconditional built-in close (unlabeled, accessible name "Close" from
  // its sr-only span). Only the Sheet (unpinned) branch renders a
  // SheetContent at all — the pinned docked <aside> never had this bug.
  it('does not render SheetContent\'s own built-in Radix close button (only the custom Close remains), unpinned', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    // The custom close (this file's mock stand-in for BrowserLiveView's own
    // `aria-label="Close live browser panel"` button) is present...
    expect(screen.getByRole('button', { name: 'mock-close' })).toBeInTheDocument()
    // ...but Radix's own default close (accessible name "Close", from
    // SheetContent's unconditional `<DialogPrimitive.Close>` + sr-only span)
    // must be suppressed via `showClose={false}` — not a second, redundant
    // close control stacked on top of the custom one.
    expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument()
  })
})
