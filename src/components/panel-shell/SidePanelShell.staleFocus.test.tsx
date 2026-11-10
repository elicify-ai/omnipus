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

it('a stale trigger retry from an UNMOUNTED shell must not steal the new shell\'s focus (US-9)', async () => {
  // The retry chain above is retired by a SECOND close on the SAME shell
  // instance (finishClose bumps the per-instance generation). This test
  // covers the hole that instance's guard cannot see: the shell UNMOUNTS
  // with a retry chain still pending. The generation ref dies with the
  // instance, so nothing retires the chain; its frames are delivered late
  // (under CPU load: seconds late), after a NEW shell has mounted, and the
  // module-level trigger origin now names the NEW shell's trigger. The
  // delayed tick must not yank focus out of the new shell's chat input.
  const scheduled: FrameRequestCallback[] = []
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    scheduled.push(callback)
    return scheduled.length
  })

  // Instance 1: a restore-style open (no trigger origin), an Escape close —
  // which arms the trigger-return retry chain because no origin exists —
  // and then the shell unmounts with the chain still pending.
  const first = render(
    <>
      <Button data-panel-trigger="library">Library trigger</Button>
      <SidePanelShell panels={[library]} username="dana" chat={<textarea data-testid="chat-input-1" />} />
    </>,
  )
  act(() => usePanelShellStore.getState().openPanel('library'))
  fireEvent.keyDown(screen.getByTestId('side-panel'), { key: 'Escape' })
  await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
  expect(scheduled.length, 'the Escape close must have armed a pending retry frame').toBeGreaterThan(0)
  first.unmount()

  // Instance 2: a fresh mount — its own generation counter starts at zero,
  // so the orphaned chain above is invisible to every later close. Focus the
  // new trigger and open through the store so the trigger origin points at
  // THIS instance's trigger, then close through the header (chat return).
  // The textarea keeps the production `chat-input` testid — that literal
  // selector is what focusChatInput() queries.
  render(
    <>
      <Button data-panel-trigger="library" data-testid="trigger-2">Library trigger</Button>
      <SidePanelShell panels={[library]} username="dana" chat={<textarea data-testid="chat-input" />} />
    </>,
  )
  const trigger2 = screen.getByTestId('trigger-2')
  act(() => trigger2.focus())
  act(() => usePanelShellStore.getState().openPanel('library'))
  fireEvent.click(screen.getByRole('button', { name: 'Close Library' }))
  await waitFor(() => expect(usePanelShellStore.getState().activePanel).toBeNull())
  await waitFor(() => expect(screen.getByTestId('chat-input')).toHaveFocus())

  // The unmounted shell's pending frames land now — against the NEW shell.
  act(() => {
    for (const callback of scheduled) callback(performance.now())
  })
  expect(document.activeElement).toBe(screen.getByTestId('chat-input'))
  expect(document.activeElement).not.toBe(trigger2)
})
