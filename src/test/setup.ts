import '@testing-library/jest-dom'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import {
  installTestIntersectionObserver,
  resetTestIntersectionObservers,
} from './intersectionObserver'

// Unmount rendered components after each test to prevent DOM bleed between tests
// when running the full vitest suite (N5 fix: 5 tests failed due to leaked DOM state).
afterEach(() => {
  cleanup()
})

// IntersectionObserver (ADR-083 EMB-065/066 — the inline-embed mount budget).
//
// jsdom ships none, and `LazyEmbedMount` FAILS OPEN without one: `mounted`
// starts `true` when the constructor is absent. Correct in a browser, but it
// meant the mount budget was never exercised anywhere in this suite — the
// four Step 6 mount files each advertise "the same lazy-mount budget
// (EMB-065)" in their header and, until this was installed, not one assertion
// in any of them ran with the gate switched on. The query fence matters most:
// it issues a REAL network request, which is the whole reason for gating.
//
// COMPATIBILITY: the default mode reports every observed element as VISIBLE,
// synchronously, on `observe()` — so an embed still mounts inside the same
// `act()` that `render()` wraps and every existing suite settles to the same
// RENDERED state it saw before. Not byte-identical behaviour: a caller's
// `onMountedChange` now fires `false` then `true` instead of once with
// `true`. That module's header states the difference in full. A test that
// wants the gate switched on calls `holdEmbedsOutOfView()` from
// `./intersectionObserver`.
//
// A suite that specifically wants the no-IntersectionObserver degradation
// path (LazyEmbedMount.test.tsx has one) still gets it the usual way, with
// `vi.stubGlobal('IntersectionObserver', undefined)`.
installTestIntersectionObserver()

// Reset after every test so one test's `holdEmbedsOutOfView()` cannot leak the
// gate into the next.
afterEach(() => {
  resetTestIntersectionObservers()
})

// Relative-URL fetch guard (Vitest "unhandled error" flake, CI shard
// "components-agents-settings").
//
// Production code (src/lib/api.ts::performRequest) intentionally calls the
// global `fetch()` with a RELATIVE path (`/api/v1/...`) — correct in a real
// browser, which resolves it against the page origin. jsdom's test
// environment has no such origin for the global `fetch`: it's Node's real
// (undici) implementation, which throws `TypeError: Failed to parse URL
// from /api/v1/...` for a relative input. Some mounted components fire a
// fetch-backed hook unconditionally on mount (e.g. useCliDetect, used by
// AgentProfile/Step1Identity for subagent_3p agents) without every test
// mocking it — that's fine, application code already catches the failure
// (performRequest's try/catch around `await fetch(...)` converts it to a
// handled ApiError). The problem is TIMING: undici's real URL-parsing path
// takes several internal microtask hops before the rejection settles, and
// under CI's resource contention that can occasionally lose the race with
// Vitest's unhandled-rejection detection — flagging a rejection that
// application code DOES eventually catch, failing the whole shard.
//
// Fix (test-only, no production behavior change): intercept fetch() calls
// whose input can't be parsed as an absolute URL and reject them via a
// single, already-settled `Promise.reject` instead of delegating to
// undici. This preserves the exact same error type/message any test or
// catch handler would have seen, it just collapses the promise chain to
// one hop so application-level catches always attach before Vitest's
// unhandled-rejection check runs. Absolute-URL fetches (explicit mocks,
// `vi.stubGlobal('fetch', ...)` in individual test files, etc.) are
// untouched — this only ever intercepts calls that would have failed with
// this exact error anyway.
const nativeFetch = globalThis.fetch

function isUnparsableRelativeInput(input: RequestInfo | URL): input is string {
  if (typeof input !== 'string') return false
  try {
    new URL(input)
    return false
  } catch {
    return true
  }
}

globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
  if (isUnparsableRelativeInput(input)) {
    return Promise.reject(new TypeError(`Failed to parse URL from ${input}`))
  }
  return nativeFetch(input, init)
}) as typeof fetch
