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

const DEFAULT_MODEL = 'z-ai/glm-5.2';

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
  /** Extra gateway args (default: ['--sandbox=off'], mirroring setup.ts —
   * this is an isolated throwaway OMNIPUS_HOME, never the developer's real
   * one, so a permissive sandbox is safe and keeps the boot-sweep proof
   * focused on lifecycle reconciliation rather than sandbox mechanics). */
  extraArgs?: string[];
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

  private readonly binary: string;
  private readonly extraArgs: string[];
  private readonly model: string;
  private proc: ChildProcess | null = null;
  private ctx: APIRequestContext | null = null;
  private csrfToken = '';

  private constructor(
    opts: { binary: string; adminUsername: string; adminPassword: string; model: string; extraArgs: string[] },
    homeDir: string,
    port: number,
  ) {
    this.binary = opts.binary;
    this.adminUsername = opts.adminUsername;
    this.adminPassword = opts.adminPassword;
    this.model = opts.model;
    this.extraArgs = opts.extraArgs;
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
    const port = await getFreePort();
    const homeDir = fs.mkdtempSync(path.join(os.tmpdir(), `omnipus-e2e-gwproc-${port}-`));
    fs.writeFileSync(
      path.join(homeDir, 'config.json'),
      JSON.stringify({ version: 1, gateway: { port } }, null, 2),
      { mode: 0o600 },
    );

    const gw = new GatewayProcess(
      {
        binary,
        adminUsername: opts.adminUsername ?? 'admin',
        adminPassword: opts.adminPassword ?? 'admin1234',
        model: opts.model ?? DEFAULT_MODEL,
        extraArgs: opts.extraArgs ?? ['--sandbox=off'],
      },
      homeDir,
      port,
    );

    await gw.spawnProcess();
    await waitForHealth(gw.baseURL, 15_000, gw.proc ?? undefined);
    await gw.onboardAndLogin();
    return gw;
  }

  /** Spawn the binary against this.homeDir/this.port and resolve once the
   * process has logged that it is listening (mirrors setup.ts:startGateway's
   * log-matching + fatal-exit-during-boot handling exactly). */
  private async spawnProcess(): Promise<void> {
    const args = ['gateway', '--allow-empty', ...this.extraArgs];
    this.proc = await new Promise<ChildProcess>((resolve, reject) => {
      const child = spawn(this.binary, args, {
        env: {
          ...process.env,
          OMNIPUS_HOME: this.homeDir,
          OMNIPUS_BEARER_TOKEN: '',
        },
        stdio: ['ignore', 'pipe', 'pipe'],
      });

      const timeout = setTimeout(() => {
        child.kill();
        reject(new Error(`GatewayProcess: gateway did not start within 30s on port ${this.port}`));
      }, 30_000);

      let output = '';
      const onData = (chunk: Buffer) => {
        output += chunk.toString();
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

  /** First-boot only: seed the admin/provider via onboarding, then log in. */
  private async onboardAndLogin(): Promise<void> {
    const apiKey = process.env.OPENROUTER_API_KEY_CI ?? process.env.OPENROUTER_API_KEY ?? 'sk-test-placeholder';
    const onboardCtx = await request.newContext({ baseURL: this.baseURL });
    try {
      const res = await onboardCtx.post('/api/v1/onboarding/complete', {
        data: {
          provider: { auth_method: 'api_key', id: 'openrouter', api_key: apiKey, model: this.model },
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
   * CSRF/cookie re-mint (why a fresh login is MANDATORY here, not optional):
   * `omnipus-session` and the CSRF cookie are minted from in-process secrets
   * (pkg/gateway/middleware/session_cookie.go, csrf.go) — a fresh process
   * boot mints FRESH secrets, so any cookie value captured before kill9() is
   * dead on arrival against the restarted process even though the underlying
   * user account persisted in the same config.json. There is no "carry the
   * old cookie forward" shortcut available; the only correct move — and the
   * one a real client reconnecting after a server restart would also have to
   * make — is a brand new POST /api/v1/auth/login against the new process.
   * login() disposes the stale APIRequestContext and captures a fresh
   * session+CSRF pair before this method returns, so every apiFetch() call
   * made after restart() resolves is already correctly authenticated.
   */
  async restart(): Promise<void> {
    await this.spawnProcess();
    await waitForHealth(this.baseURL, 15_000, this.proc ?? undefined);
    await this.login();
  }

  /** Graceful shutdown (SIGTERM) + best-effort OMNIPUS_HOME cleanup. */
  async stop(): Promise<void> {
    if (this.proc && this.proc.exitCode === null && !this.proc.killed) {
      this.proc.kill('SIGTERM');
    }
    if (this.ctx) {
      await this.ctx.dispose().catch(() => {});
      this.ctx = null;
    }
    if (fs.existsSync(this.homeDir)) {
      try {
        fs.rmSync(this.homeDir, { recursive: true, force: true });
      } catch {
        // Best-effort cleanup; workspace dirs created by the gateway may be non-empty.
      }
    }
  }
}
