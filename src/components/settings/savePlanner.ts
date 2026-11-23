// savePlanner — which request a Settings Save sends for each changed value
// (Settings redesign #1519, C-4 / B4, FR-026, FR-030, Dataset D-8).
//
// A Save covers one section. Every password-marked value of a section goes to
// that section's ONE password-protected endpoint, in one request, so one
// consent token covers the Save (C-4). The guard FR-030 runs this planner and
// fails when a section's password-marked values would reach more than one
// protected endpoint.

export interface PlannedField { // not-wire-format: screen-side description of one changed value
  key: string
  value: unknown
  password?: boolean
  /** The section's own write target name when it has several (FR-015). */
  target?: string
}

export interface PlannedWrite { // not-wire-format: one request the Save will send
  endpoint: string
  method: 'PUT'
  /** True when this request needs the Save's consent token. */
  password: boolean
  keys: string[]
  body: Record<string, unknown>
  target: string
}

/**
 * The one password-protected endpoint of each section (C-4 table). Sections
 * not listed have no password values: their buttons act alone (D-8 row 5).
 */
export const PROTECTED_ENDPOINTS: Readonly<Record<string, string>> = {
  providers: '/api/v1/integrations/providers/{id}',
  limits: '/api/v1/performance',
  protection: '/api/v1/security/sandbox-config',
  sandbox: '/api/v1/security/sandbox-config',
}

/** Write targets the sections name, and the endpoint each one writes. */
const TARGET_ENDPOINTS: Readonly<Record<string, string>> = {
  'sandbox-config': '/api/v1/security/sandbox-config',
  'rate-limits': '/api/v1/security/rate-limits',
  'prompt-guard': '/api/v1/security/prompt-guard',
  performance: '/api/v1/performance',
  context: '/api/v1/settings/context',
  'default-model': '/api/v1/providers/default-model',
  transcriber: '/api/v1/integrations/providers/{id}',
  config: '/api/v1/config',
  retention: '/api/v1/settings/retention',
  memory: '/api/v1/settings/memory',
  'third-party-agents': '/api/v1/settings/third-party-agents',
}

/** Where a section's values without a named target are written. */
const SECTION_DEFAULT_ENDPOINTS: Readonly<Record<string, string>> = {
  providers: '/api/v1/providers/default-model',
  memory: '/api/v1/settings/memory',
  'system-agents': '/api/v1/agents',
  'third-party-agents': '/api/v1/settings/third-party-agents',
  limits: '/api/v1/settings/context',
  protection: '/api/v1/security/rate-limits',
  sandbox: '/api/v1/config',
  keys: '/api/v1/credentials',
  gateway: '/api/v1/config',
  data: '/api/v1/settings/retention',
  about: '/api/v1/config',
}

function endpointFor(section: string, field: PlannedField): string {
  if (field.password) {
    const protectedEndpoint = PROTECTED_ENDPOINTS[section]
    if (!protectedEndpoint) {
      throw new Error(`Settings section "${section}" has no password-protected endpoint, but "${field.key}" is marked Password.`)
    }
    return protectedEndpoint
  }
  if (field.target && TARGET_ENDPOINTS[field.target]) return TARGET_ENDPOINTS[field.target]
  return SECTION_DEFAULT_ENDPOINTS[section] ?? '/api/v1/config'
}

/**
 * The writers that record a failed apply in GET /config/pending-restart
 * before they answer (C-6 write outcome table). Only for these may the
 * saved-state check conclude "not saved" or "changed somewhere else".
 */
const RECORDING_ENDPOINTS: ReadonlySet<string> = new Set([
  '/api/v1/config',
  '/api/v1/settings/context',
  '/api/v1/settings/third-party-agents',
  '/api/v1/security/prompt-guard',
  '/api/v1/security/rate-limits',
  '/api/v1/performance',
  // B1: a retention save that reached config.json but not the running
  // gateway is listed as apply_failed (C-6 outcome table).
  '/api/v1/settings/retention',
])

/** True when the endpoint this value is written to records failed applies (C-6). */
export function recordsFailedApplies(section: string, field: PlannedField): boolean {
  return RECORDING_ENDPOINTS.has(endpointFor(section, field))
}

/**
 * Plans the requests of one Save: one request per endpoint, holding every
 * changed value that endpoint takes. Password-marked values all go to the
 * section's one protected endpoint (C-4).
 */
export function planSectionSave(section: string, fields: readonly PlannedField[]): PlannedWrite[] {
  const writes = new Map<string, PlannedWrite>()
  for (const field of fields) {
    const endpoint = endpointFor(section, field)
    const target = field.password ? 'protected' : (field.target ?? 'default')
    const id = `${endpoint}#${target}`
    const write = writes.get(id) ?? { endpoint, method: 'PUT' as const, password: false, keys: [], body: {}, target: field.target ?? 'default' }
    write.password = write.password || field.password === true
    write.keys.push(field.key)
    write.body[field.key] = field.value
    writes.set(id, write)
  }
  return [...writes.values()]
}

/**
 * The FR-030 guard: every section reaches at most one protected endpoint.
 * `table` maps a section to the protected endpoints its Save would reach.
 */
export function assertOneProtectedEndpointPerSection(table: Readonly<Record<string, readonly string[]>>): void {
  for (const [section, endpoints] of Object.entries(table)) {
    const distinct = new Set(endpoints)
    if (distinct.size > 1) {
      throw new Error(
        `Settings section "${section}" would send password-marked values to more than one protected endpoint (${[...distinct].join(', ')}); one Save has one consent token.`,
      )
    }
  }
}
