import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { ToastContainer } from '@/components/ui/toast-container'
import { announcePanelTabPresence, getPanelTabPresence } from '@/lib/panelTabPresence'
import { useUiStore } from '@/store/ui'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'

afterEach(() => {
  cleanup()
  useUiStore.setState({ activePanel: null, toasts: [] })
})

describe('advisory panel-tab presence', () => {
  it('offers Open here beside Switch and allows the local dock to win', async () => {
    render(
      <>
        <PanelTabPresenceBridge />
        <ToastContainer />
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
      expect(screen.getByRole('button', { name: 'Open here' })).toBeVisible()
      fireEvent.click(screen.getByRole('button', { name: 'Open here' }))

      expect(useUiStore.getState().activePanel).toEqual({
        id: 'library',
        context: { workspaceId: 'ws-1' },
      })
    } finally {
      announcement.stop()
    }
  })
})
