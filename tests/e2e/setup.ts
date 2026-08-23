/**
 * setup.ts — Shared gateway lifecycle helpers for E2E tests
 *
 * Exports startGateway() / stopGateway() used by test.beforeAll / test.afterAll.
 *
 * Design:
 *   - Starts the Omnipus binary (OMNIPUS_BINARY env or /tmp/omnipus-ci)
 *   - Creates a throwaway OMNIPUS_HOME with mkdtempSync (unique per call)
 *   - Waits for /health to return 200 before resolving
 *   - Onboards a first admin via POST /api/v1/onboarding/complete
 *   - Returns the admin credentials so callers can auth-seed their own sessions
 *
 * See CLAUDE.md "SPA Embed Pipeline" and E2E "Known blockers and workarounds".
 */

import { spawn, type ChildProcess } from 'child_process';
import { createServer } from 'net';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';

// Default binary used when OMNIPUS_BINARY is not set.
export const DEFAULT_OMNIPUS_BINARY = '/tmp/omnipus-ci';

/**
 * Bind an ephemeral port on 0.0.0.0, read the OS-assigned port, then close.
 * Use this in beforeAll to avoid hardcoded port collisions across CI jobs.
 */
export async function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.listen(0, () => {
      const addr = srv.address();
      if (addr && typeof addr === 'object') {
        const port = addr.port;
        srv.close(() => resolve(port));
      } else {
        srv.close();
        reject(new Error('could not bind ephemeral port'));
      }
    });
    srv.on('error', reject);
  });
}

export interface GatewayHandle {
  process: ChildProcess;
  homeDir: string;
  port: number;
  baseURL: string;
  adminUsername: string;
  adminPassword: string;
}

export interface StartGatewayOptions {
  port: number;
  binary?: string;
  adminUsername?: string;
  adminPassword?: string;
  /** Extra gateway args (e.g. '--sandbox=off') */
  extraArgs?: string[];
}

/**
 * Start a gateway instance on the given port.
 * Returns a GatewayHandle with process info + onboarded admin credentials.
 *
 * Usage:
 *   let handle: GatewayHandle;
 *   test.beforeAll(async () => { handle = await startGateway({ port: 5551 }); });
 *   test.afterAll(async () => { await stopGateway(handle); });
 */
export async function startGateway(opts: StartGatewayOptions): Promise<GatewayHandle> {
  const binary = opts.binary ?? process.env.OMNIPUS_BINARY ?? DEFAULT_OMNIPUS_BINARY;
  const adminUsername = opts.adminUsername ?? 'admin';
  const adminPassword = opts.adminPassword ?? 'admin1234';

  // Verify binary exists.
  if (!fs.existsSync(binary)) {
    throw new Error(
      `BLOCKED: Gateway binary not found at ${binary}.\n` +
        'Build it with:\n' +
        '  CGO_ENABLED=0 go build -o /tmp/omnipus-ci ./cmd/omnipus/',
    );
  }

  // Create a throwaway OMNIPUS_HOME — unique per startGateway() call.
  const homeDir = fs.mkdtempSync(path.join(os.tmpdir(), `omnipus-e2e-${opts.port}-`));

  // Pre-write config.json with the port.
  fs.writeFileSync(
    path.join(homeDir, 'config.json'),
    JSON.stringify({ version: 1, gateway: { port: opts.port } }, null, 2),
    { mode: 0o600 },
  );

  const args = ['gateway', '--allow-empty', ...(opts.extraArgs ?? ['--sandbox=off'])];

  const proc = await new Promise<ChildProcess>((resolve, reject) => {
    const child = spawn(binary, args, {
      env: {
        ...process.env,
        OMNIPUS_HOME: homeDir,
        OMNIPUS_BEARER_TOKEN: '',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    const timeout = setTimeout(() => {
      child.kill();
      reject(new Error(`Gateway did not start within 30s on port ${opts.port}`));
    }, 30_000);

    let output = '';
    const onData = (chunk: Buffer) => {
      output += chunk.toString();
      // Match any "listening on :<PORT>" pattern the gateway logs.
      if (
        output.includes(`localhost:${opts.port}`) ||
        output.includes(`0.0.0.0:${opts.port}`) ||
        output.includes(`:${opts.port}`)
      ) {
        clearTimeout(timeout);
        resolve(child);
      }
    };
    child.stdout?.on('data', onData);
    child.stderr?.on('data', onData);

    child.on('exit', (code) => {
      clearTimeout(timeout);
      if (code !== null && code !== 0) {
        reject(new Error(`Gateway exited with code ${code}.\nOutput:\n${output}`));
      }
    });
    child.on('error', (err) => {
      clearTimeout(timeout);
      reject(err);
    });
  });

  const baseURL = `http://localhost:${opts.port}`;

  // Wait for /health to return 200. Pass the child process so a crash that
  // happens AFTER the "listening" log line (but before /health ever answers)
  // is reported immediately as the real exit, not after paying the full
  // polling budget — the TS-side equivalent of the Go harness checking
  // gw.bootErr every iteration (see pollUntilHealthy's checkFatalError).
  await waitForHealth(baseURL, 15_000, proc);

  // Onboard the first admin.
  await onboardAdmin(baseURL, adminUsername, adminPassword);

  return { process: proc, homeDir, port: opts.port, baseURL, adminUsername, adminPassword };
}

/**
 * Gracefully stop a gateway started by startGateway().
 * Always attempts cleanup of the temp OMNIPUS_HOME.
 */
export async function stopGateway(handle: GatewayHandle | null): Promise<void> {
  if (!handle) return;
  if (handle.process && !handle.process.killed) {
    handle.process.kill('SIGTERM');
  }
  if (handle.homeDir && fs.existsSync(handle.homeDir)) {
    try {
      fs.rmSync(handle.homeDir, { recursive: true, force: true });
    } catch {
      // Best-effort cleanup; workspace dirs created by the gateway may be non-empty.
    }
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Readiness polling — extracted so the decision logic is unit-testable
// without booting a real gateway. See setup.poll.selfcheck.ts (named to avoid
// Playwright's default testMatch, "**/*.@(spec|test).?(c|m)[jt]s?(x)" —
// picking it up as a Playwright test file would misfire since it uses
// node:test, not @playwright/test; run it directly with
// `node --experimental-strip-types --test tests/e2e/setup.poll.selfcheck.ts`).
//
// This mirrors pkg/agent/testutil/gateway_harness.go's pollUntilReady (Go
// twin of this fix) so both harnesses share the same semantics:
//
//   1. Failure requires `consecutiveFailThreshold` CONSECUTIVE failed probes,
//      not elapsed wall-clock time. The old design (`while (Date.now() <
//      deadline)`) trusted wall-clock time as the failure signal, which
//      breaks down under a host freeze (CI scheduling stall, IO stall): the
//      entire deadline budget can be consumed while zero probes run at all,
//      and then the very next probe — which might just catch the gateway a
//      moment before it starts listening — gets treated as terminal, with
//      the whole freeze duration reported as if it were "wait time",
//      undiagnosable from a genuine boot failure.
//   2. `checkFatalError`, when provided, is checked every iteration BEFORE
//      probing, so a fast, genuine failure (e.g. the gateway process itself
//      already exited) is reported immediately with the real cause, never
//      masked by the polling budget.
//   3. `hardBackstopMs` is an absolute wall-clock ceiling — a backstop, not
//      the primary signal — so a gateway that is genuinely wedged (hanging
//      on every request rather than failing fast) still cannot hang CI
//      forever.
// ─────────────────────────────────────────────────────────────────────────────

export interface ProbeOutcome {
  ok: boolean;
  error?: unknown;
}

export type PollOutcomeKind = 'ready' | 'fatal-error' | 'consecutive-failures' | 'hard-backstop';

export interface PollResult {
  kind: PollOutcomeKind;
  attempts: number;
  elapsedMs: number;
  consecutiveFailures: number;
  lastProbeError?: unknown;
  fatalError?: unknown;
}

export interface PollUntilHealthyConfig {
  probe: () => Promise<ProbeOutcome>;
  /** Checked every iteration before probing. Return a truthy value (e.g. an
   * Error) to fail fast; return undefined/null to keep polling. */
  checkFatalError?: () => unknown;
  intervalMs: number;
  consecutiveFailThreshold: number;
  hardBackstopMs: number;
  /** Injectable clock/sleep so the decision logic is testable with a fake
   * clock — no real gateway, no real network, no real waiting required. */
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
}

/**
 * Polls cfg.probe at cfg.intervalMs until it reports healthy. See the
 * section doc comment above for the full rationale.
 */
export async function pollUntilHealthy(cfg: PollUntilHealthyConfig): Promise<PollResult> {
  const now = cfg.now ?? (() => Date.now());
  const sleep = cfg.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  const start = now();
  let attempts = 0;
  let consecutiveFailures = 0;
  let lastProbeError: unknown;

  for (;;) {
    if (cfg.checkFatalError) {
      const fatal = cfg.checkFatalError();
      if (fatal) {
        return {
          kind: 'fatal-error',
          attempts,
          elapsedMs: now() - start,
          consecutiveFailures,
          fatalError: fatal,
        };
      }
    }

    attempts++;
    const outcome = await cfg.probe();
    if (outcome.ok) {
      return { kind: 'ready', attempts, elapsedMs: now() - start, consecutiveFailures };
    }

    lastProbeError = outcome.error;
    consecutiveFailures++;
    const elapsedMs = now() - start;

    // Backstop check first: it must fire regardless of how the failures are
    // patterned (e.g. very few, very slow failures), since it exists purely
    // to bound total wall-clock time.
    if (elapsedMs >= cfg.hardBackstopMs) {
      return { kind: 'hard-backstop', attempts, elapsedMs, consecutiveFailures, lastProbeError };
    }
    if (consecutiveFailures >= cfg.consecutiveFailThreshold) {
      return { kind: 'consecutive-failures', attempts, elapsedMs, consecutiveFailures, lastProbeError };
    }

    await sleep(cfg.intervalMs);
  }
}

/**
 * Poll /health until it returns 200.
 *
 * `proc`, when provided, is checked every iteration — if the gateway process
 * has already exited, waitForHealth fails immediately with the real exit
 * code instead of waiting out the full polling budget.
 *
 * Constants: intervalMs (200ms, unchanged from the previous design) and
 * consecutiveFailThreshold are chosen so that `consecutiveFailThreshold *
 * intervalMs == timeoutMs` — i.e. a run of CONTINUOUSLY failing probes gets
 * the exact same real-time budget the old wall-clock deadline granted
 * (default 15s), except it can now only be consumed by actual failed
 * attempts, never by a frozen/stalled host doing nothing. hardBackstopMs is
 * 2x that budget (default 30s, matching the Go harness's hard backstop) as
 * an absolute ceiling for a gateway that is wedged rather than merely slow
 * to boot — see pollUntilHealthy's doc comment for why that's a backstop and
 * not the primary signal. Each probe itself is bounded to 2s via
 * AbortSignal.timeout so one hung request cannot silently eat most of the
 * hard backstop by itself.
 */
export async function waitForHealth(
  baseURL: string,
  timeoutMs = 15_000,
  proc?: ChildProcess,
): Promise<void> {
  const intervalMs = 200;
  const consecutiveFailThreshold = Math.max(1, Math.ceil(timeoutMs / intervalMs));
  const hardBackstopMs = timeoutMs * 2;
  const probeTimeoutMs = 2_000;

  const result = await pollUntilHealthy({
    probe: async () => {
      try {
        const res = await fetch(`${baseURL}/health`, { signal: AbortSignal.timeout(probeTimeoutMs) });
        if (res.ok) return { ok: true };
        return { ok: false, error: new Error(`health endpoint returned status ${res.status}`) };
      } catch (err) {
        return { ok: false, error: err };
      }
    },
    checkFatalError: proc
      ? () =>
          proc.exitCode !== null
            ? new Error(`gateway process exited with code ${proc.exitCode} while waiting for /health`)
            : undefined
      : undefined,
    intervalMs,
    consecutiveFailThreshold,
    hardBackstopMs,
  });

  if (result.kind === 'ready') return;

  if (result.kind === 'fatal-error') {
    throw new Error(
      `${baseURL}/health: gateway failed before becoming healthy: ${String(result.fatalError)} ` +
        `(fast-fail: ${result.attempts} probe attempt(s), ${result.elapsedMs}ms elapsed — this is a genuine ` +
        `failure surfaced immediately, not a timeout)`,
    );
  }

  if (result.kind === 'consecutive-failures') {
    throw new Error(
      `${baseURL}/health never returned 200 after ${result.consecutiveFailures} consecutive failed probes ` +
        `(${result.elapsedMs}ms elapsed) — this indicates a genuine boot failure, not a scheduling stall ` +
        `(a frozen host would produce FEW probes, not failed ones); last probe error: ${String(result.lastProbeError)}`,
    );
  }

  // result.kind === 'hard-backstop'
  throw new Error(
    `${baseURL}/health hit the ${hardBackstopMs}ms hard backstop after only ${result.attempts} probe attempt(s) ` +
      `(${result.elapsedMs}ms elapsed) without becoming healthy — this is the absolute ceiling, not the primary ` +
      `failure signal; probes are likely hanging (gateway wedged/hung, not merely slow to boot); ` +
      `last probe error: ${String(result.lastProbeError)}`,
  );
}

/**
 * Seed an admin via POST /api/v1/onboarding/complete.
 * Idempotent: 409 is treated as success.
 * See pkg/gateway/rest_onboarding.go — endpoint is CSRF-exempt.
 */
export async function onboardAdmin(
  baseURL: string,
  username: string,
  password: string,
): Promise<void> {
  const res = await fetch(`${baseURL}/api/v1/onboarding/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      provider: {
        auth_method: 'api_key',
        id: 'openrouter',
        // CI injects OPENROUTER_API_KEY_CI (per-shard via GITHUB_ENV). Local
        // runs and the Go Tests job use OPENROUTER_API_KEY. Either is a real
        // key; the placeholder is last so a self-managed gateway (hot-reload)
        // does not onboard with sk-test-placeholder and get a 400 from
        // OpenRouter.
        api_key:
          process.env.OPENROUTER_API_KEY ??
          process.env.OPENROUTER_API_KEY_CI ??
          'sk-test-placeholder',
        model: 'openai/gpt-4o',
      },
      admin: { username, password },
    }),
  });

  if (!res.ok && res.status !== 409) {
    const body = await res.text();
    throw new Error(`onboardAdmin: onboarding failed ${res.status}: ${body}`);
  }
}

/**
 * Log in and return the bearer token.
 * See pkg/gateway/rest_auth.go HandleLogin.
 */
export async function loginAPI(
  baseURL: string,
  username: string,
  password: string,
): Promise<{ token: string; role: string }> {
  const res = await fetch(`${baseURL}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`loginAPI: login failed ${res.status}: ${body}`);
  }
  const data = (await res.json()) as { token: string; role: string };
  return data;
}
