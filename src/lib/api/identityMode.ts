// identityMode.ts: the client-side memory of which auth mode (local vs.
// platform) the gateway reported on its most recent GET /api/v1/state.
//
// ADR-0010 WP2 — this is the hook the edition transport (http.ts,
// MODE_GATED_CSRF_EXEMPT_PATHS / currentIdentityMode) reads to decide which
// CSRF exemptions apply and, in the platform edition, which 401 redirect
// target to send an unauthenticated request to. The open-source (core)
// build has no platform package to redirect to, so it records the mode
// here and never reads it back — the read side lives entirely in the
// edition-labelled transport, not in this seam file.
//
// Split out from auth.ts (WP7 dry-run defect 2): auth.ts is a seam file that
// ships in the upstream-facing patch; http.ts is edition-labelled and is
// excluded from that patch. auth.ts must not import a symbol that only
// exists in an excluded file.

// knownIdentityMode is `identity.mode` from the most recent GET /api/v1/state
// answer, recorded by fetchAppState (auth.ts) — the one fetcher every route's
// boot path and every screen goes through, including the onboarding wizard's
// beforeLoad, which calls it directly rather than through a query.
//
// null — never a guess — until the first /state answer has arrived (a
// signed-out browser's very first paint). Callers treat null as "not local",
// i.e. fail CLOSED to the stricter platform-mode posture.
let knownIdentityMode: 'local' | 'platform' | null = null

/** rememberIdentityMode records the mode the gateway reported; auth.ts calls it. */
export function rememberIdentityMode(mode: unknown): void {
  knownIdentityMode = mode === 'local' || mode === 'platform' ? mode : null
}

/** currentIdentityMode returns the last-remembered mode, or null if none is known yet. */
export function currentIdentityMode(): 'local' | 'platform' | null {
  return knownIdentityMode
}
