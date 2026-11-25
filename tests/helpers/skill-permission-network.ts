// C-2/C-3/C-22 transport fixture. Only the fetch edge is controlled; no
// production permissions helper, save queue, cache or editor is substituted.
import { afterEach, beforeEach, expect, vi } from 'vitest'
import type { ErrorResponse, PermissionSettings, PermissionSettingsPatch } from '../../src/lib/api/generated/openapi-types'
import {
  PermissionSettings as SettingsSchema,
  PermissionSettingsPatch as PatchSchema,
} from '../../src/lib/api/generated/schemas'

// not-wire-format: an observation of the HTTP edge, not an API carrier.
type Seen = { method: string; path: string; body?: unknown }

export class SkillPermissionNetwork {
  stored: PermissionSettings
  seen: Seen[] = []
  rejection?: { status: number; body: ErrorResponse; stored?: PermissionSettings }
  // Read-side doubles for a screen that only reads the global value (an agent's
  // Tools tab): GETs held unanswered, or one GET that fails. A PATCH never sees
  // either, and each test starts from a fresh network.
  readsHeld = false
  readFailure?: { status: number; body: ErrorResponse }

  constructor(settings: PermissionSettings) {
    SettingsSchema.parse(settings)
    this.stored = structuredClone(settings)
  }

  patches(): unknown[] {
    return this.seen.filter(({ method }) => method === 'PATCH').map(({ body }) => body)
  }

  // The response describes an external server failure. A supplied stored
  // snapshot represents 503's already-persisted state, not a UI outcome.
  refuseNext(status: number, error: string, stored?: PermissionSettings) {
    this.rejection = { status, body: { error }, stored }
  }

  // Every GET stays unanswered from now on, as a server that is still working.
  holdReads() {
    this.readsHeld = true
  }

  // The next GET answers with a server error; the GETs after it answer normally.
  failNextRead(status: number, error: string) {
    this.readFailure = { status, body: { error } }
  }

  fetch: typeof fetch = async (input, init) => {
    const rawURL = input instanceof Request ? input.url : String(input)
    const path = new URL(rawURL, 'http://localhost').pathname
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    if (path !== '/api/v1/security/permissions' || !['GET', 'PATCH'].includes(method)) {
      throw new Error(`Unexpected permissions fixture request: ${method} ${path}`)
    }
    const body: unknown = init?.body === undefined ? undefined : JSON.parse(String(init.body))
    this.seen.push({ method, path, ...(body === undefined ? {} : { body }) })
    if (method === 'GET') {
      if (this.readsHeld) return new Promise<Response>(() => {})
      const failure = this.readFailure
      this.readFailure = undefined
      if (failure) return Response.json(failure.body, { status: failure.status })
    }
    if (method === 'PATCH') {
      // Keep RAW body in observations: schema stripping must not hide extras.
      const patch: PermissionSettingsPatch = PatchSchema.parse(body)
      const failure = this.rejection
      this.rejection = undefined
      if (failure) {
        if (failure.stored) {
          SettingsSchema.parse(failure.stored)
          this.stored = structuredClone(failure.stored)
        }
        return Response.json(failure.body, { status: failure.status })
      }
      this.stored = {
        // Read-only members this fixture does not model (C-2 `upgrade_unfinished`) stay as the server holds
        // them: every PATCH answer carries the whole stored state, those members included.
        ...this.stored,
        policies: { ...this.stored.policies, ...patch.policies },
        skill_policies: { ...this.stored.skill_policies, ...patch.skill_policies },
        visibility: { ...this.stored.visibility, ...patch.visibility },
        skill_visibility: { ...this.stored.skill_visibility, ...patch.skill_visibility },
        skill_trust: patch.skill_trust ?? this.stored.skill_trust,
      }
    }
    SettingsSchema.parse(this.stored)
    return Response.json(this.stored)
  }
}

// Each test supplies its literal full read model; reloads GET the same server
// state. Nothing here infers expected policies from the component's output.
export function useSkillPermissionNetwork(initial: () => PermissionSettings) {
  let network: SkillPermissionNetwork
  beforeEach(() => {
    network = new SkillPermissionNetwork(initial())
    vi.stubGlobal('fetch', network.fetch)
    document.cookie = 'csrf=permissions-fixture; Path=/'
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    document.cookie = 'csrf=; Max-Age=0; Path=/'
  })
  return () => network
}

export function expectPermissionPatches(network: SkillPermissionNetwork, ...bodies: PermissionSettingsPatch[]) {
  expect(network.seen.filter(({ method }) => method === 'PATCH')).toEqual(
    bodies.map((body) => ({ method: 'PATCH', path: '/api/v1/security/permissions', body })),
  )
}
