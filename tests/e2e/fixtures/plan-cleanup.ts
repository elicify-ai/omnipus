// Best-effort background-work cleanup for Playwright specs that create
// Plan/Task entities directly via REST (currently: conformance-design-e2e.spec.ts).
//
// THE SYSTEMIC PROBLEM this fixture exists to fix: playwright.config.ts sets
// `retries: 3` in CI with `workers: 1` — a single gateway process serves every
// attempt of every test in a spec file. When an attempt fails (assertion
// timeout, flaky LLM turn, etc.), Playwright retries the TEST, but nothing
// ever stopped the Plan/Task that attempt created. A Plan left `running`
// keeps re-arming its supervision wake (pkg/agent/plan_engine.go) roughly
// every ~30s until its round budget is exhausted or a correction lands, and a
// Task left `in_progress` keeps its worker turn alive — both keep consuming
// real LLM calls and CPU on the SAME shared gateway the next retry attempt
// (and every other test after it) depends on.
//
// Confirmed directly in a live CI shard's gateway log: THREE live
// "t3 conformance plan" instances and TWO live "t2 conformance plan"
// instances coexisting simultaneously after repeated retries — load
// compounding across attempts, not resetting between them.
//
// FIX: every test that creates a Plan or standalone Task pushes its id into
// `createdPlanIds` / `createdTaskIds`; the auto fixture below stops each one
// on teardown. Because it is a fixture (teardown code after `await use()`),
// not a manual call at the end of the test body, it runs after EVERY
// attempt — pass, fail, or timeout — not just a clean run.
//
// VERIFIED (not assumed) against the installed Playwright 1.61.1 runner:
// node_modules/playwright/lib/worker/workerProcessEntry.js's per-test loop
// sets `shouldRunAfterEachHooks = true` immediately once the beforeEach phase
// begins, then runs the test function inside an IIFE whose rejection is
// swallowed (`.catch(() => {})`) — the "After Hooks" step (afterEach hooks,
// then `_fixtureRunner.teardownScope`, i.e. this fixture's teardown) always
// runs next, regardless of whether the test function threw, and regardless
// of whether this is the final attempt or one that will be retried. This
// mirrors the pre-existing `cancelOnTeardown` auto fixture in
// ./console-errors.ts, which relies on the exact same guarantee to click the
// chat's Stop button after every attempt.
//
// NON-VERDICT-AFFECTING (hard requirement): every stop call is best-effort.
// A cleanup failure is only ever logged — it can never throw out of the
// fixture and can never turn a passing test red, nor (since it runs strictly
// after the test body has already produced its result) turn a failing test
// green. POST /plans/{id}/stop and POST /tasks/{id}/stop are also naturally
// safe to call on an already-terminal target: both reject a non-running plan
// / non-in-progress task with a 400 (rest_plans.go's handlePlanStop,
// rest_tasks.go's handleTaskStop) rather than erroring — calling stop on a
// plan/task a test already drove to done/failed itself is an expected,
// harmless no-op, not a failure.
import { test as base } from './console-errors'

export { expect } from '@playwright/test'

// Mirrors the spec file's own BASE_URL convention exactly (absolute URL,
// not a relative path resolved against playwright.config.ts's `use.baseURL`)
// so cleanup targets the same gateway the test itself talked to regardless
// of how Playwright's own baseURL resolution behaves for `page.request`.
const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

async function getCsrfHeader(page: {
  context: () => { cookies: () => Promise<Array<{ name: string; value: string }>> }
}): Promise<Record<string, string>> {
  try {
    const cookies = await page.context().cookies()
    const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value
    return csrf ? { 'X-Csrf-Token': csrf } : {}
  } catch {
    // No cookie / context already torn down — proceed without the header;
    // the stop call will 401 at worst, which is caught and logged below.
    return {}
  }
}

export const test = base.extend<{
  createdPlanIds: string[]
  createdTaskIds: string[]
  stopBackgroundWorkOnTeardown: void
}>({
  // Test-scoped, freshly empty per attempt — a test pushes into this array
  // as it creates each Plan; the array itself is never shared across tests
  // or across retries of the same test (each attempt gets its own).
  // The first (fixtures) argument MUST be a destructuring pattern. Playwright
  // parses this signature statically to work out which fixtures each one
  // depends on, and rejects a plain identifier at load time with
  // "First argument must use the object destructuring pattern" — which fails
  // the whole spec file before a single test runs. `{}` is therefore load-
  // bearing, not style; the eslint no-empty-pattern rule is disabled rather
  // than satisfied. (Regression: 03d27695 replaced it with `_fixtures` on the
  // stated belief that it was "not a behavior change"; it took the entire
  // llm-conformance e2e shard down.)
  // eslint-disable-next-line no-empty-pattern
  createdPlanIds: async ({}, use) => {
    const ids: string[] = []
    await use(ids)
  },
  // eslint-disable-next-line no-empty-pattern
  createdTaskIds: async ({}, use) => {
    const ids: string[] = []
    await use(ids)
  },
  stopBackgroundWorkOnTeardown: [
    async ({ page, createdPlanIds, createdTaskIds }, use) => {
      await use()

      const headers = await getCsrfHeader(page)

      for (const id of createdPlanIds) {
        try {
          await page.request.post(`${BASE_URL}/api/v1/plans/${id}/stop`, { headers })
        } catch (err) {
          // Best-effort: log only. Never throw here — a cleanup failure
          // must not alter this test's already-decided verdict.
          console.warn(`[plan-cleanup] best-effort POST /plans/${id}/stop failed: ${String(err)}`)
        }
      }
      for (const id of createdTaskIds) {
        try {
          await page.request.post(`${BASE_URL}/api/v1/tasks/${id}/stop`, { headers })
        } catch (err) {
          console.warn(`[plan-cleanup] best-effort POST /tasks/${id}/stop failed: ${String(err)}`)
        }
      }
    },
    { auto: true },
  ],
})
