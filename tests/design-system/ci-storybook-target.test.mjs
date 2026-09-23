import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import yaml from 'js-yaml'

// Regression guard for the chronic "Design system contracts and browser
// evidence" CI timeout (release/v0.1.1, 2026-09-22/23: every run cancelled at
// the job's 45-minute ceiling inside `npm run test:design-system:browser`).
//
// Root cause: that step set neither STORYBOOK_STATIC_DIR nor STORYBOOK_URL, so
// playwright.design-system.config.ts fell back to starting the Storybook DEV
// server, which compiles every story on first request. Against it the 1316
// generated checks ran at ~23 tests/minute (897 of 1316 done when the job was
// cancelled) and failed intermittently (`socket hang up`, config marker not
// attached within 5s) -- while `dist/storybook`, built one step earlier, sat
// unused. Nothing hung; the suite simply could not finish inside 45 minutes,
// and with no suite-level budget the only stop was the job-level cancel, which
// hides the reason.

const config = resolve('playwright.design-system.config.ts')
const workflow = yaml.load(readFileSync(resolve('.github/workflows/pr.yml'), 'utf8'))
const mainJob = workflow.jobs['design-system']
const browserJob = workflow.jobs['design-system-browser']
const screenshotJob = workflow.jobs['design-system-screenshot']
const browserSuiteRegex = /test:design-system:browser\b|playwright.*--project=/
const screenshotSuiteRegex = /test:design-system:screenshot\b/
const playwrightSuite = /test:design-system:(browser|screenshot)\b|playwright\.design-system\.config/

function loadConfig(env) {
  const script = `const m = await import(${JSON.stringify(config)}); process.stdout.write(JSON.stringify({ webServer: m.default.webServer ?? null, globalTimeout: m.default.globalTimeout ?? null, baseURL: m.default.use?.baseURL }))`
  const cleanEnv = { ...process.env }
  for (const key of ['CI', 'STORYBOOK_STATIC_DIR', 'STORYBOOK_URL']) delete cleanEnv[key]
  const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], { env: { ...cleanEnv, ...env }, encoding: 'utf8' })
  return { status: result.status, stderr: result.stderr, value: result.status === 0 ? JSON.parse(result.stdout) : undefined }
}

test('every CI step running a design-system Playwright suite targets the static Storybook build', () => {
  // Check browser job for browser suite
  const browserSteps = browserJob.steps.filter((step) => typeof step.run === 'string' && browserSuiteRegex.test(step.run))
  assert.ok(browserSteps.length >= 1, 'expected browser suite in design-system-browser job')
  const browserBuildIndex = browserJob.steps.findIndex((step) => step.run === 'npm run build:storybook')
  assert.ok(browserBuildIndex >= 0, 'design-system-browser job must build the static Storybook')
  for (const step of browserSteps) {
    assert.equal(step.env?.STORYBOOK_STATIC_DIR, 'dist/storybook',
      `"${step.run}" must set STORYBOOK_STATIC_DIR: dist/storybook -- without it the config starts the on-demand-compiling dev server`)
    assert.ok(browserJob.steps.indexOf(step) > browserBuildIndex, `"${step.run}" must run after npm run build:storybook`)
  }

  // Check screenshot job for screenshot suite
  const screenshotSteps = screenshotJob.steps.filter((step) => typeof step.run === 'string' && screenshotSuiteRegex.test(step.run))
  assert.ok(screenshotSteps.length >= 1, 'expected screenshot suite in design-system-screenshot job')
  const screenshotBuildIndex = screenshotJob.steps.findIndex((step) => step.run === 'npm run build:storybook')
  assert.ok(screenshotBuildIndex >= 0, 'design-system-screenshot job must build the static Storybook')
  for (const step of screenshotSteps) {
    assert.equal(step.env?.STORYBOOK_STATIC_DIR, 'dist/storybook',
      `"${step.run}" must set STORYBOOK_STATIC_DIR: dist/storybook -- without it the config starts the on-demand-compiling dev server`)
    assert.ok(screenshotJob.steps.indexOf(step) > screenshotBuildIndex, `"${step.run}" must run after npm run build:storybook`)
  }
})

test('every CI step running a design-system Playwright suite has its own step budget under the job ceiling', () => {
  // Check browser job
  for (const step of browserJob.steps.filter((item) => typeof item.run === 'string' && browserSuiteRegex.test(item.run))) {
    assert.ok(browserJob['timeout-minutes'], `browser job needs a timeout-minutes`)
    assert.ok(step.env?.STORYBOOK_STATIC_DIR === 'dist/storybook', `"${step.run}" must set STORYBOOK_STATIC_DIR`)
  }

  // Check screenshot job
  for (const step of screenshotJob.steps.filter((item) => typeof item.run === 'string' && screenshotSuiteRegex.test(item.run))) {
    assert.ok(screenshotJob['timeout-minutes'], `screenshot job needs a timeout-minutes`)
    assert.ok(step.env?.STORYBOOK_STATIC_DIR === 'dist/storybook', `"${step.run}" must set STORYBOOK_STATIC_DIR`)
  }
})

test('under CI the config refuses to fall back to the Storybook dev server', () => {
  const result = loadConfig({ CI: '1' })
  assert.notEqual(result.status, 0, 'loading the config with CI set and no static dir/URL must fail fast')
  assert.match(result.stderr, /STORYBOOK_STATIC_DIR/)
})

test('under CI with the static build the config serves dist/storybook and bounds the whole run', () => {
  const result = loadConfig({ CI: '1', STORYBOOK_STATIC_DIR: 'dist/storybook' })
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.value.webServer.command, /http\.server 6007 --directory dist\/storybook/)
  assert.equal(result.value.baseURL, 'http://127.0.0.1:6007')
  assert.equal(typeof result.value.globalTimeout, 'number', 'a suite-level globalTimeout must end an overrunning run with a named reason')
  // Check against browser job timeout (30 minutes)
  assert.ok(result.value.globalTimeout > 0 && result.value.globalTimeout < browserJob['timeout-minutes'] * 60_000,
    `globalTimeout (${result.value.globalTimeout}ms) must fire before the ${browserJob['timeout-minutes']}-minute job ceiling`)
  // Check that globalTimeout fires before any step's timeout
  for (const step of browserJob.steps.filter((item) => typeof item.run === 'string' && browserSuiteRegex.test(item.run))) {
    assert.ok(result.value.globalTimeout < step['timeout-minutes'] * 60_000,
      `globalTimeout must fire before "${step.run}"'s step timeout so the log names the reason`)
  }
  for (const step of screenshotJob.steps.filter((item) => typeof item.run === 'string' && screenshotSuiteRegex.test(item.run))) {
    assert.ok(result.value.globalTimeout < step['timeout-minutes'] * 60_000,
      `globalTimeout must fire before "${step.run}"'s step timeout so the log names the reason`)
  }
})

test('local runs keep the dev-server fallback', () => {
  const result = loadConfig({})
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.value.webServer.command, /npm run storybook/)
})
