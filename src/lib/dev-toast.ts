// dev-toast.ts — shared dev-mode toast helper used by ws.ts and api.ts.
//
// Emits a warning toast via useUiStore in DEV builds only.
// Throttled: at most one toast per key per THROTTLE_MS to avoid flooding the
// UI when a burst of validation errors arrives. The key can be any string;
// callers typically pass the frame type (ws.ts) or `${method}:${path}` (api.ts).
//
// Not a hook — safe to call from module-scope (outside React components).
// Uses dynamic require() so the Zustand store is resolved lazily, avoiding
// circular-dependency issues at module initialisation time.

const _lastToastAt: Record<string, number> = {}
const THROTTLE_MS = 1000

/**
 * Emit a dev-mode toast throttled to one per `key` per second.
 * No-ops in production builds (import.meta.env.DEV is dead-code-eliminated).
 */
export function maybeDevToast(
  message: string,
  key: string,
  variant: 'warning' | 'error' = 'warning',
): void {
  if (!import.meta.env.DEV) return
  const now = Date.now()
  if (now - (_lastToastAt[key] ?? 0) < THROTTLE_MS) return
  _lastToastAt[key] = now
  try {
    // Dynamic require avoids circular-dep issues at module init time.
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const { useUiStore } = require('@/store/ui') as {
      useUiStore: { getState: () => { addToast: (t: { message: string; variant: 'warning' | 'error' }) => void } }
    }
    useUiStore.getState().addToast({ message, variant })
  } catch {
    console.warn('[dev-toast]', message)
  }
}
