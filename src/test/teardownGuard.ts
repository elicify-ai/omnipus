// #1697 — teardown guard. A timer that is still pending when a test file ends
// fires AFTER Vitest has torn the jsdom environment down, and whatever it does
// (setState, dispatchEvent) dies with "window is not defined" as an UNHANDLED
// error with no test attached: all tests pass, the job exits 1, and nothing
// names the file. This module records every timer a file creates, and at the
// end of the file names the ones that are still pending, with the line that
// created each. Report-only by default: it never fails a test and never
// suppresses an error. The fix belongs at each source (clear the timer on
// unmount), found from this report.

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

/** Wraps the timer functions on `target` and tracks what is still pending. */
export function trackTimers(target: TimerTarget): TimerTracker {
  const original = {
    setTimeout: target.setTimeout,
    setInterval: target.setInterval,
    clearTimeout: target.clearTimeout,
    clearInterval: target.clearInterval,
  }
  const live = new Map<unknown, PendingTimer>()

  const wrapSet = (kind: TimerKind) =>
    ((fn: (...a: unknown[]) => void, delay?: number, ...args: unknown[]) => {
      const site = creationSite()
      const ref: { handle?: unknown } = {}
      const run = (...a: unknown[]) => {
        if (kind === 'setTimeout') live.delete(ref.handle)
        return fn(...a)
      }
      const handle = (original[kind] as (...a: unknown[]) => unknown)(run, delay, ...args)
      ref.handle = handle
      live.set(handle, { kind, delay: delay ?? 0, site })
      return handle
    })

  const wrapClear = (kind: 'clearTimeout' | 'clearInterval') =>
    ((handle?: unknown) => {
      live.delete(handle)
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
 * stderr, and from then on attributes any uncaught error to that file, which
 * Vitest itself reports without a file name. `TEARDOWN_GUARD=fail` makes a
 * pending timer fail the file; `off` disables it. Never swallows an error.
 */
export function installTeardownGuard(
  hooks: { afterAll: (fn: () => void) => void },
  fileName: () => string,
): void {
  const mode = guardMode()
  if (mode === 'off') return
  const tracker = trackTimers(globalThis as unknown as TimerTarget)
  let finished = false
  const late = (kind: string) => (err: unknown) => {
    if (!finished) return
    const text = err instanceof Error ? `${err.name}: ${err.message}` : String(err)
    process.stderr.write(`[teardown-guard] ${fileName()}: ${kind} after the file ended: ${text}\n`)
  }
  process.on('uncaughtException', late('uncaught exception'))
  process.on('unhandledRejection', late('unhandled rejection'))
  hooks.afterAll(() => {
    finished = true
    const pending = tracker.pending()
    const report = formatPending(fileName(), pending)
    if (report) process.stderr.write(report)
    tracker.restore()
    if (mode === 'fail' && pending.length > 0) throw new Error(report)
  })
}
