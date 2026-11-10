// Omnipus — isolated, self-managed gateway process fixture.
//
// Built for E.1 (adr-053-DEFERRED-ISSUES.md) — the boot-sweep conformance
// test needs to `kill -9` a REAL gateway process mid-task and restart it,
// which the shared conformance-suite gateway (one instance for all nine
// Conformance_* specs, driven by global-setup.ts on OMNIPUS_URL) cannot
// support: killing it would take down every other test in the file.
//
// GatewayProcess owns:
//   - its own ephemeral port (setup.ts's getFreePort — no hardcoded
//     collision with the shared gateway's port or other e2e shards)
//   - its own throwaway OMNIPUS_HOME (mkdtemp — its own config.json,
//     its own auto-generated master.key + credentials.json, ADR-004)
//   - its own authenticated Playwright APIRequestContext, deliberately
//     NOT the shared spec file's `page` browser context. Cookie names
//     (`omnipus-session`, `__Host-csrf`/`csrf`) are fixed strings with no
//     port-scoping (RFC 6265 cookies are host-scoped, not host:port-scoped),
//     so logging into a second gateway on a different port from the SAME
//     browser context would silently clobber the shared gateway's session
//     cookie out from under every other Conformance_* test. A fresh
//     `request.newContext({ baseURL })` (the same primitive global-setup.ts
//     and fixtures/onboard-via-api.ts already use) keeps its own private
//     cookie jar, so the two gateways' sessions never interact.
//
// kill9() + restart() are the two operations E.1 actually needs:
//   - kill9() sends SIGKILL and waits for the real 'exit' event (kill(2) is
//     asynchronous — starting a new process on the same port before the OS
//     has released the old listener races EADDRINUSE).
//   - restart() re-spawns the SAME binary against the SAME OMNIPUS_HOME and
//     port (never mkdtemps a new home — the whole point is the NEXT process
//     rehydrates the PREVIOUS process's on-disk state, including whatever
//     credentials.json/master.key it minted on first boot) and then re-mints
//     auth from scratch. See restart()'s own doc comment for why a fresh
//     login is mandatory, not optional, after a restart.

import { spawn, type ChildProcess } from 'child_process';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { request, type APIRequestContext } from '@playwright/test';
import { getFreePort, waitForHealth, DEFAULT_OMNIPUS_BINARY } from '../setup.js';
import { E2E_FALLBACK, E2E_PRIMARY } from './e2e-model.js';
import { onboardViaAPI } from './onboard-via-api.js';

/** The fake key every mock-provider gateway onboards with; never a real secret. */
const MOCK_PROVIDER_KEY = 'sk-test-placeholder';

export interface ApiResult<T> {
  ok: boolean;
  status: number;
  body: T;
  raw: string;
}

export interface GatewayProcessOptions {
  binary?: string;
  adminUsername?: string;
  adminPassword?: string;
  model?: string;
  /**
   * provider-messages row 20: explicit provider base URL passed through to
   * onboarding as `provider.endpoint` (the OnboardingProviderApiKey wire
   * field — contracts/components/schemas/OnboardingProviderApiKey.yaml).
   * The onboarding key probe (rest_onboarding.go: probeBase resolves
   * variant.Endpoint ahead of the catalog default) then goes to THIS base
   * instead of the real vendor, so a spec can point the whole install at a
   * local mock provider. Omitted → behaviour identical to before (no
   * endpoint sent).
   */
  apiBase?: string;
  /** Extra gateway args (default: ['--sandbox=off'], mirroring setup.ts —
   * this is an isolated throwaway OMNIPUS_HOME, never the developer's real
   * one, so a permissive sandbox is safe and keeps the boot-sweep proof
   * focused on lifecycle reconciliation rather than sandbox mechanics). */
  extraArgs?: string[];
  /**
   * Extra environment variables for the spawned process — e.g. #823
   * scenario h's `OMNIPUS_TEST_ONLY_STREAM_TOKEN_DELAY_MS` (read once at
   * gateway start, `pkg/gateway/ws_session_hub.go`'s
   * `streamTokenDelayEnvOverrideVar`), which pauses the web streamer that
   * long after each published token so a kill reliably lands mid-answer
   * instead of racing a fast model to completion. Stored on the instance
   * (below) so `restart()` — which re-spawns via the SAME `spawnProcess()`
   * this env is read in — keeps it automatically; never needs to be passed
   * again. Test-only knobs like this exist specifically so an isolated
   * `GatewayProcess` can opt in without touching the shared gateway any
   * other scenario in a spec file uses.
   */
  env?: Record<string, string>;
  /**
   * Boot with gateway.public_url set to this process's own loopback origin
   * (http://localhost:<port>, via OMNIPUS_GATEWAY_PUBLIC_URL; an explicit
   * `env` entry still wins). The port is only known inside start(), so a
   * caller cannot compute it. Without a public URL a gateway cannot mint a
   * Mode 1 isolated address (ADR-094 FR-001/FR-011).
   */
  loopbackPublicURL?: boolean;
  /** The /health budget for each boot attempt (default 15 s, passed to
   * setup.ts::waitForHealth). Tests of this fixture lower it. */
  healthTimeoutMs?: number;
  /** How long a boot may take to announce its port (default 30 s). Tests of
   * this fixture lower it. */
  startupTimeoutMs?: number;
}

/** Gateway output kept in memory per process (the tail is what is reported). */
const OUTPUT_KEEP_BYTES = 256 * 1024;
/** Lines of gateway output put into a boot-failure report. */
const OUTPUT_TAIL_LINES = 80;
/** Bytes read from the end of the gateway's panic log for a report. */
export const PANIC_LOG_READ_BYTES = 64 * 1024;
/** How long stop() waits after SIGTERM before SIGKILL, and after SIGKILL. */
const STOP_GRACE_MS = 10_000;
const KILL_WAIT_MS = 10_000;

/** Whether a child has really exited (or never started: no pid). */
function hasExited(proc: ChildProcess): boolean {
  return proc.pid === undefined || proc.exitCode !== null || proc.signalCode !== null;
}

/** Resolves true when the child has exited, false after `ms` without exit. */
function waitForExit(proc: ChildProcess, ms: number): Promise<boolean> {
  if (hasExited(proc)) return Promise.resolve(true);
  return new Promise((resolve) => {
    const onExit = () => {
      clearTimeout(timer);
      resolve(true);
    };
    const timer = setTimeout(() => {
      proc.off('exit', onExit);
      resolve(hasExited(proc));
    }, ms);
    proc.once('exit', onExit);
  });
}

/** Env names whose values are treated as secrets in a boot report: a
 * KEY/TOKEN/SECRET/PASSWORD/PASSWD/CREDENTIAL(S) word anywhere in the name
 * (OPENROUTER_API_KEY, GITHUB_TOKEN, DB_PASSWORD, ...). */
const SECRET_ENV_NAME = /(^|[_-])(API_?KEY|KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?)($|[_-])/i;

/** The forms a known secret may take in output: as is, JSON-escaped (once
 * and twice, e.g. a JSON string logged inside JSON) and URL-encoded. */
function secretForms(value: string): string[] {
  const json = JSON.stringify(value).slice(1, -1);
  const json2 = JSON.stringify(json).slice(1, -1);
  return [...new Set([value, json, json2, encodeURIComponent(value)])];
}

/**
 * Blank out secrets before gateway output reaches the job log or an error.
 * Covers the value (any length) of every secret-named variable in the
 * environment the gateway was actually started with, including restart()
 * overrides, the admin password, each in its JSON-escaped and URL-encoded
 * forms, and key-shaped text: sk- keys, bearer tokens and
 * key/token/secret/password assignments. Callers scrub BEFORE cutting text
 * to a tail, so a cut never splits a known secret out of recognition.
 */
function scrubSecrets(text: string, env: Record<string, string | undefined>, extra: string[]): string {
  const labels = new Map<string, string>();
  for (const [name, value] of Object.entries(env)) {
    if (value && SECRET_ENV_NAME.test(name)) {
      for (const form of secretForms(value)) labels.set(form, name);
    }
  }
  for (const value of extra) {
    if (!value) continue;
    for (const form of secretForms(value)) if (!labels.has(form)) labels.set(form, 'password');
  }
  let out = text;
  for (const form of [...labels.keys()].sort((a, b) => b.length - a.length)) {
    out = out.split(form).join(`[REDACTED:${labels.get(form)}]`);
  }
  return out
    .replace(/\bsk-[A-Za-z0-9_-]{16,}/g, '[REDACTED:sk-key]')
    .replace(/(Bearer\s+)[A-Za-z0-9._~+/=-]{16,}/gi, '$1[REDACTED:bearer]')
    .replace(
      /((?:api[_-]?key|token|secret|password|passwd)\\?["']?\s*[:=]\s*\\?["']?)(?!\[REDACTED)([^\s"'\\,;}]{4,})/gi,
      '$1[REDACTED:value]',
    );
}

/** The last `lines` lines of `text`. When `cut` (the text is the end of a
 * longer stream), the first line is partial and is dropped. */
function lastLines(text: string, lines: number, cut: boolean): string {
  let all = text.replace(/\n$/, '').split('\n');
  if (cut) all = all.slice(1);
  return all.slice(-lines).join('\n');
}

/**
 * An isolated, killable, restartable omnipus gateway process for tests that
 * need to prove crash-recovery behavior (ADR-053 §5 boot sweep) against a
 * REAL process boundary — not a mock, not a Go-internal function call.
 */
export class GatewayProcess {
  readonly homeDir: string;
  readonly port: number;
  readonly baseURL: string;
  readonly adminUsername: string;
  readonly adminPassword: string;
  /** provider-messages row 20: api_base passed to onboarding ('' = not set). */
  readonly apiBase: string;

  private readonly binary: string;
  private readonly extraArgs: string[];
  private readonly extraEnv: Record<string, string>;
  private readonly model: string;
  private proc: ChildProcess | null = null;
  private ctx: APIRequestContext | null = null;
  private csrfToken = '';
  /** This process's stdout+stderr (last OUTPUT_KEEP_BYTES), for boot-failure reports. */
  private output = '';
  /** Whether `output` lost its beginning to the OUTPUT_KEEP_BYTES bound. */
  private outputCut = false;
  /** The environment the current process was spawned with (for scrubbing). */
  private spawnEnv: Record<string, string | undefined> = {};

  /** The last `lines` lines of this gateway's stdout+stderr (unscrubbed). */
  outputTail(lines = OUTPUT_TAIL_LINES): string {
    return lastLines(this.output, lines, this.outputCut);
  }

  /** `text` with every known secret of this gateway blanked out. */
  private scrub(text: string): string {
    return scrubSecrets(text, { ...process.env, ...this.extraEnv, ...this.spawnEnv }, [this.adminPassword]);
  }

  /**
   * The last `lines` lines of $OMNIPUS_HOME/logs/gateway_panic.log. Once the
   * gateway initializes logging it redirects its stderr into that file
   * (pkg/logger/panic_unix.go::initPanicFile) and writes recovered panics
   * there (pkg/logger/panic.go::InitPanic), so a crash after that point is
   * only visible here. Read before the home is deleted.
   */
  private panicLogTail(lines = OUTPUT_TAIL_LINES): string {
    const file = path.join(this.homeDir, 'logs', 'gateway_panic.log');
    let fd: number | undefined;
    try {
      fd = fs.openSync(file, 'r');
      const size = fs.fstatSync(fd).size;
      const len = Math.min(size, PANIC_LOG_READ_BYTES);
      const buf = Buffer.alloc(len);
      fs.readSync(fd, buf, 0, len, size - len);
      // Scrub the whole read window first, then cut: a window that starts
      // inside a line drops that partial line, so a secret it splits can
      // neither escape recognition nor be shown in part.
      const text = this.scrub(buf.toString('utf8'));
      return lastLines(text, lines, len < size) || '(empty)';
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code === 'ENOENT') return '(no panic log: the gateway did not get that far)';
      return `(could not read ${file}: ${err instanceof Error ? err.message : String(err)})`;
    } finally {
      if (fd !== undefined) fs.closeSync(fd);
    }
  }

  /** A boot failure plus this gateway's own output and panic-log tails,
   * secrets scrubbed: the cause, not a guess. */
  private bootFailureReport(err: unknown, what: string): string {
    const message = this.scrub(err instanceof Error ? err.message : String(err));
    const output = lastLines(this.scrub(this.output), OUTPUT_TAIL_LINES, this.outputCut);
    return (
      `GatewayProcess ${what} failed (port ${this.port}, OMNIPUS_HOME ${this.homeDir}, binary ${this.binary}): ${message}\n` +
      `--- gateway output (last ${OUTPUT_TAIL_LINES} lines, stdout+stderr) ---\n` +
      `${output || '(the gateway wrote no output)'}\n` +
      `--- gateway_panic.log (last ${OUTPUT_TAIL_LINES} lines; the gateway redirects its stderr here once logging starts) ---\n` +
      `${this.panicLogTail()}\n` +
      `--- end gateway output ---`
    );
  }

  /**
   * stop() after a failure, keeping that failure if cleanup fails too: the
   * returned text is `failure` plus the (scrubbed) cleanup error, or
   * undefined when stop() succeeded.
   */
  private async stopAfterFailure(failure: string): Promise<string | undefined> {
    let cleanup: string | undefined;
    try {
      await this.stop();
    } catch (stopErr) {
      cleanup = this.scrub(stopErr instanceof Error ? stopErr.message : String(stopErr));
    }
    return cleanup === undefined ? undefined : `${failure}\n--- cleanup after that failure also failed ---\n${cleanup}`;
  }

  private constructor(
    opts: { binary: string; adminUsername: string; adminPassword: string; model: string; apiBase: string; extraArgs: string[]; extraEnv: Record<string, string> },
    homeDir: string,
    port: number,
  ) {
    this.binary = opts.binary;
    this.adminUsername = opts.adminUsername;
    this.adminPassword = opts.adminPassword;
    this.apiBase = opts.apiBase;
    this.model = opts.model;
    this.extraArgs = opts.extraArgs;
    this.extraEnv = opts.extraEnv;
    this.homeDir = homeDir;
    this.port = port;
    this.baseURL = `http://localhost:${port}`;
  }

  /** Boot a fresh isolated gateway: own port, own OMNIPUS_HOME, onboarded
   * admin, authenticated APIRequestContext ready for apiFetch(). */
  static async start(opts: GatewayProcessOptions = {}): Promise<GatewayProcess> {
    const binary = opts.binary ?? process.env.OMNIPUS_BINARY ?? DEFAULT_OMNIPUS_BINARY;
    if (!fs.existsSync(binary)) {
      throw new Error(
        `BLOCKED: gateway binary not found at ${binary}.\n` +
          'Build it with:\n' +
          `  CGO_ENABLED=0 go build -tags goolm,stdjson -o ${binary} ./cmd/omnipus/`,
      );
    }
    const healthTimeoutMs = opts.healthTimeoutMs ?? 15_000;
    const startupTimeoutMs = opts.startupTimeoutMs ?? 30_000;

    // A boot (spawn + /health) that fails FAILS: it is never retried, so an
    // unexplained crash cannot turn into a pass. Its proof goes to the job
    // output and the thrown error: the gateway's own stdout/stderr and
    // panic-log tails, scrubbed (CI 2026-11-10: "/health never returned 200"
    // with no gateway output anywhere). The error is built from that
    // scrubbed text only, never with the raw error as its cause, because a
    // startup error carries the gateway's raw output. Onboarding and login
    // are not retried either.
    const gw = await GatewayProcess.create(binary, opts);
    let bootFailure: string | undefined;
    try {
      await gw.spawnProcess({}, startupTimeoutMs);
      await waitForHealth(gw.baseURL, healthTimeoutMs, gw.proc ?? undefined);
    } catch (err) {
      // The report reads the panic log, so it is built before stop()
      // deletes the home.
      bootFailure = gw.bootFailureReport(err, 'boot');
    }
    if (bootFailure !== undefined) {
      console.error(bootFailure);
      // The failed boot is really gone (exit observed, port released, home
      // removed) before the failure is thrown.
      throw new Error((await gw.stopAfterFailure(bootFailure)) ?? bootFailure);
    }
    // A failed onboarding (a missing key, a refused probe) must not leave
    // the process running or its temp OMNIPUS_HOME behind either. If that
    // cleanup fails too, the error keeps both (scrubbed, no raw cause).
    try {
      await gw.onboardAndLogin();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      const both = await gw.stopAfterFailure(gw.scrub(`GatewayProcess onboarding/login failed: ${message}`));
      if (both === undefined) throw err;
      bootFailure = both;
    }
    if (bootFailure !== undefined) throw new Error(bootFailure);
    return gw;
  }

  /** One fresh gateway instance: own free port, own OMNIPUS_HOME with its
   * config.json. Not yet spawned. */
  private static async create(binary: string, opts: GatewayProcessOptions): Promise<GatewayProcess> {
    const port = await getFreePort();
    const homeDir = fs.mkdtempSync(path.join(os.tmpdir(), `omnipus-e2e-gwproc-${port}-`));
    // A mock-provider gateway onboards a catalog provider with its endpoint
    // pointed at the loopback mock. A hosted-stamped binary accepts that
    // endpoint override only when the operator SSRF allow list names its host,
    // so the mock gateway's config names loopback, as such an operator would.
    const sandbox = opts.apiBase
      ? { sandbox: { ssrf: { allow_internal: ['127.0.0.1', '::1', 'localhost'] } } }
      : {};
    fs.writeFileSync(
      path.join(homeDir, 'config.json'),
      JSON.stringify({ version: 1, gateway: { port }, ...sandbox }, null, 2),
      { mode: 0o600 },
    );

    return new GatewayProcess(
      {
        binary,
        adminUsername: opts.adminUsername ?? 'admin',
        adminPassword: opts.adminPassword ?? 'admin1234',
        // A mock-provider gateway (apiBase set) runs on the fallback pair: the
        // mocks speak the OpenAI-compatible protocol. A live gateway runs on
        // the primary.
        model: opts.model ?? (opts.apiBase ? E2E_FALLBACK.model : E2E_PRIMARY.model),
        apiBase: opts.apiBase ?? '',
        extraArgs: opts.extraArgs ?? ['--sandbox=off'],
        extraEnv: {
          ...(opts.loopbackPublicURL ? { OMNIPUS_GATEWAY_PUBLIC_URL: `http://localhost:${port}` } : {}),
          ...(opts.env ?? {}),
        },
      },
      homeDir,
      port,
    );
  }

  /** Spawn the binary against this.homeDir/this.port and resolve once the
   * process has logged that it is listening (mirrors setup.ts:startGateway's
   * log-matching + fatal-exit-during-boot handling exactly). */
  private async spawnProcess(envOverrides: Record<string, string> = {}, startupTimeoutMs = 30_000): Promise<void> {
    const args = ['gateway', '--allow-empty', ...this.extraArgs];
    const env = {
      ...process.env,
      OMNIPUS_HOME: this.homeDir,
      OMNIPUS_BEARER_TOKEN: '',
      ...this.extraEnv,
      ...envOverrides,
    };
    // Kept so a report scrubs the secrets of THIS spawn (restart overrides too).
    this.spawnEnv = env;
    this.proc = await new Promise<ChildProcess>((resolve, reject) => {
      const child = spawn(this.binary, args, {
        env,
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      // Owned from spawn time, not only after the port announcement: a boot
      // that fails before announcing must still be stoppable by stop().
      this.proc = child;

      const timeout = setTimeout(() => {
        child.kill();
        reject(new Error(`GatewayProcess: gateway did not start within ${startupTimeoutMs / 1000}s on port ${this.port}`));
      }, startupTimeoutMs);

      let output = '';
      const onData = (chunk: Buffer) => {
        const text = chunk.toString();
        output += text;
        // Kept for the life of the process (bounded), for boot-failure reports.
        const kept = this.output + text;
        if (kept.length > OUTPUT_KEEP_BYTES) this.outputCut = true;
        this.output = kept.slice(-OUTPUT_KEEP_BYTES);
        if (
          output.includes(`localhost:${this.port}`) ||
          output.includes(`0.0.0.0:${this.port}`) ||
          output.includes(`:${this.port}`)
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
          reject(new Error(`GatewayProcess: gateway exited with code ${code} during startup.\nOutput:\n${output}`));
        }
      });
      child.on('error', (err) => {
        clearTimeout(timeout);
        reject(err);
      });
    });
  }

  /** First-boot only: seed the admin/provider via onboarding, then log in.
   *
   * Live gateway (no apiBase): the shared onboardViaAPI — primary provider
   * with its key, then the fallback provider and every agent's
   * fallback_models. Mock gateway (apiBase): the fallback pair (an
   * OpenAI-compatible provider) pointed at the mock with a fixed fake key. */
  private async onboardAndLogin(): Promise<void> {
    if (!this.apiBase) {
      await onboardViaAPI({
        baseURL: this.baseURL,
        model: this.model,
        username: this.adminUsername,
        password: this.adminPassword,
      });
      await this.login();
      return;
    }
    // A mock provider accepts any key, so a mock gateway never receives a real
    // one: this request goes through Playwright's request client, which the
    // trace records (see onboard-via-api.ts).
    const apiKey = MOCK_PROVIDER_KEY;
    const onboardCtx = await request.newContext({ baseURL: this.baseURL });
    try {
      const res = await onboardCtx.post('/api/v1/onboarding/complete', {
        data: {
          provider: {
            auth_method: 'api_key',
            id: E2E_FALLBACK.provider,
            api_key: apiKey,
            model: this.model,
            // provider-messages row 20: an explicit endpoint rides the same
            // onboarding contract (OnboardingProviderApiKey.endpoint —
            // contracts/components/schemas/OnboardingProviderApiKey.yaml;
            // rest_onboarding.go reads variant.Endpoint and persists it as
            // the provider entry's api_base; probeBase prefers it) — the key
            // probe AND every later chat call go to this base.
            endpoint: this.apiBase,
          },
          admin: { username: this.adminUsername, password: this.adminPassword },
        },
      });
      if (!res.ok() && res.status() !== 409) {
        throw new Error(`GatewayProcess: onboarding failed ${res.status()}: ${await res.text()}`);
      }
    } finally {
      await onboardCtx.dispose();
    }
    await this.login();
  }

  /**
   * (Re)establish an authenticated APIRequestContext + CSRF token via a real
   * POST /api/v1/auth/login (pkg/gateway/rest_auth.go HandleLogin — the same
   * handler global-setup.ts drives for the shared gateway). Disposes any
   * prior context first — see restart()'s doc comment for why a stale
   * context/cookie pair is never reusable across a process boundary.
   */
  private async login(): Promise<void> {
    if (this.ctx) {
      await this.ctx.dispose().catch(() => {});
      this.ctx = null;
    }
    const ctx = await request.newContext({ baseURL: this.baseURL });
    const res = await ctx.post('/api/v1/auth/login', {
      data: { username: this.adminUsername, password: this.adminPassword },
    });
    if (!res.ok()) {
      throw new Error(`GatewayProcess: login failed ${res.status()}: ${await res.text()}`);
    }
    const state = await ctx.storageState();
    const csrf = state.cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf');
    if (!csrf) {
      throw new Error(
        'GatewayProcess: POST /api/v1/auth/login did not set a CSRF cookie ' +
          '(__Host-csrf on TLS / csrf on plain HTTP) — see pkg/gateway/middleware/csrf.go.',
      );
    }
    this.ctx = ctx;
    this.csrfToken = csrf.value;
  }

  /** Storage state for a browser aimed at THIS gateway only. Cookies are for
   * this process's host, not the shared e2e gateway. */
  async browserStorageState() {
    if (!this.ctx) {
      throw new Error('GatewayProcess.browserStorageState: not authenticated — call start() first')
    }
    return this.ctx.storageState()
  }

  /** REST call against THIS gateway's own authenticated context — the
   * isolated-process twin of the shared spec file's apiFetch(page, ...). */
  async apiFetch<T = unknown>(
    method: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE',
    urlPath: string,
    data?: unknown,
  ): Promise<ApiResult<T>> {
    if (!this.ctx) {
      throw new Error('GatewayProcess.apiFetch: not authenticated — call start() (or restart() after a kill9()) first');
    }
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (method !== 'GET') {
      headers['X-Csrf-Token'] = this.csrfToken;
    }
    const res = await this.ctx.fetch(urlPath, {
      method,
      headers,
      data: data !== undefined ? JSON.stringify(data) : undefined,
    });
    const raw = await res.text();
    let body: unknown = null;
    if (raw) {
      try {
        body = JSON.parse(raw);
      } catch {
        body = null;
      }
    }
    return { ok: res.ok(), status: res.status(), body: body as T, raw };
  }

  /**
   * Send SIGKILL to the gateway process and wait for it to actually exit.
   *
   * kill(2)/SIGKILL is uncatchable but still asynchronous from Node's point
   * of view — the child does not become "exited" until the OS reaps it and
   * Node's event loop observes the 'exit' event. Resolving immediately after
   * calling .kill() (rather than awaiting 'exit') would let restart() race
   * the OS's release of the listening socket, intermittently failing with
   * EADDRINUSE on a loaded CI host. The 10s backstop is slack for that host,
   * not a signal genuinely expected to fire for an uncatchable signal.
   */
  async kill9(): Promise<void> {
    if (!this.proc || this.proc.exitCode !== null || this.proc.killed) {
      return;
    }
    const proc = this.proc;
    await new Promise<void>((resolve) => {
      let settled = false;
      const done = () => {
        if (settled) return;
        settled = true;
        resolve();
      };
      proc.once('exit', done);
      proc.kill('SIGKILL');
      setTimeout(done, 10_000);
    });
  }

  /**
   * Restart the SAME binary against the SAME OMNIPUS_HOME/port — this is the
   * "own credentials.json survives the kill" contract from the E.1 deferral:
   * restart() never mkdtemps a new home, so the next process rehydrates
   * whatever the first boot wrote (config.json, the auto-generated
   * master.key + credentials.json per ADR-004, and every task/plan/session
   * record the crashed process left on disk).
   *
   * CORRECTION (found via #823 scenario h, real-browser run): an earlier
   * version of this doc comment claimed "a fresh process boot mints FRESH
   * secrets, so any cookie value captured before kill9() is dead on arrival
   * against the restarted process". That is WRONG — checked against
   * `pkg/gateway/middleware/session_cookie.go::ResolveUserFromCookie`, the
   * `omnipus-session` cookie is validated by bcrypt-comparing its plaintext
   * against `UserConfig.SessionTokenHash`, a field PERSISTED in config.json
   * — nothing about it is re-derived from a per-process secret, so a cookie
   * minted before kill9() is still valid against the restarted process,
   * UNCHANGED, as long as nothing else overwrites that hash in the
   * meantime. The REAL reason `relogin` defaults to `true` (below) is
   * simpler and already documented elsewhere in this codebase
   * (`src/lib/authLogout.ts`'s own comment on the 'elsewhere' reason): the
   * session-token slot is SINGLE, one hash per user, overwritten on every
   * login — so `login()` calling `POST /api/v1/auth/login` for `admin` a
   * SECOND time (e.g. from a completely different browser context/page that
   * logged in as `admin` before kill9()) silently invalidates that OTHER
   * session, which is exactly the "You've been signed out — this session
   * ended, possibly because you signed in elsewhere" banner a real browser
   * shows (confirmed: this is what scenario h originally hit, tracing back
   * to this method's own internal re-login racing the test's own page
   * login for the SAME `admin` account).
   *
   * `relogin` (default `true`, matching every existing caller's behavior):
   * pass `false` when the caller does NOT need `apiFetch()` to work after
   * this call resolves and instead wants to observe an EXISTING session
   * (typically a Playwright `page` that logged in as this SAME admin user
   * via the UI, before kill9()) reconnect against the restarted process on
   * its own — the whole point of proving a real client survives a gateway
   * restart. With `relogin: false`, this method only re-spawns the process
   * and waits for health; `this.ctx`/`this.csrfToken` are left exactly as
   * they were (stale, and `apiFetch()` will throw the "not authenticated"
   * error below if called — call `login()` explicitly first if a caller
   * genuinely needs both an external page's session AND its own apiFetch
   * access after a restart).
   */
  async restart(opts: { relogin?: boolean; env?: Record<string, string> } = {}): Promise<void> {
    // Test-only process knobs can be phase-scoped without changing the saved
    // home, port, binary or credentials. Overrides apply to this boot only;
    // omitted keys retain the fixture's original environment.
    // No retry here: a restart must reuse this home and port. A failed
    // restart still carries the gateway's own output tail.
    // The error is built from the scrubbed report only (no raw cause).
    let failure: string | undefined;
    try {
      await this.spawnProcess(opts.env);
      await waitForHealth(this.baseURL, 15_000, this.proc ?? undefined);
    } catch (err) {
      failure = this.bootFailureReport(err, 'restart');
    }
    if (failure !== undefined) throw new Error(failure);
    if (opts.relogin ?? true) {
      await this.login();
    }
  }

  /** Test-only offline mutation guard (chat-history seam F). A requested kill
   * or kill9()'s backstop is not evidence that this owned child really exited. */
  assertStoppedForFixture(): void {
    if (!this.proc || (this.proc.exitCode === null && this.proc.signalCode === null)) {
      throw new Error('GatewayProcess.assertStoppedForFixture: owned gateway has not actually exited; refusing offline fixture mutation');
    }
  }

  /**
   * Shut the gateway down and wait until it has really exited: SIGTERM, then
   * SIGKILL after STOP_GRACE_MS. Only then is OMNIPUS_HOME removed, so a
   * still-running process never writes into a deleted home and its port is
   * free when stop() returns. Throws (keeping the home) if the process
   * survives SIGKILL.
   */
  async stop(): Promise<void> {
    const proc = this.proc;
    let survivor: Error | undefined;
    if (proc && !hasExited(proc)) {
      proc.kill('SIGTERM');
      if (!(await waitForExit(proc, STOP_GRACE_MS))) {
        console.error(
          `GatewayProcess.stop: pid ${proc.pid} (port ${this.port}) ignored SIGTERM for ${STOP_GRACE_MS / 1000}s; sending SIGKILL`,
        );
        proc.kill('SIGKILL');
        if (!(await waitForExit(proc, KILL_WAIT_MS))) {
          survivor = new Error(
            `GatewayProcess.stop: pid ${proc.pid} (port ${this.port}) did not exit after SIGTERM and SIGKILL; ` +
              `keeping its OMNIPUS_HOME ${this.homeDir}`,
          );
        }
      }
    }
    if (this.ctx) {
      await this.ctx.dispose().catch(() => {});
      this.ctx = null;
    }
    if (survivor) throw survivor;
    if (fs.existsSync(this.homeDir)) {
      try {
        fs.rmSync(this.homeDir, { recursive: true, force: true });
      } catch {
        // Best-effort cleanup; workspace dirs created by the gateway may be non-empty.
      }
    }
  }
}
