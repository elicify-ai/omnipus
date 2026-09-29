import { useEffect } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelContentProps, PanelDefinition } from './types'
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

function Probe(props: PanelContentProps) {
  useEffect(() => {
    props.registerExpand(() => true)
    return () => props.registerExpand(null)
  }, [props.registerExpand])
  return <div>Panel content</div>
}

const library: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: Probe,
  expandTarget: () => '/library',
}

function renderShell() {
  render(
    <>
      <button
        data-panel-trigger="library"
        onClick={() => usePanelShellStore.getState().openPanel('library')}
      >
        Library trigger
      </button>
      <SidePanelShell
        panels={[library]}
        username="dana"
        chat={<textarea data-testid="chat-input" defaultValue="chat" />}
      />
    </>,
  )
}

beforeEach(() => {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: -1,
    guardPending: false,
    historyPushed: false,
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  usePanelShellStore.getState().closePanel()
})

describe('US-9 focus return reasons', () => {
  it('returns header Close to the chat input, not the invoking toggle', async () => {
    renderShell()
    fireEvent.click(screen.getByText('Library trigger'))
    fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))

    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
    expect(document.activeElement).toBe(screen.getByTestId('chat-input'))
  })

  it('returns header Expand to the chat input', async () => {
    renderShell()
    fireEvent.click(screen.getByText('Library trigger'))
    fireEvent.click(screen.getByRole('button', { name: 'Expand Library panel' }))

    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
    expect(document.activeElement).toBe(screen.getByTestId('chat-input'))
  })

  it('keeps Escape tied to the invoking toggle', async () => {
    renderShell()
    const trigger = screen.getByText('Library trigger')
    fireEvent.click(trigger)
    fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })

    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(trigger))
  })
})
