// Order 44 / CHECK F19. C-16 and D12#1–11: exercise the actual page and its
// app-owned queue. Only fetch and the external clock are controlled. There is
// no local queue factory/driver, mocked autosave, queue state or response-less
// success. Missing SkillsToolsPage is loud RED, not a filename acceptance test.
// GREEN and mutation/proof-of-failability remain deferred to independent CHECK.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createElement, type ComponentType } from 'react'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type {
  ErrorResponse, PermissionSettings, PermissionSettingsPatch, Skill, SkillMarketplaceStatus, ToolRegistryEntry,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { request } from '@/lib/api/http'
import {
  PermissionSettings as PermissionSettingsSchema, Skill as SkillSchema,
  SkillMarketplaceStatus as SkillMarketplaceStatusSchema, ToolRegistryEntry as ToolRegistryEntrySchema,
} from '@/lib/api/generated/schemas'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requirePage(): Promise<ComponentType> {
  const load = pageModules['/src/components/skills/SkillsToolsPage.tsx']
  if (!load) throw new Error('BLOCKED: SkillsToolsPage and its C-16 app-level permission save queue are not implemented — required by S-83/S-117 and D12#1–11; bind the actual page, never a test-local queue')
  const module = await load()
  expect(typeof module.SkillsToolsPage, 'BLOCKED: actual SkillsToolsPage public export').toBe('function')
  return module.SkillsToolsPage as ComponentType
}

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(),
  useNavigate: () => navigate,
  useParams: () => ({}),
}))

const PERMISSIONS = '/api/v1/security/permissions'
const NOT_APPLIED = 'Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus.'
// The server's own 503 text: contracts/openapi.yaml PATCH /security/permissions
// 503 ("The error field is exactly ..."). The page maps it to NOT_APPLIED (C-16).
const CONTRACT_503 = 'The settings were saved, but could not be applied to running agents. Retry applying the change, or restart Omnipus.'
const NOT_STORED = 'Not saved: the settings file could not be written.'
const initial = (): PermissionSettings => ({
  policies: { bash: 'deny', read_file: 'allow' },
  skill_policies: { plan: 'deny', 'other-skill': 'ask' },
  visibility: { bash: 'full', read_file: 'full' },
  skill_visibility: { plan: 'name_and_description', 'other-skill': 'hidden' },
  skill_trust: 'ask',
})
const tools: ToolRegistryEntry[] = [
  { name: 'bash', description: 'Run a shell command.', scope: 'general', category: 'shell', source: 'builtin', visibility: 'full', visibility_locked: false, permission_fixed: false },
  { name: 'read_file', description: 'Read a file.', scope: 'general', category: 'filesystem', source: 'builtin', visibility: 'full', visibility_locked: false, permission_fixed: false },
]
const skills: Skill[] = [{
  id: 'plan', name: 'plan', description: 'Plan work.', version: '1.0.0', source: 'global',
  status: 'active', verified: true, trust_approved: false, has_local_changes: false,
  revision: 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee', category: 'business',
}]
const marketplace: SkillMarketplaceStatus = { enabled: false, registries: [] }
const full = (patch: Partial<PermissionSettings>): PermissionSettings => ({ ...initial(), ...patch })
const response = (status: number, body: PermissionSettings | ErrorResponse): Response =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

// These gates are only network responses. They never return a queue verdict,
// row state, toast or click result. Full stored state comes from the server edge.
function heldResponse() {
  let resolve!: (value: Response) => void
  let reject!: (reason: Error) => void
  let settled = false
  const promise = new Promise<Response>((yes, no) => { resolve = yes; reject = no })
  return {
    promise,
    release(status: number, body: PermissionSettings | ErrorResponse) { settled = true; resolve(response(status, body)) },
    fail() { settled = true; reject(new TypeError('F19 transport disconnected')) },
    close() { if (!settled) resolve(response(200, initial())) },
  }
}
type NetworkGate = ReturnType<typeof heldResponse>
// not-wire-format: records transport observations, not an API carrier.
type RequestSeen = { method: string; path: string; body?: PermissionSettingsPatch; keepalive?: boolean; at: number }
class PermissionTransport {
  stored = initial()
  seen: RequestSeen[] = []
  gates: { method: 'GET' | 'PATCH'; gate: NetworkGate; storedAfter?: PermissionSettings }[] = []
  allGates: NetworkGate[] = []
  inFlight = 0
  maxInFlight = 0
  hold(method: 'GET' | 'PATCH', storedAfter?: PermissionSettings) {
    const gate = heldResponse()
    this.gates.push({ method, gate, storedAfter })
    this.allGates.push(gate)
    return gate
  }
  patches() { return this.seen.filter((call) => call.method === 'PATCH') }
  fetch: typeof fetch = async (input, init) => {
    const path = new URL(input instanceof Request ? input.url : String(input), 'http://localhost').pathname
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    if (path !== PERMISSIONS) {
      if (method !== 'GET') throw new Error(`fixture: unexpected ${method} ${path}`)
      const bootstrap: Record<string, unknown> = {
        '/api/v1/tools': tools, '/api/v1/skills': skills, '/api/v1/mcp-servers': [],
        '/api/v1/providers': [], '/api/v1/skills/marketplace': marketplace,
      }
      if (!(path in bootstrap)) throw new Error(`fixture: unexpected GET ${path}`)
      return new Response(JSON.stringify(bootstrap[path]), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    this.seen.push({ method, path, body: init?.body ? JSON.parse(String(init.body)) as PermissionSettingsPatch : undefined, keepalive: init?.keepalive, at: Date.now() })
    if (method === 'PATCH') { this.inFlight++; this.maxInFlight = Math.max(this.maxInFlight, this.inFlight) }
    try {
      const next = this.gates[0]
      // Ordinary mount/focus reads do not consume the next planned write. A
      // deliberately held confirmation GET still MUST precede the next PATCH.
      if (!next || (method === 'GET' && next.method === 'PATCH')) {
        if (method !== 'GET') throw new Error('fixture: unexpected unplanned permission PATCH')
        return response(200, this.stored)
      }
      expect(method, 'actual request order must match the planned network edge').toBe(next.method)
      this.gates.shift()
      const reply = await next.gate.promise
      if (next.storedAfter) this.stored = next.storedAfter
      else if (reply.status === 200) this.stored = await reply.clone().json() as PermissionSettings
      return reply
    } finally {
      if (method === 'PATCH') this.inFlight--
    }
  }
}

let Page: ComponentType
let network: PermissionTransport
let client: QueryClient
const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-22T12:00:00Z'))
  navigate.mockClear()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  network = new PermissionTransport()
  vi.stubGlobal('fetch', network.fetch)
  document.cookie = 'csrf=f19-test; path=/'
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 0 }, mutations: { retry: false } } })
})
afterEach(async () => {
  cleanup()
  for (const gate of network.allGates) gate.close()
  await tick()
  client.clear()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

async function mountPage() {
  Page = await requirePage()
  // One provider/app lifetime, including when only the page unmounts.
  function App({ visible }: { visible: boolean }) {
    return createElement(QueryClientProvider, { client }, visible ? createElement(Page) : null)
  }
  const view = render(createElement(App, { visible: true }))
  await tick()
  return {
    leave: async () => { view.rerender(createElement(App, { visible: false })); await tick() },
    reopen: async () => { view.rerender(createElement(App, { visible: true })); await tick() },
  }
}
async function row(key: 'bash' | 'plan' | 'read_file') {
  // Tabs and group disclosures, not a direct call of a row component.
  const skill = key === 'plan'
  fireEvent.click(screen.getByRole('tab', { name: skill ? /^skills/i : /^omnipus tools/i }))
  await tick()
  const group = screen.getAllByRole('button', { name: skill ? /^business/i : key === 'bash' ? /^shell/i : /^files/i })
    .find((button) => button.hasAttribute('aria-expanded'))
  expect(group, 'the actual category disclosure is present').toBeDefined()
  if (group?.getAttribute('aria-expanded') === 'false') { fireEvent.click(group); await tick() }
  return screen.getByTestId(`${skill ? 'skill' : 'tool'}-row-${key}`)
}
async function choose(key: 'bash' | 'plan' | 'read_file', policy: 'allow' | 'ask' | 'deny') {
  const target = await row(key)
  fireEvent.click(within(target).getByRole('button', { name: new RegExp(`^${policy}$`, 'i') }))
  await tick()
  return target
}
function storedValue(target: HTMLElement) {
  return ['allow', 'ask', 'deny'].filter((value) => within(target).getByRole('button', { name: new RegExp(`^${value}$`, 'i') }).getAttribute('aria-pressed') === 'true')
}
function assertRequests(...bodies: PermissionSettingsPatch[]) {
  expect(network.patches().map(({ method, path, body, keepalive }) => ({ method, path, body, keepalive }))).toEqual(
    bodies.map((body) => ({ method: 'PATCH', path: PERMISSIONS, body, keepalive: true })),
  )
  expect(network.maxInFlight).toBe(1)
}

it('instrument only: bootstrap validates, real HTTP stays pending, then validates full server state', async () => {
  // Prevent an invalid bootstrap from hiding all queue observations after the
  // missing page lands. The negative control proves the revision check bites.
  expect(SkillSchema.safeParse({ ...skills[0], revision: 'f19-plan-r1' }).success).toBe(false)
  expect(SkillSchema.array().parse(skills)).toEqual(skills)
  expect(ToolRegistryEntrySchema.array().parse(tools)).toEqual(tools)
  expect(SkillMarketplaceStatusSchema.parse(marketplace)).toEqual(marketplace)
  const gate = network.hold('PATCH')
  const body: PermissionSettingsPatch = { policies: { bash: 'ask' } }
  let delivered = false
  const call = request('/security/permissions', {
    method: 'PATCH', body: JSON.stringify(body), keepalive: true,
  }, PermissionSettingsSchema).then((result) => { delivered = true; return result })
  await tick(1000)
  assertRequests(body)
  expect(delivered, 'the response gate must really hold the production transport').toBe(false)
  const stored = full({ policies: { bash: 'ask', read_file: 'deny' } })
  gate.release(200, stored)
  expect(await call).toEqual(stored)
  expect(delivered).toBe(true)
})

// No ran counter or dataset-size pseudo-test: each independent it owns an oracle.
describe('C-16 app-owned save queue through SkillsToolsPage (D12)', () => {
  it('D12#1: Saving by 100ms, request by 1000ms, no premature Saved, full response replaces stored maps', async () => {
    await mountPage()
    const gate = network.hold('PATCH')
    const start = Date.now()
    const target = await choose('bash', 'ask')
    // C-16 promises BY 100 ms, not the stronger unsupported 99 ms.
    await tick(100)
    expect(within(target).getByText('Saving…')).toBeInTheDocument()
    expect(within(target).queryByText('Saved')).not.toBeInTheDocument()
    await tick(900)
    assertRequests({ policies: { bash: 'ask' } })
    expect(network.patches()[0].at - start).toBeLessThanOrEqual(1000)
    expect(within(target).queryByText('Saved')).not.toBeInTheDocument()
    gate.release(200, full({ policies: { bash: 'ask', read_file: 'deny' } }))
    await tick()
    expect(within(target).getByText('Saved')).toBeInTheDocument()
    expect(storedValue(target)).toEqual(['ask'])
    expect(storedValue(await row('read_file'))).toEqual(['deny'])
    expect(network.stored).toEqual(full({ policies: { bash: 'ask', read_file: 'deny' } }))
  })
  it('D12#2: 503 is saved-not-applied; Retry sends exactly the same skill keys and values', async () => {
    await mountPage()
    // C-2: a 503 changes durable server state even though activation failed.
    const first = network.hold('PATCH', full({ skill_policies: { plan: 'ask', 'other-skill': 'ask' } }))
    const target = await choose('plan', 'ask')
    await tick(1000)
    first.release(503, { error: NOT_APPLIED })
    await tick()
    expect(within(target).getByText(NOT_APPLIED)).toBeInTheDocument()
    expect(storedValue(target)).toEqual(['ask'])
    const retry = network.hold('PATCH')
    fireEvent.click(within(target).getByRole('button', { name: /^retry$/i }))
    await tick(1000)
    assertRequests({ skill_policies: { plan: 'ask' } }, { skill_policies: { plan: 'ask' } })
    retry.release(200, full({ skill_policies: { plan: 'ask', 'other-skill': 'ask' } }))
    await tick()
    expect(within(target).getByText('Saved')).toBeInTheDocument()
    expect(storedValue(target)).toEqual(['ask'])
  })
  it('D12#3: refusal waits for fresh GET, shows exact reason and actual stored tool value with Retry', async () => {
    await mountPage()
    const save = network.hold('PATCH'), check = network.hold('GET')
    const target = await choose('bash', 'allow')
    await tick(1000)
    save.release(400, { error: 'invalid policy value' })
    await tick()
    expect(within(target).getByText('Not saved: invalid policy value.')).toBeInTheDocument()
    check.release(200, initial())
    await tick()
    expect(storedValue(target)).toEqual(['deny'])
    expect(within(target).getByRole('button', { name: /^retry$/i })).toBeInTheDocument()
    expect(network.seen.slice(-2).map(({ method }) => method)).toEqual(['PATCH', 'GET'])
    assertRequests({ policies: { bash: 'allow' } })
  })
  it('D12#4: dropped response says Checking, then the confirmed stored skill value, never Not saved', async () => {
    await mountPage()
    const save = network.hold('PATCH'), check = network.hold('GET')
    const target = await choose('plan', 'allow')
    await tick(1000)
    save.fail(); await tick()
    expect(within(target).getByText("Couldn't confirm the save. Checking…")).toBeInTheDocument()
    expect(within(target).queryByText(/^Not saved:/)).not.toBeInTheDocument()
    check.release(200, full({ skill_policies: { plan: 'ask', 'other-skill': 'ask' } }))
    await tick()
    expect(storedValue(target)).toEqual(['ask'])
    expect(within(target).getByText('Showing what is stored now.')).toBeInTheDocument()
    assertRequests({ skill_policies: { plan: 'allow' } })
  })
  it('D12#5: a 5s first request holds the second click at +100ms, not its Saving mark', async () => {
    await mountPage()
    const first = network.hold('PATCH'), second = network.hold('PATCH')
    const start = Date.now()
    await choose('bash', 'ask')
    await tick(100)
    const target = await choose('plan', 'allow')
    await tick(100)
    expect(within(target).getByText('Saving…')).toBeInTheDocument()
    await tick(800)
    assertRequests({ policies: { bash: 'ask' } })
    const sentAt = network.patches()[0].at
    expect(sentAt - start).toBeLessThanOrEqual(1000)
    // D12#5 says the RESPONSE takes 5 s, not that dispatch was immediate.
    await tick(sentAt + 4999 - Date.now())
    assertRequests({ policies: { bash: 'ask' } })
    await tick(1)
    const releasedAt = Date.now()
    first.release(200, full({ policies: { bash: 'ask', read_file: 'allow' } }))
    // The queued request may start only after release. Do not invent a
    // zero-ms scheduler/handoff API; let the C-16 dispatch window elapse.
    await tick(1000)
    assertRequests({ policies: { bash: 'ask' } }, { skill_policies: { plan: 'allow' } })
    expect(network.patches()[1].at).toBeGreaterThanOrEqual(releasedAt)
    second.release(200, full({ policies: { bash: 'ask', read_file: 'allow' }, skill_policies: { plan: 'allow', 'other-skill': 'ask' } }))
    await tick()
    expect(within(target).getByText('Saved')).toBeInTheDocument()
  })
  for (const firstOutcome of ['unknown', 'refused', 'not stored'] as const) {
    for (const outcome of ['answers', 'fails'] as const) it(`D12#6/C-16: second click waits until ${firstOutcome} request's GET ${outcome}`, async () => {
      await mountPage()
      const first = network.hold('PATCH'), check = network.hold('GET')
      await choose('plan', 'allow'); await tick(1000)
      if (firstOutcome === 'unknown') first.fail()
      else first.release(firstOutcome === 'refused' ? 400 : 500, { error: firstOutcome === 'refused' ? 'unknown key' : NOT_STORED })
      await tick()
      const second = network.hold('PATCH')
      const target = await choose('bash', 'ask')
      await tick(1000)
      assertRequests({ skill_policies: { plan: 'allow' } })
      expect(within(target).getByText('Saving…')).toBeInTheDocument()
      const checkedAt = Date.now()
      if (outcome === 'answers') check.release(200, initial()); else check.fail()
      await tick(1000)
      expect(network.patches()[1]?.at).toBeGreaterThanOrEqual(checkedAt)
      expect(network.seen.slice(-3).map(({ method }) => method)).toEqual(['PATCH', 'GET', 'PATCH'])
      assertRequests({ skill_policies: { plan: 'allow' } }, { policies: { bash: 'ask' } })
      second.release(200, full({ policies: { bash: 'ask', read_file: 'allow' } })); await tick()
      expect(within(target).getByText('Saved')).toBeInTheDocument()
    })
  }
  for (const status of [503, 400, 500] as const) it(`D12#${status === 503 ? 7 : status === 400 ? 8 : 10}: late ${status} has one exact toast/action and survives page unmount/reopen`, async () => {
    const app = await mountPage()
    const save = network.hold('PATCH', status === 503 ? full({ skill_policies: { plan: 'allow', 'other-skill': 'ask' } }) : undefined)
    await choose('plan', 'allow'); await tick(1000)
    await app.leave()
    save.release(status, { error: status === 503 ? NOT_APPLIED : status === 400 ? 'unknown key' : NOT_STORED })
    await tick()
    const message = status === 503 ? 'A change on Skills & Tools was saved but is not applied to running agents yet.'
      : `A change on Skills & Tools was not saved: ${status === 400 ? 'unknown key' : 'the settings file could not be written'}.`
    expect(useUiStore.getState().toasts.map((toast) => ({ message: toast.message, action: toast.action?.label }))).toEqual([{ message, action: 'Open Skills & Tools' }])
    await app.reopen()
    const target = await row('plan')
    expect(within(target).getByRole('button', { name: /^retry$/i })).toBeInTheDocument()
    expect(storedValue(target)).toEqual([status === 503 ? 'allow' : 'deny'])
    assertRequests({ skill_policies: { plan: 'allow' } })
  })
  for (const failedCheck of [false, true]) it(`C-16 late unknown${failedCheck ? ' and failed check' : ''} has the exact single recovery toast`, async () => {
    const app = await mountPage()
    const save = network.hold('PATCH'), check = network.hold('GET')
    await choose('bash', 'allow'); await tick(1000)
    await app.leave()
    save.fail(); await tick()
    if (failedCheck) check.fail(); else check.release(200, initial())
    await tick()
    expect(useUiStore.getState().toasts.map((toast) => ({ message: toast.message, action: toast.action?.label }))).toEqual([{
      message: "Couldn't confirm a change on Skills & Tools. Open it to see what is stored.", action: 'Open Skills & Tools',
    }])
    expect(network.seen.slice(-2).map(({ method }) => method)).toEqual(['PATCH', 'GET'])
    assertRequests({ policies: { bash: 'allow' } })
    await app.reopen()
    expect(storedValue(await row('bash'))).toEqual(['deny'])
  })
  it('D12#9: failed confirmation never calls clicked value stored; Retry reads rather than blindly PATCHing', async () => {
    await mountPage()
    const save = network.hold('PATCH'), check = network.hold('GET')
    const target = await choose('bash', 'allow'); await tick(1000)
    save.fail(); await tick(); check.fail(); await tick()
    expect(within(target).getByText("Couldn't load what is stored.")).toBeInTheDocument()
    expect(within(target).queryByText('Saved')).not.toBeInTheDocument()
    expect(storedValue(target), 'an unconfirmed click must not be shown as stored').not.toEqual(['allow'])
    const retry = network.hold('GET')
    fireEvent.click(within(target).getByRole('button', { name: /^retry$/i })); await tick()
    assertRequests({ policies: { bash: 'allow' } })
    expect(network.seen.at(-1)?.method).toBe('GET')
    retry.release(200, initial()); await tick()
    expect(storedValue(target)).toEqual(['deny'])
    expect(within(target).getByText('Showing what is stored now.')).toBeInTheDocument()
  })
  it('D12#10: 500 has exact settings-file sentence and actual stored value after fresh GET', async () => {
    await mountPage()
    const save = network.hold('PATCH'), check = network.hold('GET')
    const target = await choose('bash', 'allow'); await tick(1000)
    save.release(500, { error: NOT_STORED }); await tick()
    expect(within(target).getByText('Not saved: the settings file could not be written.')).toBeInTheDocument()
    check.release(200, initial()); await tick()
    expect(storedValue(target)).toEqual(['deny'])
    expect(within(target).getByRole('button', { name: /^retry$/i })).toBeInTheDocument()
    assertRequests({ policies: { bash: 'allow' } })
  })
  it('D12#11: hold a different sent key so both bash clicks are demonstrably queued; only later bash value is sent', async () => {
    await mountPage()
    const first = network.hold('PATCH'), second = network.hold('PATCH')
    await choose('plan', 'allow'); await tick(1000)
    assertRequests({ skill_policies: { plan: 'allow' } })
    await choose('bash', 'ask'); await tick(100)
    const target = await choose('bash', 'deny'); await tick(1000)
    expect(within(target).getByText('Saving…')).toBeInTheDocument()
    expect(storedValue(target)).toEqual(['deny'])
    assertRequests({ skill_policies: { plan: 'allow' } })
    first.release(200, full({ skill_policies: { plan: 'allow', 'other-skill': 'ask' } })); await tick(1000)
    assertRequests({ skill_policies: { plan: 'allow' } }, { policies: { bash: 'deny' } })
    second.release(200, full({ skill_policies: { plan: 'allow', 'other-skill': 'ask' } })); await tick()
    expect(within(target).getByText('Saved')).toBeInTheDocument()
  })
})

// Split from the describe above so that neither callback passes the 240-line
// function budget (scripts/check-function-budget.sh). Both describes drive the
// same page through the same module-level helpers and hooks; nothing is local
// to either of them.
describe('C-16 app-owned save queue through SkillsToolsPage: cases ported from retired legacy suites, and page ownership', () => {
  // CHECK round 4, candidate 11: the apply/recovery invariants of the retired
  // SecuritySection.policy-activation.test (cases 1 and 2, RR1 pr-test F3 / SFH
  // N1) move onto the page's app-owned queue. That suite drove the legacy
  // PUT /security/tool-policies editor, which C-22 row 6 retires; these two
  // drive the replacement, PATCH /security/permissions through the same real
  // request() boundary. Its other two cases are not carried as such: the
  // dropped response is D12#4 and D12#9 above, and a 503 that is not the
  // handler's has no meaning in C-2 (the contract gives 503 one meaning) — see
  // the report's spec-gap list.
  it('C-16 (legacy case 1): a 503 row shows exactly the C-16 sentence and none of the body details', async () => {
    await mountPage()
    // The contract's 503 body is { error } (PATCH /security/permissions 503);
    // ErrorResponse also allows `details`, so a body that carries an internal
    // reload reason is a legal input. Nothing in the spec names `details` as
    // text for the person: the 503 state is the exact C-16 sentence.
    const INTERNAL = 'INTERNAL_RELOAD_ONLY: registry creation failed: PRIVATE_OPERATOR_CONTEXT'
    const body: ErrorResponse = { error: CONTRACT_503, details: { reload_error: INTERNAL } }
    expect(JSON.stringify(body), 'planted-present control: the body does carry the internal reason').toContain(INTERNAL)
    // C-2: a 503 stores the change, so the server holds Ask afterwards.
    const save = network.hold('PATCH', full({ policies: { bash: 'ask', read_file: 'allow' } }))
    const target = await choose('bash', 'ask'); await tick(1000)
    save.release(503, body); await tick()
    expect(within(target).getByText(NOT_APPLIED)).toBeInTheDocument()
    expect(within(target).getByRole('button', { name: /^retry$/i })).toBeInTheDocument()
    expect(within(target).queryByText('Saved')).not.toBeInTheDocument()
    expect(storedValue(target)).toEqual(['ask'])
    expect(document.body.innerHTML, 'no part of the body details reaches the page').not.toContain(INTERNAL)
    assertRequests({ policies: { bash: 'ask' } })
  })
  it('C-16 (legacy case 2): a change queued behind a 503 that stored the first value is still sent, even when it equals the value stored before that save', async () => {
    await mountPage()
    // bash is stored as deny. Allow is sent and answered 503, which C-2 and
    // D12#2 say STORES allow. Deny is clicked while that request is in flight:
    // it equals the value stored BEFORE the 503'd save, so a page that
    // compared the click with its cached copy would drop it and leave allow
    // stored behind a row that shows Deny. "Later changes after a failure are
    // still sent in order" (C-16).
    const first = network.hold('PATCH', full({ policies: { bash: 'allow', read_file: 'allow' } }))
    const second = network.hold('PATCH')
    const target = await choose('bash', 'allow'); await tick(1000)
    assertRequests({ policies: { bash: 'allow' } })
    await choose('bash', 'deny'); await tick(1000)
    expect(within(target).getByText('Saving…')).toBeInTheDocument()
    assertRequests({ policies: { bash: 'allow' } })
    first.release(503, { error: CONTRACT_503 }); await tick(1000)
    expect(network.stored.policies.bash, 'the 503 stored allow').toBe('allow')
    assertRequests({ policies: { bash: 'allow' } }, { policies: { bash: 'deny' } })
    expect(within(target).getByText('Saving…')).toBeInTheDocument()
    second.release(200, full({ policies: { bash: 'deny', read_file: 'allow' } })); await tick()
    expect(within(target).getByText('Saved')).toBeInTheDocument()
    expect(storedValue(target)).toEqual(['deny'])
    expect(network.stored.policies).toEqual({ bash: 'deny', read_file: 'allow' })
  })
  // Part C item 1 (CHECK round 4, candidate 22): the retired SkillTrustSection.test
  // case 'shows SaveStatus "Saving…" while mutation is in flight' is still
  // applicable. FR-007 names the trust value among those that save on their own:
  // "Saving…" within 100 ms of the click, the request within 1,000 ms when nothing
  // is pending, and "Saved" only when that change is acknowledged (C-16, "queued:
  // the clicked value, marked Saving…"). Each click is its own PATCH naming only its
  // own key. The spec does not say whether the signal sits inside the trust block or
  // in a page-level region, so it is looked up on the page; the same probe sees
  // "Saved" after the answer, so the "not Saved yet" check is not vacuous.
  it('FR-007 (legacy SkillTrustSection case): a trust click shows "Saving…" within 100 ms, sends one PATCH of the trust key, and shows "Saved" only after that PATCH is acknowledged', async () => {
    await mountPage()
    fireEvent.click(screen.getByRole('tab', { name: /^skills/i })); await tick()
    const block = screen.getByTestId('trust-block')
    const save = network.hold('PATCH')
    // The trust block's choices are radios (spec "Accessibility and Keyboard"), not toggle buttons: a radio reads
    // aria-checked where a badge reads aria-pressed.
    fireEvent.click(within(block).getByRole('radio', { name: /^deny$/i }))
    await tick(100)
    expect(screen.queryAllByText('Saving…').length, '"Saving…" is shown within 100 ms of the click').toBeGreaterThan(0)
    await tick(900)
    assertRequests({ skill_trust: 'deny' })
    expect(screen.queryAllByText('Saved'), 'not "Saved" while the PATCH is unanswered').toHaveLength(0)
    save.release(200, full({ skill_trust: 'deny' })); await tick()
    expect(screen.queryAllByText('Saved').length, '"Saved" once the PATCH is acknowledged').toBeGreaterThan(0)
    expect(screen.queryAllByText('Saving…'), 'nothing is saving any more').toHaveLength(0)
    expect(within(block).getByRole('radio', { name: /^deny$/i })).toHaveAttribute('aria-checked', 'true')
    expect(network.stored.skill_trust).toBe('deny')
  })
  it('ownership: a reopened page joins the same in-flight app queue before its first response', async () => {
    const app = await mountPage()
    const first = network.hold('PATCH'), second = network.hold('PATCH')
    await choose('plan', 'allow'); await tick(1000)
    await app.leave(); await app.reopen()
    const target = await choose('bash', 'ask'); await tick(1000)
    assertRequests({ skill_policies: { plan: 'allow' } })
    expect(within(target).getByText('Saving…')).toBeInTheDocument()
    first.release(200, full({ skill_policies: { plan: 'allow', 'other-skill': 'ask' } })); await tick(1000)
    assertRequests({ skill_policies: { plan: 'allow' } }, { policies: { bash: 'ask' } })
    second.release(200, full({ policies: { bash: 'ask', read_file: 'allow' }, skill_policies: { plan: 'allow', 'other-skill': 'ask' } })); await tick()
    expect(within(target).getByText('Saved')).toBeInTheDocument()
  })
})
