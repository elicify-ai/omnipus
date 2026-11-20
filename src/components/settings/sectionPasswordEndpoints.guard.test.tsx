/**
 * sectionPasswordEndpoints.guard.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 23 (FR-030, Dataset D-8; BDD "Each section
 * sends its password-marked values to at most one protected endpoint").
 *
 * Oracles (spec, never code):
 *   - D-8: with every password-marked value of a section changed, the Save
 *     sends EXACTLY ONE protected write, to the section's one protected
 *     endpoint — Providers: 1 × PUT /integrations/providers/{id}; Limits:
 *     1 × PUT /performance; Protection: 1 × PUT /security/sandbox-config;
 *     Sandbox & network: 1 × PUT /security/sandbox-config; Memory & recaps,
 *     System agents, Third-party agents, Keys & secrets, Gateway, Data &
 *     retention, About: 0 protected writes (their buttons act alone).
 *   - FR-030: the guard runs the REAL save planner and FAILS when any
 *     section's password-marked values reach more than one protected
 *     endpoint; an INJECTED second protected endpoint must make it fail
 *     (positive control — the guard must be able to fail).
 *
 * The save planner does not exist yet — BLOCKED-import pattern. Proposed
 * module contract (GREEN may adjust names, not oracles):
 * `@/components/settings/savePlanner` exports `planSectionSave(section,
 * fields)` → PlannedWrite[] ({ endpoint, method, password, body }) and
 * `assertOneProtectedEndpointPerSection(table)`.
 * Counts for the four password sections are ALSO taken at the network mock
 * while the real planner runs (D-8 note), through the SaveBar; sections with
 * no password values are counted at the planner level only (their screens
 * need full data mocks — see TEST-PLAN-PART2.md gaps).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

async function loadSavePlanner(): Promise<any> {
  try {
    return await vi.importActual('@/components/settings/savePlanner')
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings save planner does not exist yet — ' +
        'required by FR-030 (the guard runs the real save planner and counts protected endpoints per section)',
    )
  }
}

async function loadSaveBar(): Promise<any> {
  try {
    const mod: any = await vi.importActual('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings SaveBar component does not exist yet — ' +
        'required by FR-030 (network-mock counting while the real planner runs)',
    )
  }
}

// D-8 sections and their password-marked rows (FR-025 badge list; C-4 table).
const D8_PASSWORD_SECTIONS: Array<{ section: string; endpoint: string; fields: any[] }> = [
  {
    section: 'providers',
    endpoint: '/api/v1/integrations/providers/',
    fields: [{ key: 'integrations.transcriber.openai_whisper', label: 'Active transcriber', value: 'groq', kind: 'select', options: ['openai_whisper', 'groq'], password: true }],
  },
  {
    section: 'limits',
    endpoint: '/api/v1/performance',
    fields: [
      { key: 'performance.max_parallel_agents', label: 'Max parallel agents', value: 4, kind: 'text', password: true },
      { key: 'agents.defaults.max_tool_iterations', label: 'Max tool calls per turn', value: 40, kind: 'text', password: true },
      { key: 'planning.goal_max_rounds', label: 'Tries per goal', value: 8, kind: 'text', password: true },
      { key: 'planning.goal_reminder_quiet_seconds', label: 'Idle reminder quiet window', value: 120, kind: 'text', password: true },
      { key: 'planning.goal_stalled_checkin_enabled', label: 'Check-in switch', value: true, kind: 'switch', password: true },
      { key: 'planning.goal_stalled_checkin_minutes', label: 'Check-in interval', value: 10, kind: 'text', password: true },
    ],
  },
  {
    section: 'protection',
    endpoint: '/api/v1/security/sandbox-config',
    fields: [{ key: 'sandbox.auto_approve', label: 'Auto-approve', value: true, kind: 'switch', password: true }],
  },
  {
    section: 'sandbox',
    endpoint: '/api/v1/security/sandbox-config',
    fields: [
      { key: 'sandbox.mode', label: 'Sandbox mode', value: 'enforce', kind: 'select', options: ['off', 'permissive', 'enforce'], password: true, restart: true },
      { key: 'sandbox.filesystem_model', label: 'Filesystem model', value: 'open', kind: 'select', options: ['confined', 'open'], password: true, restart: true },
      { key: 'sandbox.workspace_path_guard', label: 'Shell workspace limit', value: true, kind: 'switch', password: true, restart: true },
      { key: 'sandbox.allowed_paths', label: 'Allowed folders', value: ['/srv/data'], kind: 'paths', password: true, restart: true },
      { key: 'sandbox.ssrf.allow_internal', label: 'Internal network addresses', value: ['10.0.0.0/8'], kind: 'paths', password: true },
    ],
  },
]

// D-8 row 5: sections with no password values (their buttons act alone).
const D8_NO_PASSWORD_SECTIONS = [
  'memory',
  'system-agents',
  'third-party-agents',
  'keys',
  'gateway',
  'data',
  'about',
]

function stubReauthFetch() {
  return vi.stubGlobal(
    'fetch',
    vi.fn(async (input: any) => {
      const url = String(input)
      if (url.includes('/auth/reauth')) {
        return { ok: true, status: 200, json: async () => ({ token: 'rt' }) }
      }
      return { ok: true, status: 200, json: async () => ({}) }
    }),
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})

describe('D-8 — the real save planner: at most one protected endpoint per section', () => {
  it.each(D8_PASSWORD_SECTIONS.map((s) => [s.section, s] as const))(
    '%s: exactly one protected write to its one endpoint',
    async (_name, spec) => {
      const planner: any = await loadSavePlanner()
      const writes: any[] = planner.planSectionSave(spec.section, spec.fields)
      const protectedWrites = writes.filter((w) => w.password)
      expect(protectedWrites.length, `BLOCKED RED (D-8): ${spec.section} must send exactly one protected write`).toBe(1)
      expect(protectedWrites[0].endpoint).toContain(spec.endpoint)
    },
  )

  it.each(D8_NO_PASSWORD_SECTIONS.map((s) => [s] as const))(
    '%s: no protected write at all (D-8 row 5)',
    async (section) => {
      const planner: any = await loadSavePlanner()
      const writes: any[] = planner.planSectionSave(section, [
        { key: `${section}.example`, label: 'Example', value: 'changed', kind: 'text' },
      ])
      expect(writes.filter((w) => w.password).length,
        `BLOCKED RED (D-8 row 5): ${section} carries no password values`).toBe(0)
    },
  )

  it('the whole planner table passes the guard (every section has at most one protected endpoint)', async () => {
    const planner: any = await loadSavePlanner()
    expect(() => {
      for (const spec of D8_PASSWORD_SECTIONS) {
        planner.assertOneProtectedEndpointPerSection({ [spec.section]: [spec.endpoint] })
      }
      for (const section of D8_NO_PASSWORD_SECTIONS) {
        planner.assertOneProtectedEndpointPerSection({ [section]: [] })
      }
    }).not.toThrow()
  })
})

describe('FR-030 positive control — an injected second protected endpoint must fail the guard', () => {
  it('a section with two protected endpoints makes the guard fail', async () => {
    const planner: any = await loadSavePlanner()
    const injected = { gateway: ['/api/v1/config', '/api/v1/security/sandbox-config'] }
    expect(() => planner.assertOneProtectedEndpointPerSection(injected)).toThrow(
      /more than one protected endpoint|at most one protected endpoint/i,
    )
  })
})

describe('D-8 note — counts taken at the network mock while the real planner runs', () => {
  it.each(D8_PASSWORD_SECTIONS.map((s) => [s.section, s] as const))(
    '%s: saving every password-marked change sends exactly one protected request',
    async (_name, spec) => {
      stubReauthFetch()
      const SaveBar = await loadSaveBar()
      render(<SaveBar section={spec.section} fields={spec.fields} onSave={vi.fn()} onDiscard={vi.fn()} />)
      for (const f of spec.fields) {
        if (f.kind === 'select') {
          await userEvent.selectOptions(screen.getByLabelText(f.label), String(f.options[1]))
        } else if (f.kind === 'paths') {
          await userEvent.type(screen.getByLabelText(f.label), '/x')
        } else {
          await userEvent.click(screen.getByRole('switch', { name: f.label }))
        }
      }
      await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
      await userEvent.type(screen.getByLabelText(/password/i), 'correct horse')
      await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
      const fetchMock = vi.mocked(globalThis.fetch)
      const calls = fetchMock.mock.calls.map(([u]) => String(u))
      const protectedCalls = calls.filter((u) => u.includes(spec.endpoint))
      expect(protectedCalls.length,
        `BLOCKED RED (D-8 note): ${spec.section} must reach ${spec.endpoint} exactly once`).toBe(1)
    },
  )
})
