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

type TimerKind = 'setTimeout' | 'setInterval'

export interface PendingTimer {
  kind: TimerKind
  delay: number
  /** `path:line:col` of the first stack frame outside this module. */
  site: string
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

  const wrapSet = (kind: TimerKind) =>
    ((fn: (...a: unknown[]) => void, delay?: number, ...args: unknown[]) => {
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
      const entry: PendingTimer = { kind, delay: delay ?? 0, site }
      live.set(handleKey(handle), entry)
      // Timeout.refresh() re-arms a timer that already fired: it is pending again.
      const timer = handle as { refresh?: () => unknown } | null
      if (kind === 'setTimeout' && timer && typeof timer.refresh === 'function') {
        const refresh = timer.refresh
        timer.refresh = function (this: unknown) {
          // refresh() can give the timer a new numeric id: drop the old key first.
          live.delete(handleKey(handle))
          const result = refresh.call(this)
          if (!cancelled.has(timer)) live.set(handleKey(handle), entry)
          return result
        }
      }
      return handle
    })

  const wrapClear = (kind: 'clearTimeout' | 'clearInterval') =>
    ((handle?: unknown) => {
      if (typeof handle === 'object' && handle !== null) cancelled.add(handle)
      live.delete(handleKey(handle))
      return (original[kind] as (h?: unknown) => void)(handle)
    })

  target.setTimeout = wrapSet('setTimeout') as unknown as typeof setTimeout
  target.setInterval = wrapSet('setInterval') as unknown as typeof setInterval
  target.clearTimeout = wrapClear('clearTimeout') as unknown as typeof clearTimeout
  target.clearInterval = wrapClear('clearInterval') as unknown as typeof clearInterval

  return {
    pending: () => [...live.values()],
    restore: () => {
      target.setTimeout = original.setTimeout
      target.setInterval = original.setInterval
      target.clearTimeout = original.clearTimeout
      target.clearInterval = original.clearInterval
    },
  }
}

/** Human-readable report for one test file; empty string when nothing is pending. */
export function formatPending(file: string, pending: PendingTimer[]): string {
  if (pending.length === 0) return ''
  const lines = pending.map((p) => `  - ${p.kind} ${p.delay}ms created at ${p.site}`)
  return `[teardown-guard] ${file}: ${pending.length} timer(s) still pending when the file ended:\n${lines.join('\n')}\n`
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
    const pending = tracker.pending()
    const report = formatPending(fileName(), pending)
    if (report) process.stderr.write(report)
    tracker.restore()
    if (mode === 'fail' && pending.length > 0) throw new Error(report)
  })
}
