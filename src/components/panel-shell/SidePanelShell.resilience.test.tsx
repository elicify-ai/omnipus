import { lazy } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelDefinition } from './types'
import { SidePanelShell } from './SidePanelShell'
import { usePanelShellStore } from './panelShellStore'

class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    this.callback(
      [{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', RowResizeObserver)

function Probe() {
  return <div>Panel content</div>
}

const library: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: Probe,
  fullScreen: { toSearch: () => ({}), fromSearch: () => ({}) },
}

function renderShell(definition: PanelDefinition = library) {
  return render(
    <SidePanelShell
      panels={[definition]}
      username="dana"
      chat={<textarea data-testid="chat-input" defaultValue="chat" />}
    />,
  )
}

beforeEach(() => {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: null,
    guardPending: false,
    historyPushed: false,
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  usePanelShellStore.getState().closePanel()
})

describe('SidePanelShell resilience and focus', () => {
  it('does not move focus into the header for a restored/store-driven open', async () => {
    renderShell()
    const input = screen.getByTestId('chat-input')
    input.focus()

    act(() => usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' }))
    await waitFor(() => expect(screen.getByTestId('side-panel-header')).toBeInTheDocument())

    expect(document.activeElement).toBe(input)
  })

  it('falls back to the chat input when a close has no tracked trigger origin', async () => {
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    renderShell()

    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))

    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('chat-input')))
  })

  it('shows a visible loading state while lazy panel content is pending', () => {
    const Pending = lazy(() => new Promise<{ default: typeof Probe }>(() => {}))
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    renderShell({ ...library, content: Pending })

    expect(screen.getByText('Loading Library…')).toBeInTheDocument()
  })

  it('contains a panel render failure and retries only that panel content', async () => {
    let shouldThrow = true
    function FlakyPanel() {
      if (shouldThrow) throw new Error('panel render failed')
      return <div>Recovered panel</div>
    }
    vi.spyOn(console, 'error').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })

    expect(() => renderShell({ ...library, content: FlakyPanel })).not.toThrow()
    expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    expect(screen.getByTestId('chat-input')).toBeInTheDocument()

    shouldThrow = false
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('Recovered panel')).toBeInTheDocument()
  })

  it('shows a generic failure instead of calling an exception a blocked popup', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(window, 'open').mockImplementation(() => {
      throw new Error('window subsystem failed')
    })
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    renderShell()

    fireEvent.click(screen.getByRole('button', { name: 'Expand Library panel' }))

    expect(await screen.findByTestId('panel-expand-error')).toHaveTextContent(
      'Could not expand this panel. Try again.',
    )
    expect(usePanelShellStore.getState().activePanel?.id).toBe('library')
  })
})
