// #1697 — the teardown guard names the test file whose timers outlive it.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatPending, installTeardownGuard, trackTimers, type TimerTarget } from './teardownGuard'

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

  it('a cleared timer is not reported, and the underlying timer is really cancelled', () => {
    const { target, fire, live } = fakeTarget()
    const tracker = trackTimers(target)
    let ran = false
    const id = target.setTimeout(() => { ran = true }, 150) as unknown as number
    target.clearTimeout(id as unknown as ReturnType<typeof setTimeout>)
    expect(tracker.pending()).toHaveLength(0)
    expect(live.size, 'the original clearTimeout ran').toBe(0)
    fire(id)
    expect(ran, 'a cleared callback never runs').toBe(false)
    tracker.restore()
  })

  it('an interval keeps ticking until cleared (clock double repeats it)', () => {
    const { target, fire } = fakeTarget()
    const tracker = trackTimers(target)
    let ticks = 0
    const id = target.setInterval(() => { ticks++ }, 10) as unknown as number
    fire(id); fire(id)
    expect(ticks).toBe(1) // the fake drops a fired entry; the tracker must still say pending
    expect(tracker.pending()).toHaveLength(1)
    target.clearInterval(id as unknown as ReturnType<typeof setInterval>)
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

/** Real Node timers behind a plain object: the wrapper is exercised against the native implementation. */
function nativeTarget(): TimerTarget {
  return {
    setTimeout: globalThis.setTimeout, setInterval: globalThis.setInterval,
    clearTimeout: globalThis.clearTimeout, clearInterval: globalThis.clearInterval,
  }
}

describe('trackTimers against native timers (CHECK round 1, findings 3 and 4)', () => {
  it('keeps the native receiver: the callback sees its own timer as this', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    const seen = await new Promise<unknown>((resolve) => {
      const handle = target.setTimeout(function (this: unknown) { resolve([this, handle]) } as () => void, 0)
    })
    const [self, handle] = seen as [unknown, unknown]
    expect(self).toBe(handle)
    tracker.restore()
  })

  it('clearing through the numeric id cancels the timer and leaves nothing pending', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let ran = false
    const handle = target.setTimeout(() => { ran = true }, 5)
    target.clearTimeout(Number(handle) as unknown as ReturnType<typeof setTimeout>)
    expect(tracker.pending()).toHaveLength(0)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran, 'native cancellation succeeded').toBe(false)
    tracker.restore()
  })
})

describe('installTeardownGuard', () => {
  afterEach(() => { vi.unstubAllEnvs(); vi.restoreAllMocks() })

  function install(mode?: string) {
    if (mode) vi.stubEnv('TEARDOWN_GUARD', mode)
    const { target } = fakeTarget()
    const original = target.setTimeout
    let afterAll: (() => void) | undefined
    installTeardownGuard({ afterAll: (fn) => { afterAll = fn } }, () => 'src/x/some.test.ts', target)
    return { target, original, run: () => afterAll?.(), registered: () => afterAll !== undefined }
  }

  it('wraps the timers and, when the file ends, names the file and the pending timer', () => {
    const write = vi.spyOn(process.stderr, 'write').mockImplementation(() => true)
    const g = install()
    expect(g.target.setTimeout).not.toBe(g.original)
    g.target.setTimeout(() => undefined, 150)
    g.run()
    const text = write.mock.calls.map((c) => String(c[0])).join('')
    expect(text).toContain('src/x/some.test.ts')
    expect(text).toContain('setTimeout 150ms')
    expect(g.target.setTimeout, 'restored at file end').toBe(g.original)
  })

  it('stays silent when nothing is pending', () => {
    const write = vi.spyOn(process.stderr, 'write').mockImplementation(() => true)
    const g = install()
    g.run()
    expect(write).not.toHaveBeenCalled()
  })

  it('report mode does not throw, fail mode throws naming the file, off installs nothing', () => {
    vi.spyOn(process.stderr, 'write').mockImplementation(() => true)
    const report = install('report'); report.target.setTimeout(() => undefined, 1)
    expect(() => report.run()).not.toThrow()
    const fail = install('fail'); fail.target.setTimeout(() => undefined, 1)
    expect(() => fail.run()).toThrow(/some\.test\.ts/)
    const off = install('off')
    expect(off.registered()).toBe(false)
    expect(off.target.setTimeout).toBe(off.original)
  })

  it('adds no process error listener: Vitest keeps reporting unhandled errors itself', () => {
    const before = [process.listenerCount('uncaughtException'), process.listenerCount('unhandledRejection')]
    install()
    expect([process.listenerCount('uncaughtException'), process.listenerCount('unhandledRejection')]).toEqual(before)
  })
})
