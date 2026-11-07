// Omnipus — a stub external-CLI worker for real-gateway e2e specs.
//
// A `subagent_3p` agent's executor runs the program path saved for its CLI in
// Settings → Third-party agents directly (os/exec with an absolute path, no
// $PATH lookup) and hands it the task prompt on stdin
// (pkg/agent/runner/driver_claude.go Run: `cmd.Stdin = strings.NewReader(opts.Input)`,
// fed from pkg/agent/external_dispatch.go `Input: task`). With `cli:
// 'claude-code'` the stub only has to print ONE stream-json line —
// `{"type":"result","subtype":"success","result":"<text>"}` — and the text
// reaches the task run verbatim (driver_claude.go parseResultEvent →
// external_dispatch.go drainExternalRun). No LLM runs on the worker side, so
// what the worker "says" is exactly what the spec chose. The CLI-version
// preflight is non-fatal, but the stub answers `--version` anyway.
//
// The stub prints one of two texts, chosen per try within one task run:
//   - STUB_FIRST_TRY_TEXT on the run's first try;
//   - STUB_LATER_TRY_TEXT on every later try in the same run (the first-try
//     text again when it is unset).
// A later try is recognised by the section the task run appends to an external
// CLI's prompt after a try that did not finish the task (RUN_FEEDBACK_HEADER,
// pkg/agent/task_run_loop.go executeTaskRun): an external CLI keeps no
// conversation between invocations, so the run re-sends the whole task with
// that section on every later try. Reading it from the prompt keeps the stub
// free of side effects — it writes nothing into the worker's working
// directory, where the Judge would see it as work the worker did.

import * as fs from 'fs'
import * as os from 'os'
import * as path from 'path'
import type { Page } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

/**
 * The heading pkg/agent/task_run_loop.go's executeTaskRun puts before the
 * feedback it appends to an external CLI's prompt on every try after the
 * first in one run. If the product renames it, a stub with a later-try text
 * stops switching and its spec fails on the flow assertions that follow.
 */
export const RUN_FEEDBACK_HEADER = '## Feedback from your previous try in this run:'

/** The two texts a stub worker prints (see the file header). */
export interface StubCliTexts {
  firstTry: string
  laterTry?: string
}

/**
 * The `executor` object of a POST /api/v1/agents body for a `subagent_3p`
 * stub worker. No `cli_path`: the gateway refuses it on every agent
 * create/update with 400 "cli_path cannot be set on an agent" (r17 §3.11,
 * pkg/thirdpartycli/executor_settings.go CLIPathRefusalText) — the program
 * path is Settings-owned and reaches the gateway through saveStubCliProgram.
 */
export interface StubCliExecutor {
  cli: 'claude-code'
  env_overrides: Record<string, string>
}

let stubCliPath: string | null = null

/**
 * Writes the stub script once per test process, to a scratch path on the
 * machine the gateway runs on (CI and local runs both start the gateway on the
 * host that runs `npx playwright test`), and returns its absolute path.
 *
 * The script lives in its own private folder, never directly in os.tmpdir():
 * the Settings save refuses a program whose containing folder is world-
 * writable (pkg/thirdpartycli/path.go finalFileCheck → writable_by_others,
 * /tmp is exactly that), and mkdtempSync mints a 0700 folder that also stays
 * outside $OMNIPUS_HOME and outside sandbox.allowed_paths (agent_writable_
 * location). The shebang embeds process.execPath — the node binary already
 * running this test — rather than relying on `#!/usr/bin/env node` resolving
 * inside the gateway's spawned child.
 */
export function stubCliScriptPath(): string {
  if (stubCliPath && fs.existsSync(stubCliPath)) {
    return stubCliPath
  }
  const stubDir = fs.mkdtempSync(path.join(os.tmpdir(), 'omnipus-e2e-stub-cli-'))
  const scriptPath = path.join(stubDir, `stub-cli-${process.pid}.js`)
  const script = [
    `#!${process.execPath}`,
    "'use strict';",
    '// Omnipus e2e stub external CLI -- see tests/e2e/fixtures/stub-external-cli.ts.',
    "if (process.argv.slice(2).includes('--version')) {",
    "  process.stdout.write('2.0.0 (omnipus e2e stub)\\n');",
    '  process.exit(0);',
    '}',
    "let input = '';",
    "try { input = require('fs').readFileSync(0, 'utf8'); } catch (_) { input = ''; }",
    "const firstTry = process.env.STUB_FIRST_TRY_TEXT || '';",
    `const laterTry = input.includes(${JSON.stringify(RUN_FEEDBACK_HEADER)});`,
    'const text = laterTry && process.env.STUB_LATER_TRY_TEXT !== undefined',
    '  ? process.env.STUB_LATER_TRY_TEXT',
    '  : firstTry;',
    'process.stdout.write(JSON.stringify({',
    "  type: 'result',",
    "  subtype: 'success',",
    '  result: text,',
    "  session_id: 'omnipus-e2e-stub-session',",
    '}) + "\\n");',
    'process.exit(0);',
    '',
  ].join('\n')
  fs.writeFileSync(scriptPath, script, { mode: 0o755 })
  fs.chmodSync(scriptPath, 0o755)
  stubCliPath = scriptPath
  return scriptPath
}

/**
 * Builds the `executor` for a stub worker that prints `texts`. Call
 * saveStubCliProgram first: with no saved Settings path the agent is created
 * fine (201) but reports "no program path is set for Claude Code" and cannot
 * run.
 */
export function stubCliExecutor(texts: StubCliTexts): StubCliExecutor {
  const envOverrides: Record<string, string> = { STUB_FIRST_TRY_TEXT: texts.firstTry }
  if (texts.laterTry !== undefined) {
    envOverrides.STUB_LATER_TRY_TEXT = texts.laterTry
  }
  return { cli: 'claude-code', env_overrides: envOverrides }
}

/**
 * $OMNIPUS_HOME, resolved exactly the way global-setup.ts's
 * validateOrSeedGatewayConfig resolves it: OMNIPUS_HOME when the environment
 * names one (the CI e2e job exports OMNIPUS_HOME=/tmp/omnipus-e2e at job
 * level, .github/workflows/pr.yml), else $HOME/.omnipus, else the /tmp
 * fallback. The gateway this spec talks to runs on the SAME host as the test
 * process (stubCliScriptPath's comment), so this local path IS the gateway's
 * config directory.
 */
function gatewayHomeDir(): string {
  return (
    process.env.OMNIPUS_HOME ||
    (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e')
  )
}

/** The two keys of config.json this fixture reads and (only ever) flips. */
interface GatewayConfigDoc {
  gateway?: { dev_mode_bypass?: boolean }
}

/**
 * Atomically set gateway.dev_mode_bypass in the gateway's config.json,
 * preserving every other key on disk. Atomic temp-file + rename, mirroring
 * the gateway's own fileutil.WriteFileAtomic convention (CLAUDE.md "Storage"):
 * the gateway's file watcher and the POST /reload below must never observe a
 * half-written config. Re-read on every call — between flip and restore the
 * PUT itself rewrites config.json (it persists the saved CLI rows), so the
 * restore must mutate the file as it exists THEN, never a stale copy.
 */
async function setDevModeBypass(configPath: string, value: boolean): Promise<void> {
  const raw = await fs.promises.readFile(configPath, 'utf-8')
  const doc = JSON.parse(raw) as GatewayConfigDoc
  if (!doc.gateway || typeof doc.gateway !== 'object') doc.gateway = {}
  doc.gateway.dev_mode_bypass = value
  const out = JSON.stringify(doc, null, 2) + '\n'
  const tmp = `${configPath}.e2e-stub-tmp-${process.pid}`
  await fs.promises.writeFile(tmp, out, { mode: 0o600 })
  await fs.promises.rename(tmp, configPath)
}

/**
 * POST /reload — the health server's unauthenticated reload hook
 * (pkg/health/server.go reloadHandler). It runs the gateway's reload
 * function synchronously: 200 means the on-disk config was re-read and the
 * in-memory config swapped; a non-2xx carries the reload error in the body.
 * The same endpoint retention.spec.ts uses for its bypass flip.
 */
async function postReload(page: Page): Promise<void> {
  const res = await page.request.post(`${BASE_URL}/reload`, { failOnStatusCode: false })
  const raw = await res.text()
  if (!res.ok()) {
    throw new Error(`saveStubCliProgram: POST /reload failed ${res.status()}: ${raw}`)
  }
}

/**
 * Poll GET /api/v1/config until the LIVE config reports the wanted
 * dev_mode_bypass value. POST /reload only QUEUES the reload (newReloadTrigger
 * signals manualReloadChan and returns; gateway_reload.go's runReloadCycle
 * applies it asynchronously), so the poll is what actually waits for the
 * swap — never trusting the 200 alone. The GET is auth'd by the same session
 * cookie: under bypass it authenticates as the synthetic bypass identity,
 * after the flip as the signed-in person — both read the same live config.
 *
 * `gateway.dev_mode_bypass` carries `omitempty` on the wire (config.go): the
 * key is ABSENT whenever the live value is false, so an absent key reads as
 * false. Budget is 20s, not 5: the reload cycle restarts live services and
 * can legitimately take seconds on a loaded CI host.
 */
async function pollDevModeBypass(page: Page, want: boolean): Promise<void> {
  const deadline = Date.now() + 20_000
  let lastSeen = '(no probe ran)'
  while (Date.now() < deadline) {
    try {
      const res = await page.request.get(`${BASE_URL}/api/v1/config`, { failOnStatusCode: false })
      if (res.ok()) {
        const cfg = (await res.json()) as GatewayConfigDoc
        // omitempty: an absent key IS false on this wire.
        const seen = cfg?.gateway?.dev_mode_bypass === true
        if (seen === want) return
        lastSeen = String(seen)
      } else {
        lastSeen = `HTTP ${res.status()}`
      }
    } catch (err) {
      lastSeen = String(err)
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(
    `saveStubCliProgram: the gateway's live config never reported ` +
      `dev_mode_bypass=${want} within 20s (last observation: ${lastSeen}). ` +
      'The POST /reload did not take effect; refusing to continue on a gateway ' +
      'in an unknown auth mode.',
  )
}

/**
 * Run `body` while the gateway's dev_mode_bypass is OFF, restoring the
 * previous value afterwards.
 *
 * WHY this exists (#1661): the third-party-agents PUT is person-session-only
 * (middleware.RequirePersonSession → requireSessionCookieAuth), but every e2e
 * gateway seeds `gateway.dev_mode_bypass: true` (.github/workflows/pr.yml
 * "Seed gateway config", deploy/ci-worker/runci.sh, global-setup.ts's own
 * minimal config). On a bypass gateway the person session is UNREACHABLE,
 * twice over: checkBearerAuth (pkg/gateway/auth.go) answers every request
 * that carries no bearer header with the synthetic `_dev_bypass` identity
 * BEFORE the omnipus-session cookie fallback ever runs, and
 * middleware.RequirePersonSession refuses EVERY request on a bypass gateway
 * outright ("dev_mode_bypass_on" — bypass means nobody was authenticated).
 * So the 403 person_session_required this PUT returned in CI was not a
 * missing login: the page DID carry the real person cookie — the gateway was
 * just never allowed to read it. Flipping bypass off is the only way any
 * request can authenticate as a person, and it is the suite's established
 * precedent (retention.spec.ts flips bypass off for its
 * RequireNotBypass-gated sweep endpoint, then restores).
 *
 * The window is as small as possible and restored in a `finally`, because
 * bypass-off changes auth for every other request on this gateway: the rest
 * of the shard must keep running exactly as before. Serial execution
 * (workers: 1, one spec per shard gateway) means no concurrent test can
 * observe the intermediate state. A restore failure never masks the body's
 * own error — both are reported.
 */
async function runWithBypassOffForPersonSession(page: Page, body: () => Promise<void>): Promise<void> {
  const configPath = path.join(gatewayHomeDir(), 'config.json')
  let raw: string
  try {
    raw = await fs.promises.readFile(configPath, 'utf-8')
  } catch (err) {
    throw new Error(
      `saveStubCliProgram: cannot read the gateway config at ${configPath} (${String(err)}). ` +
        'The third-party-agents PUT needs a person session, which a dev_mode_bypass ' +
        'gateway can never carry; this fixture flips the flag through the config file, ' +
        'so the gateway home must be on this machine and hold a config.json (the CI e2e ' +
        'job and runci.sh both seed one).',
      { cause: err },
    )
  }
  let bypassWasOn: boolean
  try {
    bypassWasOn = (JSON.parse(raw) as GatewayConfigDoc)?.gateway?.dev_mode_bypass === true
  } catch (err) {
    throw new Error(`saveStubCliProgram: config.json at ${configPath} does not parse (${String(err)}).`, {
      cause: err,
    })
  }
  if (!bypassWasOn) {
    // Already off (e.g. a locally started gateway) — the person-session path
    // works as-is; no flip, no reload, nothing to restore.
    await body()
    return
  }
  await setDevModeBypass(configPath, false)
  await postReload(page)
  await pollDevModeBypass(page, false)
  let bodyErr: Error | null = null
  try {
    await body()
  } catch (err) {
    bodyErr = err as Error
  }
  try {
    await setDevModeBypass(configPath, true)
    await postReload(page)
    await pollDevModeBypass(page, true)
  } catch (restoreErr) {
    if (bodyErr) {
      throw new Error(
        `${bodyErr.message} (AND restoring dev_mode_bypass afterwards also failed: ${String(restoreErr)})`,
        { cause: restoreErr },
      )
    }
    throw restoreErr
  }
  if (bodyErr) throw bodyErr
}

/**
 * Save the stub as the person-chosen program for claude-code in Settings →
 * Third-party agents — the only sanctioned way the gateway learns an external
 * CLI's program path since the r17 contract (commit 27384787e): every agent
 * create/update carrying `executor.cli_path` is refused 400 "cli_path cannot
 * be set on an agent" (pkg/gateway/rest_agents_create_test.go
 * TestCreateAgent_Subagent3p_CliPathRefused).
 *
 * The PUT route is person-session-only (middleware.RequirePersonSession plus
 * the session-cookie check — an agent or tool bearer token gets 403, r17
 * §3.3 row 9.23). The page carries the real omnipus-session cookie the
 * shared login fixture minted (global-setup.ts REST login; fixtures/login.ts),
 * and page.request shares that browser context's cookies. That cookie only
 * becomes the request's identity once the gateway is OUT of dev_mode_bypass
 * (see runWithBypassOffForPersonSession for why, and for the flip/restore
 * that brackets the PUT). The fixture deliberately does NOT re-login: the
 * session slot is single per user (gateway-process.ts restart()'s doc), so a
 * second login would invalidate the shared storageState cookie every other
 * test in the shard holds.
 *
 * No caching: the PUT replaces the whole section, is idempotent, and
 * re-sending it before every stub-worker create keeps the saved path correct
 * even if the gateway was (re)booted after an earlier save.
 */
export async function saveStubCliProgram(page: Page): Promise<void> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  if (!csrf) {
    throw new Error(
      'saveStubCliProgram: no CSRF cookie in the browser context — the person ' +
        'login (global-setup.ts via fixtures/login.ts) must have seeded one before ' +
        'a stub CLI worker can be set up.',
    )
  }
  const stubPath = stubCliScriptPath()
  await runWithBypassOffForPersonSession(page, async () => {
    const res = await page.request.fetch(`${BASE_URL}/api/v1/settings/third-party-agents`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-Csrf-Token': csrf.value },
      data: JSON.stringify({ clis: { 'claude-code': { path: stubPath, preset_args: '' } } }),
    })
    const raw = await res.text()
    if (!res.ok()) {
      throw new Error(`saveStubCliProgram: PUT /api/v1/settings/third-party-agents failed ${res.status()}: ${raw}`)
    }
    // The response is the saved view (all three CLI rows): assert the claude-code
    // row carries exactly the stub path — a silent mismatch here would surface
    // far away, as a worker that cannot run ("no program path is set").
    let view: { clis?: Record<string, { path?: string }> } | undefined
    try {
      view = JSON.parse(raw) as { clis?: Record<string, { path?: string }> }
    } catch {
      view = undefined
    }
    const savedPath = view?.clis?.['claude-code']?.path
    if (savedPath !== stubPath) {
      throw new Error(
        'saveStubCliProgram: the saved Settings view does not carry the stub path ' +
          `(got ${JSON.stringify(savedPath) ?? '(unparseable response)'}, want ${stubPath}) ` +
          '— the stub worker would be created unable to run.',
      )
    }
  })
}

/**
 * A completion marker that reports failure. A task run reads it from an
 * external-CLI worker as a blocked claim and ends the task Failed
 * "Blocked: <summary>" at once — no Judge call, no task attempt used, no
 * restart (ADR-043 §8, ADR-084 §11; pkg/agent/task_run_loop.go resolveRunClaim).
 */
export function blockedMarker(summary: string): string {
  return `TASK_STATUS: failure\nTASK_SUMMARY: ${summary}`
}
