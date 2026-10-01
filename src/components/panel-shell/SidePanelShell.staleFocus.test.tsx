import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import { SidePanelShell } from './SidePanelShell'
import { usePanelShellStore } from './panelShellStore'
import type { PanelDefinition } from './types'

class RowResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', RowResizeObserver)

const library: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: () => <div>Panel content</div>,
  fullScreen: { toSearch: () => ({}), fromSearch: () => ({}) },
}

beforeEach(() => {
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false })
})

it('keeps header Close focus on chat when an older Escape retry later runs (US-9)', async () => {
  render(
    <>
      <Button data-panel-trigger="library">Library trigger</Button>
      <SidePanelShell panels={[library]} username="dana" chat={<textarea data-testid="chat-input" />} />
    </>,
  )
  const scheduled: FrameRequestCallback[] = []
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    scheduled.push(callback)
    return scheduled.length
  })

  act(() => usePanelShellStore.getState().openPanel('library'))
  fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
  await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
  expect(scheduled.length).toBeGreaterThan(0)

  const trigger = screen.getByText('Library trigger')
  act(() => trigger.focus())
  act(() => usePanelShellStore.getState().openPanel('library'))
  fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
  await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
  expect(document.activeElement).toBe(screen.getByTestId('chat-input'))

  act(() => {
    for (const callback of scheduled) callback(performance.now())
  })
  expect(document.activeElement).toBe(screen.getByTestId('chat-input'))
  expect(document.activeElement).not.toBe(trigger)
})
