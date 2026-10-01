import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import { announcePanelPopoutClosed } from '@/lib/panelPopoutLifecycle'
import { useUiStore } from '@/store/ui'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'
import { SidePanelShell } from './SidePanelShell'
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
  fullScreen: {
    toSearch: ({ workspaceId }): Record<string, string> => workspaceId ? { workspace: workspaceId } : {},
    fromSearch: () => ({ workspaceId: 'ws-a' }),
  },
}

beforeEach(() => {
  useUiStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useUiStore.getState().closePanel()
})

it('re-docking an announced pop-out returns focus to the original tab chat input', async () => {
  const child = {
    closed: false,
    opener: null,
    close: vi.fn(),
    focus: vi.fn(),
    location: { replace: vi.fn() },
  }
  vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
  render(
    <>
      <Button data-testid="other-focus">Other focus</Button>
      <PanelTabPresenceBridge />
      <SidePanelShell panels={[library]} username="dana" chat={<textarea data-testid="chat-input" />} />
    </>,
  )

  act(() => useUiStore.getState().openPanel('library', { workspaceId: 'ws-a' }))
  fireEvent.click(screen.getByRole('button', { name: 'Expand Library panel' }))
  await waitFor(() => expect(useUiStore.getState().activePanel).toBeNull())
  const fullScreenUrl = child.location.replace.mock.calls[0]?.[0] as string
  const popoutId = new URLSearchParams(fullScreenUrl.split('?')[1]).get('popout')
  expect(popoutId).not.toBeNull()

  const other = screen.getByTestId('other-focus')
  other.focus()
  expect(document.activeElement).toBe(other)
  act(() => announcePanelPopoutClosed('library', popoutId!, {
    workspaceId: 'ws-a', path: 'Notes/Current.md',
  }))
  await waitFor(() => expect(useUiStore.getState().activePanel).toEqual({
    id: 'library', context: { workspaceId: 'ws-a', path: 'Notes/Current.md' },
  }))
  await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('chat-input')))
})
