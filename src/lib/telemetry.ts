// Minimal telemetry sink for production observability.
//
// Omnipus does not ship a bundled telemetry SDK (privacy: no third-party
// network calls, MIT-licensed, no user tracking). This module exposes a
// thin abstraction so call sites don't depend on a specific backend.
//
// In dev/test the helpers are no-ops on the console — the existing dev
// counters and dev-toast helpers in src/lib/ws.ts handle richer
// feedback. In production we emit structured console.error records
// (JSON) so a log-collector (the operator's choice — journald, Loki,
// Sentry's console-error capture, etc.) can pick them up.
//
// If/when the project wires a real SDK, only this file needs to change.

export interface TelemetryEvent { // not-wire-format: SPA-internal log sink shape — never serialized across the gateway/SPA boundary, only console.error JSON output
  /** Stable event name, e.g. "wsFrameDropped". Camel-case, dot-separated for grouping. */
  event: string
  /** Free-form properties. Strings, numbers, booleans, null. No objects (keeps it grep-friendly). */
  [key: string]: string | number | boolean | null | undefined
}

let _sampleWindowStart = 0
let _sampleCount = 0
const SAMPLE_WINDOW_MS = 60_000
const SAMPLE_LIMIT = 10

function _shouldEmit(): boolean {
  // Coarse rate-limit: emit at most SAMPLE_LIMIT events per SAMPLE_WINDOW_MS
  // per page-load to avoid log floods if a contract drift explodes. The
  // window resets on first call after expiry so a slow trickle stays
  // visible but a burst is throttled.
  const now = Date.now()
  if (now - _sampleWindowStart > SAMPLE_WINDOW_MS) {
    _sampleWindowStart = now
    _sampleCount = 0
  }
  if (_sampleCount >= SAMPLE_LIMIT) return false
  _sampleCount++
  return true
}

/**
 * Emit a structured telemetry event.
 *
 * Production: writes a single-line JSON record to console.error so a
 * log-collector can pick it up. Rate-limited to avoid floods.
 *
 * Dev/test: also writes to console.error — dev-mode telemetry still goes
 * somewhere visible, but the dev counters and dev-toast helpers in
 * src/lib/ws.ts carry the richer feedback.
 */
export function logError(event: TelemetryEvent): void {
  if (!_shouldEmit()) return
  // Single-line JSON: makes grep / awk / log-collector parsing trivial.
  // The "telemetry" prefix is the stable key operators can filter on.
  const payload = JSON.stringify({ telemetry: 'error', ...event })
  // eslint-disable-next-line no-console
  console.error(payload)
}