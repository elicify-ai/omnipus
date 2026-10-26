// Persistent informational notices must remain until dismissal; ordinary
// transient toasts must keep their existing four-second default.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from './ui'

beforeEach(() => {
  vi.useFakeTimers()
  useUiStore.setState({ toasts: [] })
})
afterEach(() => {
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.clearAllTimers()
  vi.useRealTimers()
})

describe('informational toast lifetime', () => {
  it('keeps duration zero visible until explicitly dismissed', () => {
    useUiStore.getState().addToast({ message: 'Persistent loading information', variant: 'default', duration: 0 })
    const notice = useUiStore.getState().toasts[0]
    vi.advanceTimersByTime(60_000)
    expect(useUiStore.getState().toasts).toEqual([notice])
    useUiStore.getState().removeToast(notice.id)
    expect(useUiStore.getState().toasts).toEqual([])
  })

  it('still expires an ordinary toast after its existing four-second default', () => {
    useUiStore.getState().addToast({ message: 'Transient information', variant: 'default' })
    vi.advanceTimersByTime(3999)
    expect(useUiStore.getState().toasts.map(toast => toast.message)).toEqual(['Transient information'])
    vi.advanceTimersByTime(1)
    expect(useUiStore.getState().toasts).toEqual([])
  })
})
