// memory-observer.ts — polls performance.memory (Chrome/Edge only) and notifies
// subscribers on heap threshold transitions (200 MiB dev_warn / 250 MiB lite_mode / 300 MiB critical).

// Thresholds in bytes.
const THRESHOLD_DEV_WARN = 200 * 1024 * 1024   // 200 MiB
const THRESHOLD_LITE_MODE = 250 * 1024 * 1024  // 250 MiB
const THRESHOLD_CRITICAL = 300 * 1024 * 1024   // 300 MiB

const POLL_INTERVAL_MS = 5_000

export type MemoryLevel = 'ok' | 'dev_warn' | 'lite_mode' | 'critical'

export interface MemorySnapshot { // not-wire-format: purely client-side telemetry for the iPad-OOM observer; never serialized across the gateway/SPA boundary
  usedJSHeapSizeBytes: number | null
  level: MemoryLevel
  supported: boolean
}

export type MemoryObserverCallback = (snapshot: MemorySnapshot) => void

// ── Global subscriber registry ────────────────────────────────────────────────
// Allows multiple consumers (OmnipusRuntimeProvider, tests) to subscribe.

const subscribers = new Set<MemoryObserverCallback>()
let currentSnapshot: MemorySnapshot = {
  usedJSHeapSizeBytes: null,
  level: 'ok',
  supported: false,
}

/**
 * Subscribe to memory level changes. The callback fires on every poll if the
 * level or heap size changed. Returns a disposer function.
 */
export function addMemoryObserver(cb: MemoryObserverCallback): () => void {
  subscribers.add(cb)
  return () => subscribers.delete(cb)
}

/** Return the most recent snapshot without polling. */
export function getCurrentSnapshot(): MemorySnapshot {
  return currentSnapshot
}

function notify(snapshot: MemorySnapshot): void {
  for (const cb of subscribers) {
    try {
      cb(snapshot)
    } catch (err) {
      console.error('[memory-observer] subscriber threw', err)
    }
  }
}

function classify(bytes: number): MemoryLevel {
  if (bytes >= THRESHOLD_CRITICAL) return 'critical'
  if (bytes >= THRESHOLD_LITE_MODE) return 'lite_mode'
  if (bytes >= THRESHOLD_DEV_WARN) return 'dev_warn'
  return 'ok'
}

function sample(): MemorySnapshot {
  // performance.memory is a Chrome/Edge extension; typed as optional.
  const mem = (performance as Performance & { memory?: { usedJSHeapSize?: number } }).memory
  if (!mem || typeof mem.usedJSHeapSize !== 'number') {
    return { usedJSHeapSizeBytes: null, level: 'ok', supported: false }
  }
  const bytes = mem.usedJSHeapSize
  return { usedJSHeapSizeBytes: bytes, level: classify(bytes), supported: true }
}

export interface MemoryObserver { // not-wire-format: handle returned from startMemoryObserver to the SPA lifecycle; never serialized
  dispose: () => void
  getCurrentSnapshot: () => MemorySnapshot
}

/**
 * Start the memory observer. Call on app mount; call dispose() on unmount.
 * Multiple calls are safe — each returns an independent disposer.
 */
export function startMemoryObserver(): MemoryObserver {
  let devWarnFired = false
  let disposed = false

  const handle = setInterval(() => {
    if (disposed) return

    const snap = sample()
    const prev = currentSnapshot

    // Always update current snapshot.
    currentSnapshot = snap

    // Only notify + log when something changed.
    if (
      snap.usedJSHeapSizeBytes === prev.usedJSHeapSizeBytes &&
      snap.level === prev.level &&
      snap.supported === prev.supported
    ) {
      return
    }

    // Emit console messages for elevated levels.
    if (snap.supported) {
      if (snap.level === 'critical') {
        console.error(
          '[memory-observer] Heap critical — consider reloading the page.',
          `Used: ${Math.round((snap.usedJSHeapSizeBytes ?? 0) / (1024 * 1024))} MiB`,
        )
      } else if (snap.level === 'lite_mode') {
        console.warn(
          '[memory-observer] Heap pressure elevated — lite mode active.',
          `Used: ${Math.round((snap.usedJSHeapSizeBytes ?? 0) / (1024 * 1024))} MiB`,
        )
      } else if (snap.level === 'dev_warn' && !devWarnFired) {
        // Emit as a non-blocking dev hint — use console.info so it doesn't
        // look like a bug in production monitoring dashboards.
        console.info(
          '[memory-observer] Heap approaching iOS limit (~250 MiB).',
          `Used: ${Math.round((snap.usedJSHeapSizeBytes ?? 0) / (1024 * 1024))} MiB`,
        )
        devWarnFired = true
      }
    }

    notify(snap)
  }, POLL_INTERVAL_MS)

  return {
    dispose: () => {
      disposed = true
      clearInterval(handle)
    },
    getCurrentSnapshot: () => currentSnapshot,
  }
}
