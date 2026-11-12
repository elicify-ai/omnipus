// #1697 (CHECK round 4) — a file that installs fake timers in beforeAll and
// returns vi.useRealTimers() as that hook's cleanup runs the cleanup AFTER the
// shared afterAll hooks, i.e. after the teardown guard restored its slots. The
// guard must not leave the four timer globals undefined. A throw in the
// returned cleanup fails the file, so the check runs where the damage happens.
import { beforeAll, expect, it, vi } from 'vitest'

beforeAll(() => {
  vi.useFakeTimers()
  return () => {
    vi.useRealTimers()
    for (const name of ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval'] as const) {
      if (typeof globalThis[name] !== 'function') {
        throw new Error(`${name} is ${typeof globalThis[name]} after fake-timer cleanup (teardown guard broke the restore order)`)
      }
    }
  }
})

it('fake timers work with the guard installed', () => {
  let ran = 0
  setTimeout(() => { ran++ }, 10)
  vi.advanceTimersByTime(10)
  expect(ran).toBe(1)
})
