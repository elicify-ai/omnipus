// BrowserLivePanel.test.tsx — always-docked layout coverage (operator
// direction 2026-07-16, amends ADR-040 D4: the unpinned Sheet overlay and
// the pin toggle are retired; open = docked <aside>, fullscreen = pop-out).
//
// BrowserLiveView itself is mocked — its own behaviour (WS lifecycle,
// control toggle, annotate mode, etc.) is already covered by
// BrowserLiveView.*.test.tsx. This file exercises ONLY what BrowserLivePanel
// itself is responsible for: the docked <aside> and what it passes down.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act, useEffect } from 'react'
import { useUiStore } from '@/store/ui'

const mockBrowserLiveViewProps = vi.fn()
const lifecycle: string[] = []

vi.mock('./BrowserLiveView', () => ({
  BrowserLiveView: (props: {
    sessionId: string
    agentId: string
    onClose?: () => void
    onPopOut?: () => void
    canAnnotate?: boolean
  }) => {
    useEffect(() => { lifecycle.push('mounted'); return () => { lifecycle.push('detached') } }, [])
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
  lifecycle.length = 0
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

})

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/') })

function popup() {
  const child = { closed: false, opener: {}, location: { replace: vi.fn() }, focus: vi.fn(), close: vi.fn() }
  child.close.mockImplementation(() => { child.closed = true })
  vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
  return child
}
function openDock() {
  const mounted = render(<BrowserLivePanel />)
  act(() => useUiStore.getState().openBrowserPanel('s1', 'a1'))
  return mounted
}

describe('exclusive popout ownership', () => {
  it('ignores unowned broadcasts, including inside the popout document', async () => {
    window.history.replaceState({}, '', '/#/browser-live?session=other&agent=other')
    render(<BrowserLivePanel />)
    const channel = new BroadcastChannel('omnipus-browser-live-handoff')
    act(() => channel.postMessage({ type: 'popout-closed', sessionId: 'other-session', agentId: 'other-agent' }))
    await new Promise(resolve => setTimeout(resolve, 300))
    expect(useUiStore.getState().browserPanel).toBeNull()
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
    channel.close()
  })
  it('preserves the dock when the browser blocks a popout', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    openDock()
    fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 's1', agentId: 'a1' })
    expect(lifecycle).toEqual(['mounted'])
  })
  it('detaches the dock and severs the opener before navigating the trusted blank tab', () => {
    const child = popup(); let observed: unknown
    child.location.replace.mockImplementation(() => { observed = { lifecycle: [...lifecycle], opener: child.opener } })
    openDock()
    fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    expect(window.open).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    expect(observed).toEqual({ lifecycle: ['mounted', 'detached'], opener: null })
    expect(child.location.replace).toHaveBeenCalledExactlyOnceWith('/#/browser-live?session=s1&agent=a1')
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
    expect(localStorage.length).toBe(0); expect(sessionStorage.length).toBe(0)
  })
  it('keeps the dock unmounted through reload and Open browser, then restores its exact owner once after native close', () => {
    vi.useFakeTimers(); const child = popup(); openDock()
    fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    act(() => { vi.advanceTimersByTime(2000) }) // Reload retains the same live WindowProxy.
    expect(useUiStore.getState().browserPanel).toBeNull()
    act(() => useUiStore.getState().openBrowserPanel('different', 'different'))
    expect(lifecycle).toEqual(['mounted', 'detached'])
    expect(useUiStore.getState().browserPanel).toBeNull()
    expect(child.focus).toHaveBeenCalledTimes(1)
    child.closed = true
    act(() => { vi.advanceTimersByTime(250) })
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 's1', agentId: 'a1' })
    expect(lifecycle).toEqual(['mounted', 'detached', 'mounted'])
    act(() => useUiStore.getState().closeBrowserPanel())
    act(() => { vi.advanceTimersByTime(2000) })
    expect(useUiStore.getState().browserPanel).toBeNull()
  })
  it('does not overwrite a newer dock request after the owned child already closed', () => {
    vi.useFakeTimers(); const child = popup(); openDock()
    fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    child.closed = true
    act(() => useUiStore.getState().openBrowserPanel('new', 'new-agent'))
    act(() => { vi.advanceTimersByTime(1000) })
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'new', agentId: 'new-agent' })
  })
  it('closes an unnavigable blank tab and restores the dock', () => {
    const child = popup(); child.location.replace.mockImplementation(() => { throw new Error('navigation denied') })
    openDock(); fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    expect(child.close).toHaveBeenCalledTimes(1)
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 's1', agentId: 'a1' })
    expect(screen.getAllByTestId('mock-browser-live-view')).toHaveLength(1)
  })
  it('ends ownership on parent unmount without a delayed restoration', () => {
    vi.useFakeTimers(); const child = popup(); const mounted = openDock()
    fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    mounted.unmount(); act(() => { vi.advanceTimersByTime(2000) })
    expect(child.close).toHaveBeenCalledTimes(1)
    expect(useUiStore.getState().browserPanel).toBeNull()
  })
  it('restores composer focus after successful handover', () => {
    popup(); const composer = document.createElement('textarea'); composer.dataset.testid = 'chat-input'; document.body.appendChild(composer)
    openDock(); fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
    expect(document.activeElement).toBe(composer)
    composer.remove()
  })
  it('never mounts the global dock inside the fullscreen popout route', () => {
    window.history.replaceState({}, '', '/#/browser-live?session=s1&agent=a1')
    openDock()
    expect(lifecycle).toEqual([])
    expect(screen.queryByTestId('browser-live-panel-docked')).not.toBeInTheDocument()
  })
})

it('closes the dock through the existing close callback', () => {
  openDock(); fireEvent.click(screen.getByRole('button', { name: 'mock-close' }))
  expect(useUiStore.getState().browserPanel).toBeNull()
  expect(lifecycle).toEqual(['mounted', 'detached'])
})
it('remounts the dock for an explicitly changed target', () => {
  openDock(); act(() => useUiStore.getState().openBrowserPanel('s2', 'a2'))
  expect(mockBrowserLiveViewProps.mock.calls.at(-1)?.[0]).toMatchObject({ sessionId: 's2', agentId: 'a2' })
  expect(lifecycle).toEqual(['mounted', 'detached', 'mounted'])
  expect(screen.getAllByTestId('browser-live-panel-docked')).toHaveLength(1)
})

it('keeps the dock and explains a thrown popup-creation failure', () => {
  vi.spyOn(window, 'open').mockImplementation(() => { throw new Error('popup creation denied') })
  openDock()
  expect(() => act(() => mockBrowserLiveViewProps.mock.calls.at(-1)?.[0].onPopOut())).not.toThrow()
  expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 's1', agentId: 'a1' })
  expect(useUiStore.getState().toasts.at(-1)?.message).toBe('The popout could not open. The browser remains here.')
  expect(lifecycle).toEqual(['mounted'])
})
it('closes the owned popout on parent pagehide without restoring a hidden dock', () => {
  vi.useFakeTimers(); const child = popup(); openDock()
  fireEvent.click(screen.getByRole('button', { name: 'mock-pop-out' }))
  fireEvent(window, new Event('pagehide'))
  act(() => { vi.advanceTimersByTime(1000) })
  expect(child.close).toHaveBeenCalledTimes(1)
  expect(useUiStore.getState().browserPanel).toBeNull()
})
