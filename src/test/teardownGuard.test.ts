// #1697 — the teardown guard names the test file whose timers outlive it.
import { readFileSync } from 'node:fs'
import { promisify } from 'node:util'
import { resolve } from 'node:path'
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

describe('trackTimers: aliases and re-arming (CHECK round 2)', () => {
  it('clearing through the numeric string id cancels the timer and leaves nothing pending', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let ran = false
    const handle = target.setTimeout(() => { ran = true }, 5)
    target.clearTimeout(String(Number(handle)) as unknown as ReturnType<typeof setTimeout>)
    expect(tracker.pending()).toHaveLength(0)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran).toBe(false)
    tracker.restore()
  })

  it('a timeout re-armed with refresh() inside its callback is pending again', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let runs = 0
    let seen = -1
    await new Promise<void>((resolve) => {
      target.setTimeout(function (this: { refresh: () => void }) {
        runs++
        if (runs === 1) { this.refresh(); seen = tracker.pending().length; resolve() }
      } as () => void, 1)
    })
    expect(runs).toBe(1)
    expect(seen, 'pending while re-armed').toBe(1)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(runs, 'native timer fired again').toBe(2)
    expect(tracker.pending(), 'fired again, so no longer pending').toHaveLength(0)
    tracker.restore()
  })
})

describe('trackTimers: CHECK round 3 edges', () => {
  it('padded and hex strings are not aliases: the timer stays pending because Node ignores them', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let ran = 0
    const handle = target.setTimeout(() => { ran++ }, 5)
    target.clearTimeout(` ${Number(handle)} ` as unknown as ReturnType<typeof setTimeout>)
    target.clearTimeout(`0x${Number(handle).toString(16)}` as unknown as ReturnType<typeof setTimeout>)
    expect(tracker.pending()).toHaveLength(1)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran, 'native timer still fired').toBe(1)
    tracker.restore()
  })

  it('refresh() from outside after the timer fired: one entry, under the new id, gone after it fires', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let ran = 0
    const handle = target.setTimeout(() => { ran++ }, 3) as unknown as { refresh: () => void }
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran).toBe(1)
    handle.refresh(); handle.refresh()
    expect(tracker.pending(), 'one live timer, one entry').toHaveLength(1)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran).toBe(2)
    expect(tracker.pending(), 'fired, nothing stale').toHaveLength(0)
    tracker.restore()
  })

  it('refresh() of a cleared timer does not invent a pending timer', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let ran = 0
    const handle = target.setTimeout(() => { ran++ }, 5) as unknown as { refresh: () => void }
    target.clearTimeout(handle as unknown as ReturnType<typeof setTimeout>)
    handle.refresh()
    expect(tracker.pending()).toHaveLength(0)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran).toBe(0)
    tracker.restore()
  })

  it('the shared setup file really installs the guard (wiring is not covered by a fake hook)', () => {
    const setup = readFileSync(resolve(__dirname, 'setup.ts'), 'utf8')
    expect(setup).toMatch(/^installTeardownGuard\(\{ afterAll \}, /m)
  })
})

describe('trackTimers: CHECK round 4', () => {
  it('cancelling a pending timeout through its numeric id, then refresh(), invents nothing', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    let ran = 0
    const handle = target.setTimeout(() => { ran++ }, 5) as unknown as { refresh: () => void }
    target.clearTimeout(Number(handle) as unknown as ReturnType<typeof setTimeout>)
    handle.refresh()
    expect(tracker.pending()).toHaveLength(0)
    const h2 = target.setTimeout(() => { ran++ }, 5) as unknown as { refresh: () => void }
    target.clearTimeout(String(Number(h2)) as unknown as ReturnType<typeof setTimeout>)
    h2.refresh()
    expect(tracker.pending()).toHaveLength(0)
    await new Promise<void>((resolve) => setTimeout(resolve, 30))
    expect(ran).toBe(0)
    tracker.restore()
  })

  it('restore leaves a slot alone when fake timers replaced our wrapper, so their cleanup still works', () => {
    const target = nativeTarget()
    const nativeSet = target.setTimeout
    const tracker = trackTimers(target)
    const fake = (() => 0) as unknown as typeof setTimeout
    target.setTimeout = fake // what vi.useFakeTimers() does after the guard installed
    tracker.restore()
    expect(target.setTimeout, 'the fake is untouched').toBe(fake)
    expect(target.clearTimeout, 'a still-wrapped slot is restored').toBe(globalThis.clearTimeout)
    target.setTimeout = nativeSet
  })
})

describe('trackTimers: CHECK round 5 (native API compatibility)', () => {
  it('util.promisify(setTimeout) still resolves with the value', async () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    const sleep = promisify(target.setTimeout) as unknown as (ms: number, value: string) => Promise<string>
    await expect(sleep(1, 'value')).resolves.toBe('value')
    tracker.restore()
  })

  it('an invalid callback throws synchronously like native, and schedules nothing', () => {
    const target = nativeTarget()
    const tracker = trackTimers(target)
    expect(() => (target.setTimeout as unknown as (f: unknown, d: number) => void)(null, 60_000)).toThrow(TypeError)
    expect(() => (target.setInterval as unknown as (f: unknown, d: number) => void)(null, 60_000)).toThrow(TypeError)
    expect(tracker.pending()).toHaveLength(0)
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
