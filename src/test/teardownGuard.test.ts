// #1697 — the teardown guard names the test file whose timers outlive it.
import { describe, expect, it } from 'vitest'
import { formatPending, trackTimers, type TimerTarget } from './teardownGuard'

/** A manual clock standing in for the global timer functions. */
function fakeTarget() {
  let next = 1
  const live = new Map<number, () => void>()
  const target: TimerTarget = {
    setTimeout: ((fn: (...a: unknown[]) => void, _d?: number, ...args: unknown[]) => { const id = next++; live.set(id, () => fn(...args)); return id }) as unknown as TimerTarget['setTimeout'],
    setInterval: ((fn: (...a: unknown[]) => void, _d?: number, ...args: unknown[]) => { const id = next++; live.set(id, () => fn(...args)); return id }) as unknown as TimerTarget['setInterval'],
    clearTimeout: ((id: number) => { live.delete(id) }) as TimerTarget['clearTimeout'],
    clearInterval: ((id: number) => { live.delete(id) }) as TimerTarget['clearInterval'],
  }
  const fire = (id: number) => { const fn = live.get(id); live.delete(id); fn?.() }
  return { target, fire, live }
}

describe('trackTimers', () => {
  it('reports a timeout that is still pending, with its delay and creation site', () => {
    const { target } = fakeTarget()
    const tracker = trackTimers(target)
    target.setTimeout(() => undefined, 150)
    const pending = tracker.pending()
    expect(pending).toHaveLength(1)
    expect(pending[0].kind).toBe('setTimeout')
    expect(pending[0].delay).toBe(150)
    expect(pending[0].site).toContain('teardownGuard.test.ts')
    tracker.restore()
  })

  it('positive control: a timer that fired is not reported', () => {
    const { target, fire } = fakeTarget()
    const tracker = trackTimers(target)
    let ran = false
    const id = target.setTimeout(() => { ran = true }, 0) as unknown as number
    fire(id)
    expect(ran).toBe(true)
    expect(tracker.pending()).toHaveLength(0)
    tracker.restore()
  })

  it('a cleared timer is not reported', () => {
    const { target } = fakeTarget()
    const tracker = trackTimers(target)
    const id = target.setTimeout(() => undefined, 150)
    target.clearTimeout(id)
    expect(tracker.pending()).toHaveLength(0)
    tracker.restore()
  })

  it('an interval stays pending until it is cleared', () => {
    const { target, fire } = fakeTarget()
    const tracker = trackTimers(target)
    const id = target.setInterval(() => undefined, 1000)
    fire(id as unknown as number)
    expect(tracker.pending()).toHaveLength(1)
    target.clearInterval(id)
    expect(tracker.pending()).toHaveLength(0)
    tracker.restore()
  })

  it('still runs the original callback with its arguments', () => {
    const { target, fire } = fakeTarget()
    const tracker = trackTimers(target)
    const seen: unknown[] = []
    const id = target.setTimeout(((a: unknown, b: unknown) => seen.push(a, b)) as unknown as () => void, 0, 'x', 'y') as unknown as number
    fire(id)
    expect(seen).toEqual(['x', 'y'])
    tracker.restore()
  })

  it('restore puts the original timer functions back', () => {
    const { target } = fakeTarget()
    const original = target.setTimeout
    const tracker = trackTimers(target)
    expect(target.setTimeout).not.toBe(original)
    tracker.restore()
    expect(target.setTimeout).toBe(original)
  })
})

describe('formatPending', () => {
  it('names the file and lists each pending timer', () => {
    const text = formatPending('src/components/rooms/RoomScreen.history.test.tsx', [
      { kind: 'setTimeout', delay: 150, site: 'src/components/rooms/useRoomComposerMenu.ts:197:43' },
    ])
    expect(text).toContain('src/components/rooms/RoomScreen.history.test.tsx')
    expect(text).toContain('setTimeout 150ms')
    expect(text).toContain('useRoomComposerMenu.ts:197:43')
  })

  it('returns nothing when no timer is pending', () => {
    expect(formatPending('a.test.ts', [])).toBe('')
  })
})
