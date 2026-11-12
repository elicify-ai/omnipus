// #1697 — a toast's auto-dismiss timer must not outlive its toast. A test (or a
// store reset) that replaces `toasts` used to leave the 4 s timer pending, so it
// could fire after the test file ended and its jsdom window was gone.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from './ui'

describe('toast auto-dismiss timers', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useUiStore.setState({ toasts: [] })
  })
  afterEach(() => {
    useUiStore.setState({ toasts: [] })
    vi.useRealTimers()
  })

  it('a toast still dismisses itself after its duration', () => {
    useUiStore.getState().addToast({ message: 'saved', variant: 'success' })
    expect(useUiStore.getState().toasts).toHaveLength(1)
    vi.advanceTimersByTime(3_999)
    expect(useUiStore.getState().toasts).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(useUiStore.getState().toasts).toHaveLength(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('replacing the toast list clears the timers of the removed toasts', () => {
    useUiStore.getState().addToast({ message: 'one', variant: 'default' })
    useUiStore.getState().addToast({ message: 'two', variant: 'default', duration: 10_000 })
    expect(vi.getTimerCount()).toBe(2)
    useUiStore.setState({ toasts: [] })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps the timer of a toast that is still in the list', () => {
    useUiStore.getState().addToast({ message: 'one', variant: 'default' })
    useUiStore.getState().addToast({ message: 'two', variant: 'default' })
    const [first] = useUiStore.getState().toasts
    useUiStore.setState({ toasts: [first] })
    expect(vi.getTimerCount()).toBe(1)
    vi.advanceTimersByTime(4_000)
    expect(useUiStore.getState().toasts).toHaveLength(0)
  })

  it('a toast with duration 0 never arms a timer', () => {
    useUiStore.getState().addToast({ message: 'sticky', variant: 'default', duration: 0 })
    expect(vi.getTimerCount()).toBe(0)
  })
})
