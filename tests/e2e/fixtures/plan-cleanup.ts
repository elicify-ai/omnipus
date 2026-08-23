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
  // The empty `{}` first parameter is MANDATORY and is not stylistic.
  // Playwright derives each fixture's dependency list by parsing the
  // fixture function's own SOURCE TEXT (innerFixtureParameterNames in
  // node_modules/playwright/lib/common/index.js) and hard-rejects a first
  // parameter that is not an object-destructuring pattern:
  //
  //     if (firstParam[0] !== '{' || firstParam[firstParam.length-1] !== '}')
  //       onError({ message: 'First argument must use the object
  //                 destructuring pattern: ' + firstParam })
  //
  // That onError aborts loading the WHOLE spec file. A previous edit
  // rewrote these two params as `(_fixtures, use)` to satisfy ESLint's
  // `no-empty-pattern`, with a comment asserting "this is not a behavior
  // change". It was: every spec importing this `test` collected ZERO tests
  // and `playwright test` exited 1 before running a single assertion —
  // verified on Playwright 1.61.1 (the version pinned in package-lock.json)
  // by listing the conformance specs before and after.
  //
  // So the lint rule is disabled on exactly these two lines, narrowly and
  // with the reason stated, rather than the API requirement being bent to
  // suit it. Do NOT "clean this up" again without running
  // `npx playwright test --list tests/e2e/conformance-design-chat-e2e.spec.ts`
  // and confirming it still reports 3 tests rather than 0.
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
