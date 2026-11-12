// #1697 — the blur timer must not outlive the composer. A 150 ms timer that is
// still pending when the component unmounts (or the test file ends) calls
// setState on a dead tree, or after jsdom is gone ("window is not defined").
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { useRoomComposerMenu } from './useRoomComposerMenu'

function fakeRuntime(): ComposerRuntime {
  return {
    subscribe: () => () => undefined,
    getState: () => ({ text: '' }),
    setText: () => undefined,
  } as unknown as ComposerRuntime
}

function mount() {
  return renderHook(() => useRoomComposerMenu({ composerRuntime: fakeRuntime(), members: [], muted: [] }))
}

afterEach(() => { vi.useRealTimers() })

describe('useRoomComposerMenu blur timer (#1697)', () => {
  it('leaves no timer pending after unmount', () => {
    vi.useFakeTimers()
    const view = mount()
    act(() => { view.result.current.onInputBlur() })
    expect(vi.getTimerCount(), 'blur arms the dismiss timer').toBe(1)
    view.unmount()
    expect(vi.getTimerCount(), 'unmount must clear it').toBe(0)
  })

  it('positive control: the blur still dismisses the menu after 150 ms', () => {
    vi.useFakeTimers()
    const view = mount()
    act(() => { view.result.current.onInputChange('/') })
    expect(view.result.current.slashOpen).toBe(true)
    act(() => { view.result.current.onInputBlur() })
    act(() => { vi.advanceTimersByTime(150) })
    expect(view.result.current.slashOpen).toBe(false)
    view.unmount()
  })

  it('typing again before the timer fires cancels the stale dismiss', () => {
    vi.useFakeTimers()
    const view = mount()
    act(() => { view.result.current.onInputChange('/') })
    act(() => { view.result.current.onInputBlur() })
    act(() => { view.result.current.onInputChange('/m') })
    act(() => { vi.advanceTimersByTime(150) })
    expect(view.result.current.slashOpen, 'the menu must stay open while typing').toBe(true)
    view.unmount()
  })
})
