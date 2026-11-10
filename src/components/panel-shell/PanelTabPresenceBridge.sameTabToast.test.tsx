import { act } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { forgetPanelTabHandle, registerPanelTabHandle } from '@/lib/panelTabPresence'
import { useUiStore } from '@/store/ui'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'

function ToastFixture() {
  const toasts = useUiStore((state) => state.toasts)
  return toasts.map((toast) => <div role="status" key={toast.id}>{toast.message}</div>)
}

afterEach(() => {
  cleanup()
  useUiStore.setState({ activePanel: null, toasts: [] })
})

describe('same-tab registered panel handle', () => {
  it('confirms a focused existing Library tab with a visible toast, without opening a duplicate dock', () => {
    const identity = { panelId: 'library', workspaceId: 'ws-1' } as const
    const focus = vi.fn()
    const handle = { closed: false, focus } as unknown as Window
    registerPanelTabHandle(identity, handle)
    try {
      render(<><PanelTabPresenceBridge /><ToastFixture /></>)

      act(() => useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' }))

      expect(focus).toHaveBeenCalledTimes(1)
      expect(useUiStore.getState().activePanel).toBeNull()
      const toasts = useUiStore.getState().toasts
      expect(toasts).toHaveLength(1)
      expect(toasts[0]?.message).toBe("Library is open in another browser tab. If you don't see it, switch to that tab.")
      expect(screen.getByRole('status')).toBeVisible()
    } finally {
      forgetPanelTabHandle(identity, handle)
    }
  })
})
