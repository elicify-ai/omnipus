import { act } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import {
  announcePanelTabPresence,
  getPanelTabPresence,
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

describe('PanelTabPresenceBridge', () => {
  afterEach(() => {
    cleanup()
    for (const toast of useUiStore.getState().toasts) {
      useUiStore.getState().removeToast(toast.id)
    }
    useUiStore.setState({ activePanel: null })
    vi.restoreAllMocks()
  })

  it('keeps a manually-opened Library tab singular and offers an explicit switch action', async () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastFixture />
      </>,
    )
    const announcement = announcePanelTabPresence({ panelId: 'library', workspaceId: 'ws-1' })
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})

    try {
      await vi.waitFor(() => {
        expect(getPanelTabPresence()).toContainEqual({ panelId: 'library', workspaceId: 'ws-1' })
      })

      act(() => {
        useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
      })

      expect(useUiStore.getState().activePanel).toBeNull()
      expect(screen.getByText(/Library is already open in another tab/i)).toBeVisible()
      expect(focus).not.toHaveBeenCalled()

      fireEvent.click(screen.getByRole('button', { name: 'Switch' }))
      await vi.waitFor(() => expect(focus).toHaveBeenCalledOnce())
    } finally {
      announcement.stop()
    }
  })
})
