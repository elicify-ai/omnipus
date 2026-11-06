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
 * Save the stub as the person-chosen program for claude-code in Settings →
 * Third-party agents — the only sanctioned way the gateway learns an external
 * CLI's program path since the r17 contract (commit 27384787e): every agent
 * create/update carrying `executor.cli_path` is refused 400 "cli_path cannot
 * be set on an agent" (pkg/gateway/rest_agents_create_test.go
 * TestCreateAgent_Subagent3p_CliPathRefused).
 *
 * The PUT route is person-session-only (middleware.RequirePersonSession plus
 * the session-cookie check — an agent or tool bearer token gets 403, r17
 * §3.3 row 9.23). The page here carries the real omnipus-session cookie the
 * shared login fixture minted (global-setup.ts REST login; fixtures/login.ts),
 * and page.request shares that browser context's cookies, so the same session
 * authorises this call. No caching: the PUT replaces the whole section, is
 * idempotent, and re-sending it before every stub-worker create keeps the
 * saved path correct even if the gateway was (re)booted after an earlier save.
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
