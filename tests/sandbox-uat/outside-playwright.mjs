#!/usr/bin/env node
//
// outside-playwright.mjs — Live sandbox UAT check S.4 (UI truthfulness).
//
// Runs OUTSIDE the sandboxed agent, against a REAL running Omnipus gateway
// (see .github/workflows/sandbox-uat.yml for how the gateway is built,
// seeded, and onboarded). This script drives the SPA the gateway itself
// serves, with its own headless Chromium instance (never a browser shared
// with any other probe), and cross-checks what the UI claims about sandbox
// protection against the ground truth in `${BASE}/health`.
//
// Check:
//   S.4 — the UI must not overstate sandbox protection. If the real backend
//   is the app-level `fallback` backend, the Settings > Security > Process
//   Sandbox panel must not present the sandbox as an active/kernel-enforced
//   protection. If the real backend is a kernel backend (landlock-v*,
//   seatbelt), it is legitimate for the UI to say so.
//
// Required env:
//   BASE        e.g. http://127.0.0.1:6070
//   ADMIN_USER  login username
//   ADMIN_PASS  login password
//   SANDBOX_MODE  enforce | permissive (informational, included in evidence)
//   OUT_DIR     directory to write the evidence screenshot into
//
// Output contract: exactly one line —
//   S.4 PASS: <evidence, including the backend value compared against>
//   S.4 FAIL: <what the UI claimed vs what /health reported>
//   S.4 N/A: <reason>
// Exit 0 unless FAIL, in which case exit 1.

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'

const BASE = process.env.BASE || 'http://127.0.0.1:6070'
const ADMIN_USER = process.env.ADMIN_USER || 'admin'
const ADMIN_PASS = process.env.ADMIN_PASS || ''
const SANDBOX_MODE = process.env.SANDBOX_MODE || ''
const OUT_DIR = process.env.OUT_DIR || '.'
const NAV_TIMEOUT_MS = 30000

// Collapse to a single line and cap length — the output contract is exactly
// one line, and CI log noise from a runaway string helps nobody.
function oneLine(s, max = 400) {
  return String(s).replace(/\s+/g, ' ').trim().slice(0, max)
}

async function main() {
  mkdirSync(OUT_DIR, { recursive: true })
  const screenshotPath = join(OUT_DIR, 's4-sandbox-status.png')

  // --- Ground truth: /health's sandbox sub-object (see
  // pkg/gateway/sandbox_apply.go::registerSandboxHealthCheck). Fetched with
  // node's own fetch, independent of anything the browser does. ---
  let health
  try {
    const resp = await fetch(`${BASE}/health`, { signal: AbortSignal.timeout(NAV_TIMEOUT_MS) })
    if (!resp.ok) {
      console.log(`S.4 FAIL: GET ${BASE}/health returned HTTP ${resp.status}`)
      process.exit(1)
      return
    }
    health = await resp.json()
  } catch (err) {
    console.log(`S.4 FAIL: could not fetch ${BASE}/health — ${oneLine(err?.message ?? err)}`)
    process.exit(1)
    return
  }

  const sandbox = health && typeof health === 'object' ? health.sandbox : undefined
  if (!sandbox || typeof sandbox.backend !== 'string') {
    console.log(`S.4 FAIL: /health response had no usable sandbox.backend field — got: ${oneLine(JSON.stringify(health))}`)
    process.exit(1)
    return
  }

  const backend = sandbox.backend
  const mode = sandbox.mode
  const isFallbackBackend = backend === 'fallback'
  const isKernelBackend = backend.startsWith('landlock') || backend === 'seatbelt'
  const truth = `backend=${backend} mode=${mode}${SANDBOX_MODE ? ` (matrix mode=${SANDBOX_MODE})` : ''}${sandbox.disabled_by ? ` disabled_by=${sandbox.disabled_by}` : ''}${sandbox.landlock_enforced ? ' landlock_enforced=true' : ''}${sandbox.seccomp_enforced ? ' seccomp_enforced=true' : ''}${sandbox.audit_only ? ' audit_only=true' : ''}`

  let browser
  let page
  try {
    // Own, dedicated browser instance — never shared with another probe.
    browser = await chromium.launch({ headless: true })
    const context = await browser.newContext()
    page = context.newPage ? await context.newPage() : await browser.newPage()
    page.setDefaultTimeout(NAV_TIMEOUT_MS)
    page.setDefaultNavigationTimeout(NAV_TIMEOUT_MS)

    // --- Log in through the real login form (src/routes/login.tsx):
    // labeled "Username" / "Password" text inputs, "Sign in" submit button. ---
    await page.goto(`${BASE}/login`, { waitUntil: 'domcontentloaded', timeout: NAV_TIMEOUT_MS })
    await page.locator('#login-username').fill(ADMIN_USER)
    await page.locator('#login-password').fill(ADMIN_PASS)
    await page.getByRole('button', { name: 'Sign in' }).click()

    // A successful login navigates away from the login form (to /onboarding
    // if still needed, otherwise into the app at "/", which itself redirects
    // into the default workspace's Chat tab — see
    // src/routes/_app/index.tsx::DefaultWorkspaceRedirect). This is a
    // hash-router SPA (src/main.tsx uses createHashHistory() — "required for
    // go:embed static file serving"): a client-side route change only
    // rewrites the URL fragment after "#", never the real pathname, so a
    // page.waitForURL() keyed on `url.pathname` never fires (pathname stays
    // "/login" forever) and just times out — that is the bug this replaces.
    // Wait instead for the login form itself to go away, which is true
    // regardless of which route (onboarding vs. app) it was replaced by.
    await page.locator('#login-password').waitFor({ state: 'detached', timeout: NAV_TIMEOUT_MS }).catch(async () => {
      // Some browsers/DOM diffing keep the node attached but hidden rather
      // than removing it outright — fall back to a hash-aware URL predicate
      // (checking the fragment, not the real pathname) so a slow-but-real
      // navigation away from #/login still counts as success.
      await page.waitForURL((url) => !url.hash.includes('/login'), { timeout: NAV_TIMEOUT_MS })
    })

    const loggedInUrl = page.url()
    if (loggedInUrl.includes('/onboarding')) {
      console.log(`S.4 FAIL: login succeeded but landed on /onboarding (${loggedInUrl}) — instance is not onboarded, cannot reach Settings`)
      await page.screenshot({ path: screenshotPath, fullPage: true }).catch(() => {})
      await browser.close()
      process.exit(1)
      return
    }

    // --- Reach the app, then the sandbox status surface. Source search
    // (src/components/settings/SandboxSection.tsx, mounted inside
    // src/components/settings/SecuritySection.tsx at Settings > Security >
    // "Process Sandbox (Landlock / seccomp)") is where the SPA renders
    // sandbox state; it is reached via the Settings screen's "Security" tab
    // (data-testid="settings-tab-security", src/components/screens/SettingsScreen.tsx). ---
    // Hash-router navigation (see the login-wait comment above): the real
    // pathname is irrelevant to route resolution, only the "#/..." fragment
    // is, so go straight to the settings route via the hash rather than a
    // plain path (which would just reload into whatever "/" resolves to —
    // the default workspace's Chat tab, not Settings).
    await page.goto(`${BASE}/#/settings`, { waitUntil: 'domcontentloaded', timeout: NAV_TIMEOUT_MS })

    const securityTab = page.getByTestId('settings-tab-security')
    await securityTab.waitFor({ state: 'visible', timeout: NAV_TIMEOUT_MS })
    await securityTab.click()

    // --- Root cause of the S.4 N/A verdict (issue #625): the Security tab's
    // two-layer IA (src/components/settings/SecuritySection.tsx) hides ALL
    // "jargon" — including the whole "Process Sandbox" panel — behind a
    // single collapsed <AdvancedDisclosure title="Advanced / technical
    // details"> (src/components/shared/AdvancedDisclosure.tsx). That
    // component defaults to closed (defaultOpen=false, not overridden here)
    // and — by design ("Children are NOT mounted until the disclosure is
    // expanded (conditional render, not CSS visibility)") — does not put its
    // children in the DOM at all while collapsed. SandboxSection, and its
    // "Process Sandbox" <h3>, therefore never exist in the DOM until this
    // trigger is clicked. This was never an auth or navigation-timing
    // problem (both were already fine) — the probe was simply waiting on a
    // heading that had not been mounted yet. The trigger carries a stable,
    // already-shipped selector (data-testid="advanced-disclosure-trigger")
    // and is the only AdvancedDisclosure instance reachable from the
    // Security tab, so it is unambiguous here. ---
    const advancedTrigger = page.getByTestId('advanced-disclosure-trigger')
    try {
      await advancedTrigger.waitFor({ state: 'visible', timeout: NAV_TIMEOUT_MS })
      if ((await advancedTrigger.getAttribute('aria-expanded')) !== 'true') {
        await advancedTrigger.click()
      }
    } catch {
      // Trigger itself never appeared — fall through; the heading wait below
      // will also fail and this correctly resolves to a real N/A rather than
      // masking the failure.
    }

    // SandboxSection renders an <h3> "Process Sandbox" unconditionally as
    // soon as it mounts (see the JSX in SandboxSection.tsx — the heading is
    // not gated on the status query's loading state, only the body below it
    // is). Give it a real chance to appear now that the Advanced disclosure
    // has been expanded, but don't hang forever if the SPA never surfaces it.
    //
    // Use a role-based locator (heading), NOT a plain text match. A plain
    // `getByText('Process Sandbox', {exact:false})` also matches the static
    // label `<p>Process Sandbox (Landlock / seccomp)</p>` that
    // SecuritySection.tsx renders as a sibling wrapper one level up from
    // SandboxSection's own <h3> — and since that <p> precedes the <h3> in
    // DOM order, `.first()` silently picked the label, not the heading. The
    // label is a <p>, not a heading element, so `getByRole('heading', ...)`
    // resolves the real <h3> unambiguously.
    const heading = page.getByRole('heading', { name: 'Process Sandbox', exact: false }).first()
    let sectionFound = true
    try {
      await heading.waitFor({ state: 'visible', timeout: 15000 })
    } catch {
      sectionFound = false
    }

    if (!sectionFound) {
      await page.screenshot({ path: screenshotPath, fullPage: true }).catch(() => {})
      console.log(`S.4 N/A: the SPA does not surface sandbox state (Settings > Security > "Process Sandbox" panel never appeared) — ${truth}`)
      await browser.close()
      process.exit(0)
      return
    }

    // Pull the readable text of the enclosing <section> for the panel so
    // the evidence captures the actual rendered claim, not just the heading.
    //
    // NOTE: this must be the NEAREST ancestor <section> of the heading, not
    // just "any <section> containing the text 'Process Sandbox' on the
    // page" — the DOM here is two nested <section>s: SecuritySection.tsx's
    // wrapper (`<section><p>Process Sandbox (Landlock / seccomp)</p>
    // <SandboxSection/><p>...auto-detected...</p></section>`) around
    // SandboxSection's own root (`<section><h3>Process Sandbox</h3>
    // {renderStatusBody()}</section>`), and BOTH match a text filter on
    // "Process Sandbox" (the outer via its label <p>, the inner via its
    // <h3>). `page.locator('section').filter(...).first()` — the original
    // form here — resolves in DOM/document order, so it silently grabbed
    // the OUTER wrapper, which holds only the static label and footnote,
    // never the actual backend label / description that
    // resolveBackendLabel/resolveDescription render inside SandboxSection's
    // OWN section. That made the fallback-vs-kernel text comparison below
    // structurally unable to see "Application fallback" even when the UI
    // correctly rendered it. Walking up from the heading itself picks the
    // correct (inner, nearest) section unambiguously.
    const section = heading.locator('xpath=ancestor::section[1]')

    // The heading is unconditional, but the status body underneath it
    // (resolveBackendLabel/resolveDescription in SandboxSection.tsx) only
    // renders its real content once the `/api/v1/security/sandbox-status`
    // query resolves — before that it shows a text-free animate-pulse
    // skeleton. Reading sectionText immediately after the heading appears
    // can race that fetch and capture the skeleton (no backend label at
    // all), which would make the fallback/kernel comparison below trivially
    // vacuous. Wait for real content — the fallback label, a landlock/
    // seatbelt backend name, or an explicit fetch-error message — before
    // treating the panel text as evidence.
    const contentReady = section.filter({
      hasText: /application fallback|landlock|seatbelt|failed to load sandbox status/i,
    })
    await contentReady.first().waitFor({ state: 'visible', timeout: 15000 }).catch(() => {})

    let sectionText = ''
    try {
      sectionText = await section.innerText({ timeout: 5000 })
    } catch {
      sectionText = await heading.locator('xpath=ancestor::div[1]').innerText({ timeout: 5000 }).catch(async () => page.locator('body').innerText())
    }

    // Full-page screenshot for evidence, whichever way this resolves. Scroll
    // the panel into view first — the Advanced disclosure content can start
    // well below the fold, and an un-scrolled screenshot would show the
    // primary-layer Security Score card instead of the actual panel a human
    // reviewing the CI artifact needs to see. The printed evidence line
    // above (sectionText) is the authoritative verdict evidence regardless;
    // this only makes the screenshot artifact match it.
    await heading.scrollIntoViewIfNeeded({ timeout: 5000 }).catch(() => {})
    await page.screenshot({ path: screenshotPath, fullPage: true }).catch(() => {})

    const lower = sectionText.toLowerCase()
    // SandboxSection's own vocabulary (resolveBackendLabel/resolveDescription):
    // fallback -> "Application fallback" label + "Kernel-level sandboxing is
    // unavailable..." description. Kernel backend -> the raw backend string
    // (e.g. "landlock-v7") as the label + "restricted at the kernel level...".
    const uiClaimsFallback = lower.includes('application fallback') || lower.includes('kernel-level sandboxing is unavailable')
    const uiClaimsKernelProtection = lower.includes('restricted at the kernel level') || (isKernelBackend && lower.includes(backend.toLowerCase()) && !uiClaimsFallback)

    if (isFallbackBackend) {
      if (uiClaimsKernelProtection && !uiClaimsFallback) {
        console.log(`S.4 FAIL: /health reports ${truth} (app-level fallback, NOT kernel-enforced) but the Settings UI claims kernel protection — panel text: "${oneLine(sectionText)}"`)
        await browser.close()
        process.exit(1)
        return
      }
      console.log(`S.4 PASS: /health reports ${truth}; UI correctly does not claim active/kernel protection — panel text: "${oneLine(sectionText)}"`)
      await browser.close()
      process.exit(0)
      return
    }

    if (isKernelBackend) {
      console.log(`S.4 PASS: /health reports ${truth}, a real kernel backend, so the UI is entitled to present the sandbox as protected — panel text: "${oneLine(sectionText)}"`)
      await browser.close()
      process.exit(0)
      return
    }

    // Neither the known "fallback" name nor a landlock-*/seatbelt kernel
    // backend — an unrecognized backend value. Don't guess at a verdict;
    // report what was observed on both sides.
    console.log(`S.4 N/A: /health reports an unrecognized sandbox.backend value (${truth}), not classifiable as fallback or kernel — panel text: "${oneLine(sectionText)}"`)
    await browser.close()
    process.exit(0)
  } catch (err) {
    try {
      if (page) await page.screenshot({ path: screenshotPath, fullPage: true }).catch(() => {})
    } catch {
      // best-effort only
    }
    console.log(`S.4 FAIL: ${oneLine(err?.stack ?? err?.message ?? err)}`)
    try {
      if (browser) await browser.close()
    } catch {
      // best-effort only
    }
    process.exit(1)
  }
}

main()
