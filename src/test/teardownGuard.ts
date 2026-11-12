// #1697 — teardown guard. A timer that is still pending when a test file ends
// fires AFTER Vitest has torn the jsdom environment down, and whatever it does
// (setState, dispatchEvent) dies with "window is not defined" as an UNHANDLED
// error with no test attached: all tests pass, the job exits 1, and nothing
// names the file. This module records every timer a file creates, and at the
// end of the file names the ones that are still pending, with the line that
// created each. Report-only by default: it never fails a test and never
// suppresses an error. The fix belongs at each source (clear the timer on
// unmount), found from this report.
//
// It registers NO process-level error listener: Vitest skips its own unhandled
// error report when another uncaughtException/unhandledRejection listener
// exists, so any such listener would hide the very failure this guard helps to
// find (CHECK round 1, finding 1).

import { promisify } from 'node:util'

type TimerKind = 'setTimeout' | 'setInterval'

export interface PendingTimer {
  kind: TimerKind
  delay: number
  /** `path:line:col` of the first stack frame outside this module. */
  site: string
  /** Date.now() when armed (reset by refresh()); used to tell how long is left. */
  armedAt?: number
}

export interface TimerTarget {
  setTimeout: typeof setTimeout
  setInterval: typeof setInterval
  clearTimeout: typeof clearTimeout
  clearInterval: typeof clearInterval
}

export interface TimerTracker {
  pending(): PendingTimer[]
  restore(): void
}

const FRAME = /\(?((?:[A-Za-z]:)?[^()\s]+:\d+:\d+)\)?$/

/** First stack frame that is not this module or node internals; prefers project source over node_modules. */
function creationSite(): string {
  const frames = (new Error().stack ?? '').split('\n').slice(1)
  const sites: string[] = []
  for (const line of frames) {
    const m = FRAME.exec(line.trim())
    if (!m) continue
    const site = m[1].replace(/^file:\/\//, '')
    if (site.includes('/teardownGuard.ts:') || site.startsWith('node:')) continue
    sites.push(site)
  }
  const own = sites.find((s) => !s.includes('/node_modules/'))
  const chosen = own ?? sites[0] ?? 'unknown'
  const cwd = process.cwd() + '/'
  return chosen.startsWith(cwd) ? chosen.slice(cwd.length) : chosen
}

/** Node timers are objects that also accept their numeric id in clearTimeout; key both forms the same. */
function handleKey(handle: unknown): unknown {
  if (typeof handle === 'object' && handle !== null) {
    const id = Number(handle)
    return Number.isFinite(id) ? id : handle
  }
  // Node also accepts the id as a CANONICAL decimal string ("57"). Padded or
  // hex strings ("57 ", "0x39") are looked up literally by Node, so they are
  // not aliases and must not be normalized.
  if (typeof handle === 'string' && /^(0|[1-9]\d*)$/.test(handle)) return Number(handle)
  return handle
}

/** Wraps the timer functions on `target` and tracks what is still pending. */
export function trackTimers(target: TimerTarget): TimerTracker {
  const original = {
    setTimeout: target.setTimeout,
    setInterval: target.setInterval,
    clearTimeout: target.clearTimeout,
    clearInterval: target.clearInterval,
  }
  const live = new Map<unknown, PendingTimer>()
  // Handles cleared through the object: Node's refresh() does not re-arm those.
  // (A clear through the numeric id AFTER the timer fired is not seen here.)
  const cancelled = new WeakSet<object>()
  // key -> the handle object, so a clear through the numeric id can mark it cancelled too.
  const owner = new Map<unknown, unknown>()

  const wrapSet = (kind: TimerKind) =>
    ((fn: (...a: unknown[]) => void, delay?: number, ...args: unknown[]) => {
      // An invalid callback must fail exactly as natively, synchronously, from
      // the caller's own call: hand it straight to the original.
      if (typeof fn !== 'function') return (original[kind] as (...a: unknown[]) => unknown)(fn, delay, ...args)
      const site = creationSite()
      const ref: { handle?: unknown } = {}
      // A plain function, not an arrow: Node calls a timer callback with the
      // timer as `this`, and callers may rely on it (`this.refresh()`).
      const run = function (this: unknown, ...a: unknown[]) {
        if (kind === 'setTimeout') live.delete(handleKey(ref.handle))
        return fn.apply(this, a)
      }
      const handle = (original[kind] as (...a: unknown[]) => unknown)(run, delay, ...args)
      ref.handle = handle
      const entry: PendingTimer = { kind, delay: delay ?? 0, site, armedAt: Date.now() }
      live.set(handleKey(handle), entry)
      owner.set(handleKey(handle), handle)
      // Timeout.refresh() re-arms a timer that already fired: it is pending again.
      const timer = handle as { refresh?: () => unknown } | null
      if (kind === 'setTimeout' && timer && typeof timer.refresh === 'function') {
        const refresh = timer.refresh
        timer.refresh = function (this: unknown) {
          // refresh() can give the timer a new numeric id: drop the old key first.
          live.delete(handleKey(handle))
          const result = refresh.call(this)
          entry.armedAt = Date.now()
          if (!cancelled.has(timer)) { live.set(handleKey(handle), entry); owner.set(handleKey(handle), handle) }
          return result
        }
      }
      return handle
    })

  const wrapClear = (kind: 'clearTimeout' | 'clearInterval') =>
    ((handle?: unknown) => {
      const target = typeof handle === 'object' && handle !== null ? handle : owner.get(handleKey(handle))
      if (typeof target === 'object' && target !== null) cancelled.add(target)
      live.delete(handleKey(handle))
      return (original[kind] as (h?: unknown) => void)(handle)
    })

  // Node's setTimeout/setInterval carry util.promisify.custom; keep it so
  // promisify(setTimeout) still resolves (the native promise version is used).
  const keepPromisify = <F extends object>(wrapper: F, native: unknown): F => {
    const custom = (native as Record<symbol, unknown> | undefined)?.[promisify.custom]
    if (custom !== undefined) Object.defineProperty(wrapper, promisify.custom, { value: custom, configurable: true })
    return wrapper
  }
  const wrapped = {
    setTimeout: keepPromisify(wrapSet('setTimeout') as unknown as typeof setTimeout, original.setTimeout),
    setInterval: keepPromisify(wrapSet('setInterval') as unknown as typeof setInterval, original.setInterval),
    clearTimeout: wrapClear('clearTimeout') as unknown as typeof clearTimeout,
    clearInterval: wrapClear('clearInterval') as unknown as typeof clearInterval,
  }
  target.setTimeout = wrapped.setTimeout
  target.setInterval = wrapped.setInterval
  target.clearTimeout = wrapped.clearTimeout
  target.clearInterval = wrapped.clearInterval

  return {
    pending: () => [...live.values()],
    restore: () => {
      // Put an original back only if the slot still holds OUR wrapper. If a
      // test installed fake timers over it (vi.useFakeTimers), the fake owns
      // the slot and its own cleanup (possibly running after this hook) must
      // find it untouched.
      if (target.setTimeout === wrapped.setTimeout) target.setTimeout = original.setTimeout
      if (target.setInterval === wrapped.setInterval) target.setInterval = original.setInterval
      if (target.clearTimeout === wrapped.clearTimeout) target.clearTimeout = original.clearTimeout
      if (target.clearInterval === wrapped.clearInterval) target.clearInterval = original.clearInterval
    },
  }
}

/** Human-readable report for one test file; empty string when nothing is pending. */
export function formatPending(file: string, pending: PendingTimer[]): string {
  if (pending.length === 0) return ''
  const lines = pending.map((p) => `  - ${p.kind} ${p.delay}ms created at ${p.site}`)
  return `[teardown-guard] ${file}: ${pending.length} timer(s) still pending when the file ended:\n${lines.join('\n')}\n`
}

/**
 * A timer with more than this left cannot fire before a test file ends (a shard
 * runs for minutes, a file for seconds), so it is left out of the report: the
 * report lists the timers that can fire after teardown (#1697). Query-cache
 * garbage collection timers (5 min) were 95% of the first hosted report.
 */
export const REPORT_MAX_DELAY_MS = 60_000

const NODE_MAX_DELAY_MS = 2 ** 31 - 1

/** The delay the runtime really uses: Node runs a missing, NaN, < 1 or > 2^31-1 (Infinity) delay at 1 ms. */
export function effectiveDelayMs(delay: number): number {
  return delay >= 1 && delay <= NODE_MAX_DELAY_MS ? delay : 1
}

/** Time until the timer next fires, from when it was armed (an interval: until its next tick). */
export function remainingMs(p: PendingTimer, now: number): number {
  const delay = effectiveDelayMs(p.delay)
  const elapsed = Math.max(0, now - (p.armedAt ?? now))
  return p.kind === 'setInterval' ? delay - (elapsed % delay) : delay - elapsed
}

type GuardMode = 'off' | 'report' | 'fail'

function guardMode(): GuardMode {
  const raw = process.env.TEARDOWN_GUARD
  return raw === 'off' || raw === 'fail' ? raw : 'report'
}

/**
 * Per-file install, called from the shared setup file. Tracks timers for the
 * whole file; when the file ends, writes the pending ones (naming the file) to
 * stderr. The late error itself stays Vitest's to report. `TEARDOWN_GUARD=fail`
 * makes a pending timer fail the file; `off` disables it. Never swallows an
 * error and never adds a process error listener.
 */
export function installTeardownGuard(
  hooks: { afterAll: (fn: () => void) => void },
  fileName: () => string,
  target: TimerTarget = globalThis as unknown as TimerTarget,
): void {
  const mode = guardMode()
  if (mode === 'off') return
  const tracker = trackTimers(target)
  hooks.afterAll(() => {
    const now = Date.now()
    const pending = tracker.pending().filter((p) => remainingMs(p, now) <= REPORT_MAX_DELAY_MS)
    const report = formatPending(fileName(), pending)
    if (report) process.stderr.write(report)
    tracker.restore()
    if (mode === 'fail' && pending.length > 0) throw new Error(report)
  })
}
