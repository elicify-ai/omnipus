// useCancelState.test.ts — Stop/cancel button state machine (EC-15/FR-21/T23/T25).
// Extracted out of OmnipusComposer's own inline state; ChatScreen's existing
// integration tests exercise this through the rendered composer, these tests
// isolate the hook's own logic (the guarded vs. unconditional cancel variants,
// the minimum "Stopping..." display window, and the global Escape listener's
// race-window behavior) so a regression here fails fast and close to the cause.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useCancelState } from './useCancelState'
import { useChatStore } from '@/store/chat'

function pressEscape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ isStreaming: false })
  })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useCancelState — cancelIfStreaming (guarded)', () => {
  it('sets stopLabel to stopping and calls cancelStream when isStreaming is true', () => {
    const cancelStream = vi.fn()
    const { result } = renderHook(() => useCancelState(true, cancelStream))

    act(() => result.current.cancelIfStreaming())

    expect(result.current.stopLabel).toBe('stopping')
    expect(cancelStream).toHaveBeenCalledTimes(1)
  })

  it('leaves stopLabel as stop but still calls cancelStream when isStreaming is false', () => {
    // Mirrors /cancel and the local Escape handler firing on an already-finished
    // turn — cancelStream() still safely marks the last message interrupted.
    const cancelStream = vi.fn()
    const { result } = renderHook(() => useCancelState(false, cancelStream))

    act(() => result.current.cancelIfStreaming())

    expect(result.current.stopLabel).toBe('stop')
    expect(cancelStream).toHaveBeenCalledTimes(1)
  })
})

describe('useCancelState — cancelUnconditional (Stop button)', () => {
  it('always sets stopLabel to stopping and calls cancelStream, even when isStreaming is false', () => {
    // This is the race-window case the Stop button's onClick doc comment
    // describes: the button became visible while isStreaming was true, but by
    // click time the turn may have already finished.
    const cancelStream = vi.fn()
    const { result } = renderHook(() => useCancelState(false, cancelStream))

    act(() => result.current.cancelUnconditional())

    expect(result.current.stopLabel).toBe('stopping')
    expect(cancelStream).toHaveBeenCalledTimes(1)
  })
})

describe('useCancelState — T25 minimum "Stopping..." display duration', () => {
  it('keeps stopLabel as stopping for at least 1000ms after isStreaming flips false', () => {
    vi.useFakeTimers()
    const cancelStream = vi.fn()
    const { result, rerender } = renderHook(
      ({ isStreaming }) => useCancelState(isStreaming, cancelStream),
      { initialProps: { isStreaming: true } },
    )

    act(() => result.current.cancelUnconditional())
    expect(result.current.stopLabel).toBe('stopping')

    // Turn finishes almost immediately — isStreaming flips false.
    rerender({ isStreaming: false })
    // Still within the 1000ms minimum display window.
    expect(result.current.stopLabel).toBe('stopping')

    act(() => { vi.advanceTimersByTime(999) })
    expect(result.current.stopLabel).toBe('stopping')

    act(() => { vi.advanceTimersByTime(2) })
    expect(result.current.stopLabel).toBe('stop')
  })

  it('resets immediately to stop when isStreaming ends without stopLabel ever being stopping', () => {
    const cancelStream = vi.fn()
    const { result, rerender } = renderHook(
      ({ isStreaming }) => useCancelState(isStreaming, cancelStream),
      { initialProps: { isStreaming: true } },
    )
    rerender({ isStreaming: false })
    expect(result.current.stopLabel).toBe('stop')
  })
})

describe('useCancelState — T23 global Escape handler', () => {
  it('cancels when the live store reports isStreaming, even if the isStreaming prop is stale', () => {
    const cancelStream = vi.fn()
    renderHook(() => useCancelState(false, cancelStream))

    act(() => { useChatStore.setState({ isStreaming: true }) })
    act(() => pressEscape())

    expect(cancelStream).toHaveBeenCalledTimes(1)
  })

  it('cancels within the race window after streaming started, even after isStreaming flips false', () => {
    const cancelStream = vi.fn()
    const { rerender } = renderHook(
      ({ isStreaming }) => useCancelState(isStreaming, cancelStream),
      { initialProps: { isStreaming: true } },
    )
    // Turn completes fast — done frame clears isStreaming everywhere.
    act(() => { useChatStore.setState({ isStreaming: false }) })
    rerender({ isStreaming: false })

    act(() => pressEscape())

    expect(cancelStream).toHaveBeenCalledTimes(1)
  })

  it('does nothing when there is no streaming turn, no recent stream, and the button is not showing stopping', () => {
    const cancelStream = vi.fn()
    renderHook(() => useCancelState(false, cancelStream))

    act(() => pressEscape())

    expect(cancelStream).not.toHaveBeenCalled()
  })

  it('ignores non-Escape keys', () => {
    const cancelStream = vi.fn()
    renderHook(() => useCancelState(true, cancelStream))

    act(() => { useChatStore.setState({ isStreaming: true }) })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' })) })

    expect(cancelStream).not.toHaveBeenCalled()
  })

  it('removes the document listener on unmount', () => {
    const cancelStream = vi.fn()
    const { unmount } = renderHook(() => useCancelState(true, cancelStream))
    act(() => { useChatStore.setState({ isStreaming: true }) })

    unmount()
    act(() => pressEscape())

    expect(cancelStream).not.toHaveBeenCalled()
  })

  // bugfixes3 deferred item 5: a focused-textarea Escape mid-stream used to
  // double-dispatch cancelStream — once via ChatScreen.handleKeyDown's own
  // cancel-Escape branch (React synthetic onKeyDown, which preventDefaults
  // then calls cancelIfStreaming() directly), and a SECOND time via this
  // hook's own document-level bubble listener, which didn't check
  // defaultPrevented and re-ran its own shouldCancel logic against the same
  // native keydown as it continued bubbling to `document`. The fix: this
  // listener now early-returns when the event's default was already
  // prevented upstream — pressEscape() below constructs the event with
  // `cancelable: true` and calls `preventDefault()` on it BEFORE dispatch,
  // simulating exactly what the React synthetic handler already did to the
  // native event by the time it reaches this document-level listener.
  it('does not call cancelStream when the Escape event was already defaultPrevented upstream (kills the double dispatch)', () => {
    const cancelStream = vi.fn()
    renderHook(() => useCancelState(true, cancelStream))
    act(() => { useChatStore.setState({ isStreaming: true }) })

    act(() => {
      const event = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true })
      event.preventDefault()
      document.dispatchEvent(event)
    })

    expect(cancelStream).not.toHaveBeenCalled()
  })

  // Companion case: an Escape that reaches this listener WITHOUT any upstream
  // preventDefault (e.g. focus is nowhere near the composer — the FR-23/T23
  // "cancel even when unfocused" scenario) must still cancel normally. Proves
  // the defaultPrevented check only suppresses the double-fire case, not
  // legitimate global-Escape cancellation.
  it('still calls cancelStream for a plain (non-defaultPrevented) global Escape', () => {
    const cancelStream = vi.fn()
    renderHook(() => useCancelState(true, cancelStream))
    act(() => { useChatStore.setState({ isStreaming: true }) })

    act(() => pressEscape())

    expect(cancelStream).toHaveBeenCalledTimes(1)
  })
})
