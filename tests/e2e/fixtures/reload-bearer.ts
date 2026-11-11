/**
 * reload-bearer.ts — the admin bearer e2e code uses for POST /reload, and the
 * one way to send it.
 *
 * Why a bearer at all: the health server's /reload accepts ONLY a bearer that
 * matches a Gateway.Users account or Gateway.CLIToken (issues #276/#640) —
 * never the omnipus-session cookie, never dev_mode_bypass. A spec must not
 * mint a fresh login to get one: POST /api/v1/auth/login overwrites the
 * single-slot session hash and kills the shared storageState cookie for every
 * later spec in the shard (scripts/check-e2e-login-crosstalk.sh). Instead the
 * bearer of an EXISTING login is reused: global-setup.ts stores the one from
 * its own login (account bearers are appended, so it stays valid), and a
 * private GatewayProcess keeps the one from its own login in memory.
 *
 * Two rules this file enforces:
 *   1. Storage is unique to the FULL auth-file identity: the bearer lives at
 *      `<absolute auth file>.reload-bearer`, never a fixed name in a shared
 *      folder. On the Fly CI worker every shard's auth file sits in the same
 *      folder (deploy/ci-worker/runci.sh::_e2e_run_shard), so a fixed name let
 *      a concurrent shard read another shard's gateway token and get 401.
 *   2. The bearer is sent with Node's own fetch, NOT a Playwright request
 *      client: Playwright records every request it sends (headers included)
 *      in the test trace, and CI uploads retained traces as artifacts. Same
 *      pattern as fixtures/onboard-via-api.ts; tests/e2e/trace-secret-canary
 *      .spec.ts pins that a bearer sent through here never reaches a trace.
 * No function here puts the token into a URL, browser storage or error text.
 */
import fs from 'node:fs'
import path from 'node:path'

/** Where the reload bearer for the storageState file `authFile` is stored. */
export function reloadBearerFileFor(authFile: string): string {
  return `${path.resolve(authFile)}.reload-bearer`
}

/** Store `token` as the reload bearer of `authFile` (owner-only, 0600). */
export function writeReloadBearer(authFile: string, token: string): void {
  const file = reloadBearerFileFor(authFile)
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, token, { mode: 0o600 })
  // writeFileSync's mode applies only when it creates the file.
  fs.chmodSync(file, 0o600)
}

/** Read the reload bearer global-setup.ts stored for `authFile`. Throws when absent or empty. */
export function readReloadBearer(authFile: string): string {
  const file = reloadBearerFileFor(authFile)
  if (!fs.existsSync(file)) {
    throw new Error(
      `readReloadBearer: no admin reload bearer at ${file}. global-setup.ts ` +
        'writes it next to the storageState at suite start (OMNIPUS_AUTH_FILE ' +
        'sets the storageState path). Do NOT fall back to POST /api/v1/auth/login ' +
        'here — that rotates the shared session.',
    )
  }
  const token = fs.readFileSync(file, 'utf-8').trim()
  if (!token) {
    throw new Error(`readReloadBearer: ${file} is empty — rerun the suite so global-setup.ts re-mints it.`)
  }
  return token
}

/** Result of POST /reload: status and the response text (the gateway's reload error on failure). */
export interface ReloadResult {
  ok: boolean
  status: number
  text: string
}

/**
 * POST `${baseURL}/reload` with `token` as the bearer, via Node fetch (no
 * trace observes it). The status is returned, never thrown, so each caller
 * keeps its own failure rule.
 */
export async function postReloadWithBearer(baseURL: string, token: string): Promise<ReloadResult> {
  const res = await fetch(new URL('/reload', baseURL), {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  })
  return { ok: res.ok, status: res.status, text: await res.text() }
}
