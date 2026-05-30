// dev-toast.ts — shared dev-mode toast helper used by ws.ts and api.ts.
//
// Emits a warning toast via useUiStore in DEV builds only.
// Throttled: at most one toast per key per THROTTLE_MS to avoid flooding the
// UI when a burst of validation errors arrives. The key can be any string;
// callers typically pass the frame type (ws.ts) or `${method}:${path}` (api.ts).
//
// Not a hook — safe to call from module-scope (outside React components).
// Uses dynamic import() so the Zustand store is resolved lazily, keeping the
// toast store out of the api-module init path for dead-code elimination.

// Map (rather than a plain object) so an attacker-controlled `key` like
// "__proto__" or "constructor" cannot pollute Object.prototype via the
// `_lastToastAt[key] = now` assignment below. The risk window is small —
// the function is gated on import.meta.env.DEV — but a Map closes the
// CodeQL js/prototype-polluting-assignment finding regardless.
const _lastToastAt = new Map<string, number>()
const THROTTLE_MS = 1000

/**
 * Emit a dev-mode toast throttled to one per `key` per second.
 * No-ops in production builds (import.meta.env.DEV is dead-code-eliminated).
 */
export async function maybeDevToast(
  message: string,
  key: string,
  variant: 'warning' | 'error' = 'warning',
): Promise<void> {
  if (!import.meta.env.DEV) return
  const now = Date.now()
  if (now - (_lastToastAt.get(key) ?? 0) < THROTTLE_MS) return
  _lastToastAt.set(key, now)
  try {
    const { useUiStore } = await import('@/store/ui') as {
      useUiStore: { getState: () => { addToast: (t: { message: string; variant: 'warning' | 'error' }) => void } }
    }
    useUiStore.getState().addToast({ message, variant })
  } catch {
    console.warn('[dev-toast]', message)
  }
}
