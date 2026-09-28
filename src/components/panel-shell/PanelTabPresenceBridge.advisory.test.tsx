import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import {
  announcePanelTabPresence,
  getPanelTabPresence,
  registerPanelTabHandle,
  forgetPanelTabHandle,
} from '@/lib/panelTabPresence'
import { useUiStore } from '@/store/ui'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'

function ToastFixture() {
  const toasts = useUiStore((state) => state.toasts)
  return toasts.map((toast) => (
    <div key={toast.id}>
      {toast.message}
      {toast.action && (
        <Button variant="ghost" onClick={toast.action.onClick}>{toast.action.label}</Button>
      )}
    </div>
  ))
}

afterEach(() => {
  cleanup()
  useUiStore.setState({ activePanel: null, toasts: [] })
  vi.restoreAllMocks()
})

describe('advisory panel-tab presence', () => {
  it('offers Switch only and never lets a live remote presence duplicate locally (SP-18)', async () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastFixture />
      </>,
    )
    const announcement = announcePanelTabPresence({ panelId: 'library', workspaceId: 'ws-1' })

    try {
      await expect.poll(() => getPanelTabPresence()).toContainEqual({
        panelId: 'library',
        workspaceId: 'ws-1',
      })
      act(() => useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' }))

      expect(screen.getByRole('button', { name: 'Switch' })).toBeVisible()
      expect(screen.queryByRole('button', { name: 'Open here' })).not.toBeInTheDocument()
      expect(useUiStore.getState().activePanel).toBeNull()
    } finally {
      announcement.stop()
    }
  })

  it('allows a local dock when the registered remote handle reports closed', () => {
    const identity = { panelId: 'library', workspaceId: 'ws-1' } as const
    const staleHandle = { closed: true, focus: () => {} } as unknown as Window
    registerPanelTabHandle(identity, staleHandle)
    render(<PanelTabPresenceBridge />)

    try {
      act(() => useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' }))

      expect(useUiStore.getState().activePanel).toEqual({
        id: 'library',
        context: { workspaceId: 'ws-1' },
      })
      expect(useUiStore.getState().toasts).toHaveLength(0)
    } finally {
      forgetPanelTabHandle(identity, staleHandle)
    }
  })

  it('keeps a live manual tab singular and shows the manual-switch degrade when focus fails', async () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastFixture />
      </>,
    )
    const identity = { panelId: 'library', workspaceId: 'ws-1' } as const
    const announcement = announcePanelTabPresence(identity)
    vi.spyOn(window, 'focus').mockImplementation(() => {
      throw new Error('focus denied')
    })

    try {
      await expect.poll(() => getPanelTabPresence()).toContainEqual(identity)
      act(() => useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' }))
      fireEvent.click(screen.getByRole('button', { name: 'Switch' }))

      expect(await screen.findByText(/switch to that tab manually/i)).toBeVisible()
      expect(useUiStore.getState().activePanel).toBeNull()
      expect(getPanelTabPresence()).toContainEqual(identity)
      expect(screen.queryByRole('button', { name: 'Open here' })).not.toBeInTheDocument()
    } finally {
      announcement.stop()
    }
  })

  it('opens locally after the remote presence is explicitly cleared', async () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastFixture />
      </>,
    )
    const identity = { panelId: 'library', workspaceId: 'ws-1' } as const
    const announcement = announcePanelTabPresence(identity)
    vi.spyOn(window, 'focus').mockImplementation(() => {})

    await expect.poll(() => getPanelTabPresence()).toContainEqual(identity)
    act(() => useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' }))
    fireEvent.click(screen.getByRole('button', { name: 'Switch' }))
    announcement.stop()

    await expect.poll(() => useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'ws-1' },
    })
  })
})
