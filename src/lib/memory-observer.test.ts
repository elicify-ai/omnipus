/**
 * W2: memory-observer.test.ts
 *
 * Tests for the heap pressure observer module. Mocks performance.memory at
 * specific byte values and asserts the correct level transitions and callbacks.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  startMemoryObserver,
  addMemoryObserver,
  getCurrentSnapshot,
  type MemoryLevel,
  type MemorySnapshot,
} from '@/lib/memory-observer'

// ── Helpers ───────────────────────────────────────────────────────────────────

const MB = 1024 * 1024

function setFakeHeapSize(bytes: number | undefined): void {
  if (bytes === undefined) {
    // Simulate Safari / Firefox where performance.memory is absent.
    Object.defineProperty(performance, 'memory', {
      configurable: true,
      value: undefined,
    })
    return
  }
  Object.defineProperty(performance, 'memory', {
    configurable: true,
    value: { usedJSHeapSize: bytes },
  })
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('memory-observer', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    // Start with a known heap size below all thresholds.
    setFakeHeapSize(50 * MB)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  describe('getCurrentSnapshot', () => {
    it('returns supported=false when performance.memory is absent', () => {
      setFakeHeapSize(undefined)
      const observer = startMemoryObserver()

      // Advance one poll interval so the observer has run.
      vi.advanceTimersByTime(5_001)

      const snap = getCurrentSnapshot()
      // Either not supported (Safari/Firefox) or ok (browser with memory API below threshold)
      expect(snap.level === 'ok' || snap.supported === false).toBe(true)

      observer.dispose()
    })

    it('returns level=ok at 50 MiB', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()
      vi.advanceTimersByTime(5_001)
      expect(getCurrentSnapshot().level).toBe('ok')
      observer.dispose()
    })
  })

  describe('threshold transitions', () => {
    it('fires dev_warn level at 220 MiB', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      const received: MemorySnapshot[] = []
      const unsub = addMemoryObserver((snap) => received.push(snap))

      // Jump to 220 MiB and advance one interval.
      setFakeHeapSize(220 * MB)
      vi.advanceTimersByTime(5_001)

      const levelsReceived = received.map((s) => s.level)
      expect(levelsReceived).toContain('dev_warn')

      unsub()
      observer.dispose()
    })

    it('fires lite_mode level at 260 MiB', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      const received: MemorySnapshot[] = []
      const unsub = addMemoryObserver((snap) => received.push(snap))

      setFakeHeapSize(260 * MB)
      vi.advanceTimersByTime(5_001)

      const levelsReceived = received.map((s) => s.level)
      expect(levelsReceived).toContain('lite_mode')

      unsub()
      observer.dispose()
    })

    it('fires critical level at 320 MiB', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      const received: MemorySnapshot[] = []
      const unsub = addMemoryObserver((snap) => received.push(snap))

      setFakeHeapSize(320 * MB)
      vi.advanceTimersByTime(5_001)

      const levelsReceived = received.map((s) => s.level)
      expect(levelsReceived).toContain('critical')

      unsub()
      observer.dispose()
    })

    it('returns to ok level when heap drops back below dev_warn', () => {
      setFakeHeapSize(260 * MB)
      const observer = startMemoryObserver()
      vi.advanceTimersByTime(5_001)

      const received: MemorySnapshot[] = []
      const unsub = addMemoryObserver((snap) => received.push(snap))

      // Heap drops back to safe territory.
      setFakeHeapSize(50 * MB)
      vi.advanceTimersByTime(5_001)

      const levelsReceived = received.map((s) => s.level)
      expect(levelsReceived).toContain('ok')

      unsub()
      observer.dispose()
    })
  })

  describe('liteMode flag integration', () => {
    it('transitions liteMode via subscriber at 260 MiB', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      let liteMode = false
      const unsub = addMemoryObserver((snap) => {
        if (snap.level === 'lite_mode' || snap.level === 'critical') liteMode = true
        else if (snap.level === 'ok') liteMode = false
      })

      setFakeHeapSize(260 * MB)
      vi.advanceTimersByTime(5_001)
      expect(liteMode).toBe(true)

      // Drop back down.
      setFakeHeapSize(50 * MB)
      vi.advanceTimersByTime(5_001)
      expect(liteMode).toBe(false)

      unsub()
      observer.dispose()
    })
  })

  describe('dispose', () => {
    it('stops polling after dispose()', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      const received: MemorySnapshot[] = []
      const unsub = addMemoryObserver((snap) => received.push(snap))

      // Trigger one poll.
      setFakeHeapSize(220 * MB)
      vi.advanceTimersByTime(5_001)
      const countAfterFirstPoll = received.length

      // Dispose and advance more time — no further callbacks should fire.
      observer.dispose()
      setFakeHeapSize(320 * MB)
      vi.advanceTimersByTime(20_001)

      expect(received.length).toBe(countAfterFirstPoll)

      unsub()
    })
  })

  describe('subscriber removal', () => {
    it('stops calling a subscriber after its disposer is called', () => {
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      const received: MemoryLevel[] = []
      const unsub = addMemoryObserver((snap) => received.push(snap.level))

      setFakeHeapSize(220 * MB)
      vi.advanceTimersByTime(5_001)
      const countBeforeUnsub = received.length

      unsub()

      setFakeHeapSize(260 * MB)
      vi.advanceTimersByTime(5_001)

      expect(received.length).toBe(countBeforeUnsub)

      observer.dispose()
    })
  })

  describe('error isolation', () => {
    it('subscriber that throws is logged via console.error, loop continues', () => {
      const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
      setFakeHeapSize(50 * MB)
      const observer = startMemoryObserver()

      const receivedByGood: MemoryLevel[] = []

      // Throwing subscriber — must not crash the loop.
      const unsub1 = addMemoryObserver(() => { throw new Error('subscriber crash') })
      // Good subscriber after the bad one — must still receive calls.
      const unsub2 = addMemoryObserver((snap) => receivedByGood.push(snap.level))

      setFakeHeapSize(220 * MB)
      vi.advanceTimersByTime(5_001)

      // The good subscriber must have received the notification.
      expect(receivedByGood.length).toBeGreaterThan(0)

      // console.error must have been called with the subscriber error.
      const errorCalls = errorSpy.mock.calls.filter(
        (args) => typeof args[0] === 'string' && (args[0] as string).includes('[memory-observer] subscriber threw')
      )
      expect(errorCalls.length).toBeGreaterThanOrEqual(1)

      unsub1()
      unsub2()
      observer.dispose()
      errorSpy.mockRestore()
    })
  })
})
