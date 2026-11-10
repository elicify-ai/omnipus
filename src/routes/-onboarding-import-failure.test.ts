// RED: src/routes/onboarding.tsx::Route.beforeLoad falls open to the wizard on
// a dynamic-import failure, instead of propagating the failure like every
// other route hook that touches '@/lib/api' this way.
//
// Code-review finding (e822a1c27, "perf: defer API client until route use"):
// onboarding.tsx's beforeLoad now does `await import('@/lib/api')` INSIDE the
// existing try whose catch treats "couldn't determine onboarding mode" as
// fail-open — it renders the wizard, with a visible banner only for a >=500
// ApiError. A chunk-load failure of that dynamic import (e.g. a stale
// index.html referencing a JS chunk hash a fresh deploy has already replaced)
// is NOT an ApiError, so it falls through to the SAME fail-open branch:
// `{ appStateBannerMessage: null, onboardingAuthMode: undefined }` — the
// platform-mode sign-in gate (`onboardingAuthMode === 'platform' &&
// !hasStoredSession()`) is silently skipped, with no banner either. Before
// e822a1c27 this import was static, so only the GET /state HTTP call itself
// could ever reach that catch.
//
// ORACLE (stated up front, not read off the implementation): an import
// failure inside a route hook must behave like any other unhandled rejection
// in that hook — it PROPAGATES to the router's own error handling, never
// resolving to a value that looks like success. That is what
// `_app.tsx::Route.beforeLoad` already does (the import sits OUTSIDE any
// try/catch entirely) and what
// `_app/sessions.$sessionId.tsx::Route.loader` already does (the import is
// inside a try, but the catch only special-cases a CONFIRMED 404 ApiError —
// `isApiError(err) && err.status === 404` — and rethrows everything else,
// including a plain Error from a failed import). Both are pinned below as
// regression guards and are expected GREEN; the same check against
// onboarding.tsx's beforeLoad is expected RED until the import moves outside
// the fail-open try, mirroring _app.tsx.
//
// MECHANISM: `vi.doMock('@/lib/api', factory)` (non-hoisted) + `vi.resetModules()`
// force the NEXT resolution of '@/lib/api' to run the given factory. A route
// module (onboarding.tsx / _app.tsx / _app/sessions.$sessionId.tsx) is loaded
// via a plain, unmocked static `import` at the TOP of this file — before any
// test body runs, hence before any `vi.doMock`/`vi.resetModules()` call — so
// its own static dependency on '@/lib/api' (onboarding.tsx statically imports
// `probeProvider`/`completeOnboardingTransaction` from the SAME specifier)
// resolves normally and the module evaluates successfully. `vi.resetModules()`
// only clears the CACHE of already-instantiated modules for FUTURE `import()`
// calls — it does not undo code that already ran, so the `Route` object
// captured at file-load time keeps its already-evaluated closures intact.
// The dynamic `await import('@/lib/api')` INSIDE beforeLoad/loader is a fresh
// call made later, at test-invocation time, so it goes through the registry
// as reconfigured by that test's `breakApiModuleImports()` call and is the
// one call actually forced to reject.
//
// The "control" describe block below proves this mechanism truly makes a
// bare `import('@/lib/api')` reject with the exact injected error, BEFORE
// trusting it to say anything about route code.

import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (options: unknown) => options,
    redirect: (options: { to: string }) => Object.assign(new Error('redirect'), options),
  }
})

// _app/sessions.$sessionId.tsx statically imports ChatScreen, which pulls in
// the WebSocket store / full chat component tree — mocked away exactly as
// -sessions.$sessionId.test.tsx already does, purely so importing the module
// is cheap; the component is never rendered here, only its `loader`.
vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => null,
}))

// Captured ONCE, before any vi.doMock/vi.resetModules call in this file runs
// (module-eval order: static imports resolve before any test body executes).
// '@/lib/api' is NOT mocked at this point — these three route modules load
// against the real module, so each one's own static dependency on it
// (onboarding.tsx and _app/sessions.$sessionId.tsx both also statically
// import other names from '@/lib/api') succeeds normally. If any of the
// three failed to load, THIS FILE would fail to collect at all — which is
// itself the "did the baseline even work" control for module load health.
import { Route as OnboardingRoute } from './onboarding'
import { Route as AppRoute } from './_app'
import { Route as SessionRoute } from './_app/sessions.$sessionId'

const onboardingBeforeLoad = (OnboardingRoute as unknown as {
  beforeLoad: () => Promise<{ appStateBannerMessage: string | null; onboardingAuthMode?: string }>
}).beforeLoad

const appBeforeLoad = (AppRoute as unknown as {
  beforeLoad: () => Promise<unknown>
}).beforeLoad

const sessionLoader = (SessionRoute as unknown as {
  loader: (opts: { params: { sessionId: string } }) => Promise<unknown>
}).loader

// A distinctive message (not a generic "boom") so a passing assertion can
// only mean THIS injected failure was observed, not some unrelated Error.
const CHUNK_LOAD_FAILURE_MESSAGE =
  'Failed to fetch dynamically imported module: /assets/api-9f3c1a2b.js'

/** Forces the NEXT `import('@/lib/api')` anywhere (test or app code) to reject. */
function breakApiModuleImports(): void {
  vi.resetModules()
  vi.doMock('@/lib/api', async () => {
    throw new Error(CHUNK_LOAD_FAILURE_MESSAGE)
  })
}

/**
 * Vitest itself wraps any error a `vi.mock`/`vi.doMock` factory throws in its
 * own diagnostic error ("[vitest] There was an error when mocking a
 * module..."), attaching the original as `.cause` (confirmed empirically: the
 * first run of this suite showed the raw wrapper before this helper existed —
 * `.message` alone never carries the injected text). The route code under
 * test never sees that wrapper — it only ever sees whatever rejects the
 * `import()` Promise it awaited, which IS this wrapper object in the test
 * environment. Asserting on `.cause.message` targets the thing this test
 * actually injected, independent of Vitest's own wrapping.
 */
function expectRejectsWithInjectedFailure(promise: Promise<unknown>) {
  return expect(promise).rejects.toMatchObject({ cause: { message: CHUNK_LOAD_FAILURE_MESSAGE } })
}

afterEach(() => {
  // Restore '@/lib/api' to its real, unmocked resolution so a later test in
  // this file (or a differently-ordered rerun) never inherits a prior test's
  // broken registration.
  vi.doUnmock('@/lib/api')
  vi.resetModules()
})

describe('control — the fault-injection mechanism truly makes a fresh dynamic import reject', () => {
  it('rejects a bare import("@/lib/api") with the injected error once the module factory is swapped to throw', async () => {
    breakApiModuleImports()
    await expectRejectsWithInjectedFailure(import('@/lib/api'))
  })
})

describe('onboarding.tsx Route.beforeLoad — import-failure safety (RED — currently fails open)', () => {
  it('propagates a dynamic import("@/lib/api") failure instead of resolving to the fail-open wizard state', async () => {
    breakApiModuleImports()
    // Oracle: consistency with _app.tsx's safe pattern (import outside the
    // try) — an import failure must propagate, never resolve to
    // { onboardingAuthMode: undefined, appStateBannerMessage: null }, the
    // silent fail-open state that skips the platform-mode sign-in redirect.
    await expectRejectsWithInjectedFailure(onboardingBeforeLoad())
  })
})

describe('_app.tsx Route.beforeLoad — import-failure safety (regression pin, expected GREEN)', () => {
  it('propagates a dynamic import("@/lib/api") failure — the import runs OUTSIDE any try/catch', async () => {
    breakApiModuleImports()
    await expectRejectsWithInjectedFailure(appBeforeLoad())
  })
})

describe('_app/sessions.$sessionId.tsx Route.loader — import-failure safety (regression pin, expected GREEN)', () => {
  it('rethrows a dynamic import("@/lib/api") failure — the catch only special-cases a confirmed 404 ApiError', async () => {
    breakApiModuleImports()
    await expectRejectsWithInjectedFailure(
      sessionLoader({ params: { sessionId: 'sess-import-failure-904' } }),
    )
  })
})
