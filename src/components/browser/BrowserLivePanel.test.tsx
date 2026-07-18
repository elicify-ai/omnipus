// BrowserLivePanel.test.tsx — always-docked layout coverage (operator
// direction 2026-07-16, amends ADR-040 D4: the unpinned Sheet overlay and
// the pin toggle are retired; open = docked <aside>, fullscreen = pop-out).
//
// BrowserLiveView itself is mocked — its own behaviour (WS lifecycle,
// control toggle, annotate mode, etc.) is already covered by
// BrowserLiveView.*.test.tsx. This file exercises ONLY what BrowserLivePanel
// itself is responsible for: the docked <aside> and what it passes down.

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
  }) => {
    mockBrowserLiveViewProps(props)
    return (
      <div data-testid="mock-browser-live-view">
        <span data-testid="mock-can-annotate">{String(props.canAnnotate)}</span>
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

import { BrowserLivePanel } from './BrowserLivePanel'

beforeEach(() => {
  mockBrowserLiveViewProps.mockClear()
  useUiStore.setState({ browserPanel: null, toasts: [] })
})

describe('BrowserLivePanel (always-docked)', () => {
  it('renders nothing when browserPanel is closed (null)', () => {
    render(<BrowserLivePanel />)
    expect(screen.queryByTestId('mock-browser-live-view')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
  })

  it('renders BrowserLiveView inside a docked <aside> when opened — never a Sheet dialog', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    const docked = screen.getByTestId('browser-live-panel-docked')
    expect(docked.tagName).toBe('ASIDE')
    // The retired overlay mode must stay retired: no dialog role anywhere.
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('mock-browser-live-view')).toBeInTheDocument()
    expect(screen.getByTestId('mock-can-annotate')).toHaveTextContent('true')
    expect(mockBrowserLiveViewProps).toHaveBeenCalledWith(
      expect.objectContaining({ sessionId: 'sess-1', agentId: 'agent-1', canAnnotate: true }),
    )
  })

  it('never passes the retired pin props (isPinned / onTogglePin) to BrowserLiveView', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    const calledProps = mockBrowserLiveViewProps.mock.calls[0]?.[0] as Record<string, unknown>
    expect(calledProps).toBeDefined()
    expect('isPinned' in calledProps).toBe(false)
    expect('onTogglePin' in calledProps).toBe(false)
  })

  // ADR-040 D1/D2: the three-verb "Take control / Release control / Hand to
  // agent" cluster stays retired — `onHandToAgent` must not reappear.
  it('no longer passes onHandToAgent to BrowserLiveView (ADR-040 D1 removal)', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    const calledProps = mockBrowserLiveViewProps.mock.calls[0]?.[0] as Record<string, unknown>
    expect(calledProps).toBeDefined()
    expect('onHandToAgent' in calledProps).toBe(false)
  })

  describe('onPopOut (the fullscreen escape)', () => {
    it('is always wired, mirrors the sessionStorage auth token into localStorage, and opens the hash-routed pop-out URL', () => {
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
      // UAT 2026-07-18: popping out HANDS OVER the view — the docked panel
      // closes (unmount detaches its viewer; the server releases the drive
      // lock on detach), so the pop-out is never wedged behind this
      // connection's control lock ("Someone else is driving", dead input).
      expect(useUiStore.getState().browserPanel).toBeNull()
      expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()

      openSpy.mockRestore()
      sessionStorage.clear()
      localStorage.clear()
    })

    // a11y fix-wave (F1): closeBrowserPanel() unmounts the docked panel,
    // which drops keyboard focus to <body> — this asserts the pop-out path
    // restores it to the chat composer instead of stranding keyboard/SR
    // users. Simulates the composer textarea via the SAME
    // `data-testid="chat-input"` ChatScreen.tsx's real composer carries
    // (BrowserLivePanel has no direct handle on ChatScreen's internal ref,
    // so it queries by this testid — see BrowserLivePanel.tsx's own comment).
    it('restores focus to the chat composer textarea after popping out', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
      const composerInput = document.createElement('textarea')
      composerInput.setAttribute('data-testid', 'chat-input')
      document.body.appendChild(composerInput)

      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))

      expect(document.activeElement).toBe(composerInput)

      openSpy.mockRestore()
      document.body.removeChild(composerInput)
    })

    it('does not throw when popping out and the composer textarea is not mounted (graceful no-op fallback)', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
      expect(document.querySelector('[data-testid="chat-input"]')).toBeNull()

      render(<BrowserLivePanel />)
      act(() => {
        useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
      })

      expect(() => {
        fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
      }).not.toThrow()

      openSpy.mockRestore()
    })
  })

  it('closes via the panel close callback (onClose -> closeBrowserPanel)', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })

    fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
    expect(useUiStore.getState().browserPanel).toBeNull()
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
  })

  it('remounts BrowserLiveView (fresh key) when a second open targets a different (session, agent) pair', () => {
    render(<BrowserLivePanel />)
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    })
    act(() => {
      useUiStore.getState().openBrowserPanel('sess-2', 'agent-2')
    })

    const lastProps = mockBrowserLiveViewProps.mock.calls.at(-1)?.[0] as Record<string, unknown>
    expect(lastProps).toMatchObject({ sessionId: 'sess-2', agentId: 'agent-2' })
    // Still exactly one docked panel — the key remount swaps content, not layout.
    expect(screen.getAllByTestId('browser-live-panel-docked')).toHaveLength(1)
  })
})
