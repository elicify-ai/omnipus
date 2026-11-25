// Order 53 / CHECK F20. C-13, S-115, D15: real page -> Upload a skill ->
// actual review -> Install -> lost response -> actual polling/recovery UI.
// Only fetch, external time and router navigation are controlled. Full generated
// carriers use state/revision, never legacy status/version/trust vocabulary.
// Missing page/review is loud RED; GREEN/mutation remain deferred to CHECK.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import type {
  AppState, ErrorResponse, PermissionSettings, Skill, SkillInstallRequest,
  SkillInstallReview, SkillInstallReviewStatus, SkillMarketplaceStatus, UploadFilesResponse,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import {
  PermissionSettings as PermissionSettingsSchema, Skill as SkillSchema,
  SkillInstallReview as ReviewSchema, SkillInstallReviewStatus as StatusSchema,
  UploadFilesResponse as UploadSchema,
} from '@/lib/api/generated/schemas'

const pages = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requirePage(): Promise<ComponentType> {
  const load = pages['/src/components/skills/SkillsToolsPage.tsx']
  if (!load) throw new Error('BLOCKED: native SkillsToolsPage/install review is not implemented — required by C-13/S-115/D15; never replace it with a test-local recovery driver')
  const module = await load()
  expect(typeof module.SkillsToolsPage, 'BLOCKED: actual SkillsToolsPage public export').toBe('function')
  return module.SkillsToolsPage as ComponentType
}
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(), useNavigate: () => vi.fn(), useParams: () => ({}),
}))

const R1 = '1111111111111111111111111111111111111111111111111111111111111111'
const R2 = '2222222222222222222222222222222222222222222222222222222222222222'
const REVIEW: SkillInstallReview = {
  review_id: 'f20-review-001', name: 'weather', version: '2.0.0', revision: R2,
  verified: false, verification: 'no_registry_hash', suggested_category: 'research',
  replaces_existing: true, has_local_changes_to_replace: false,
  expires_at: '2026-11-22T12:15:00Z',
}
const SOURCE = 'media://f20-weather-source-001'
const MARKDOWN = '---\nname: weather\ndescription: F20 UI fixture.\nmetadata:\n  category: research\n---\n\nF20-UPLOADED-R2\n'
const UNKNOWN = "Couldn't confirm the install. Open Skills to see what is installed."
const CHECKING = 'Could not confirm the install. Checking…'
const OLD: Skill = {
  id: 'weather', name: 'weather', description: 'Old weather remains installed.', version: '1.0.0',
  revision: R1, category: 'research', source: 'global', status: 'active',
  verified: false, trust_approved: false, has_local_changes: false,
}
const PERMISSIONS: PermissionSettings = {
  policies: {}, skill_policies: { weather: 'ask' }, visibility: {}, skill_visibility: { weather: 'name_and_description' }, skill_trust: 'ask',
}
const APP_STATE: AppState = { onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } }
const MARKETPLACE: SkillMarketplaceStatus = { enabled: false, registries: [] }
const UPLOAD: UploadFilesResponse = { files: [{
  name: 'weather.md', path: 'uploads/f20/weather.md', ref: SOURCE,
  size: new TextEncoder().encode(MARKDOWN).length, content_type: 'text/markdown',
}] }
const STATUS = `/api/v1/skills/install/review/${REVIEW.review_id}`
const state = (value: SkillInstallReviewStatus['state'], extra: Partial<SkillInstallReviewStatus> = {}): SkillInstallReviewStatus => ({
  review_id: REVIEW.review_id, state: value, revision: R2, installed_revision: null, replaced_revision: null, ...extra,
})
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

// not-wire-format: actual transport observations, never queue/review state.
type Seen = { method: string; path: string; at: number; body?: SkillInstallRequest }
let seen: Seen[]
let readStatus: () => Response | Promise<Response>
let client: QueryClient
let epoch: number
const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
const reads = () => seen.filter((call) => call.method === 'GET' && call.path === STATUS)
const installs = () => seen.filter((call) => call.method === 'POST' && call.path === '/api/v1/skills/install')

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-22T12:00:00Z'))
  epoch = Date.now()
  seen = []
  readStatus = () => json(state('publishing'))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
  document.cookie = 'csrf=f20-review; path=/'
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const path = new URL(input instanceof Request ? input.url : String(input), 'http://localhost').pathname
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    const call: Seen = { method, path, at: Date.now() - epoch }
    if (init?.body && !(init.body instanceof FormData)) call.body = JSON.parse(String(init.body)) as SkillInstallRequest
    seen.push(call)
    if (method === 'GET' && path === STATUS) return readStatus()
    if (method === 'POST' && path === '/api/v1/skills/install') throw new TypeError('F20 Install response lost')
    if (method === 'POST' && path === '/api/v1/skills/install/review') return json(REVIEW)
    if (method === 'POST' && path === '/api/v1/upload') {
      expect(init?.body).toBeInstanceOf(FormData)
      const uploaded = (init?.body as FormData).getAll('files')
      expect(uploaded).toHaveLength(1)
      expect((uploaded[0] as File).name).toBe('weather.md')
      expect((uploaded[0] as File).size).toBe(UPLOAD.files[0].size)
      return json(UPLOAD)
    }
    const bootstrap: Record<string, unknown> = {
      '/api/v1/skills': [OLD], '/api/v1/tools': [], '/api/v1/security/permissions': PERMISSIONS,
      '/api/v1/mcp-servers': [], '/api/v1/providers': [], '/api/v1/skills/marketplace': MARKETPLACE, '/api/v1/state': APP_STATE,
    }
    if (method === 'GET' && path in bootstrap) return json(bootstrap[path])
    throw new Error(`fixture: unexpected ${method} ${path}`)
  }))
})
afterEach(async () => {
  cleanup()
  client.clear()
  await tick()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

async function mountAndInstall() {
  const Page = await requirePage()
  render(<QueryClientProvider client={client}><Page /></QueryClientProvider>)
  await tick()
  fireEvent.click(screen.getByRole('button', { name: /^new skill$/i })); await tick()
  fireEvent.click(screen.getByRole('menuitem', { name: 'Upload a skill' })); await tick()
  const inputs = document.querySelectorAll<HTMLInputElement>('input[type="file"]')
  expect(inputs.length, 'actual upload chooser, no fabricated review props').toBe(1)
  // A plain change event, not user-event: under vitest fake timers user.upload never returns
  // (RTL's asyncWrapper waits on a fake setTimeout that it advances only for jest), and the
  // timed-out test left the act environment off for every later test of the file.
  fireEvent.change(inputs[0], { target: { files: [new File([MARKDOWN], 'weather.md', { type: 'text/markdown' })] } })
  await tick()
  const staged = seen.filter((call) => call.method === 'POST' && call.path === '/api/v1/skills/install/review')
  expect(staged).toHaveLength(1)
  expect(staged[0].body?.upload_id).toBe(SOURCE)
  expect(staged[0].body?.slug).toBeUndefined()
  expect(staged[0].body?.review_id).toBeUndefined()
  fireEvent.click(screen.getByRole('button', { name: /^install skill$/i }))
  await tick()
  expect(installs().map((call) => call.body)).toEqual([{
    upload_id: SOURCE, review_id: REVIEW.review_id, category: 'research', revision: R1,
  }])
  // C-13 does not prescribe an immediate t=0 read. Permit that or the first
  // one-second tick, while bounding the initial wait to one polling interval.
  if (reads().length === 0) await tick(1000)
  expect(reads()).toHaveLength(1)
  const firstAt = reads()[0].at
  expect(firstAt).toBeGreaterThanOrEqual(installs()[0].at)
  expect(firstAt - installs()[0].at).toBeLessThanOrEqual(1000)
  return { firstAt }
}
function pollTimes(firstAt: number, through: number) {
  return Array.from({ length: Math.max(0, Math.floor((through - firstAt) / 1000) + 1) }, (_, n) => firstAt + n * 1000)
}
async function advanceTo(ms: number) {
  const remaining = ms - (Date.now() - epoch)
  expect(remaining, 'fixture time must never go backwards').toBeGreaterThanOrEqual(0)
  await tick(remaining)
}
function expectInstalled() {
  expect(screen.getByText('Installed weather.', { exact: true })).toBeInTheDocument()
  expect(screen.queryByText(UNKNOWN)).not.toBeInTheDocument()
}
function expectUnknown() {
  expect(screen.getByText(UNKNOWN)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^retry$/i })).toBeInTheDocument()
  expect(screen.queryByText('Installed weather.', { exact: true })).not.toBeInTheDocument()
}

it('instrument only: upload, review, old listing and every status use the actual generated carriers', () => {
  expect(UploadSchema.parse(UPLOAD)).toEqual(UPLOAD)
  expect(ReviewSchema.parse(REVIEW)).toEqual(REVIEW)
  expect(SkillSchema.parse(OLD)).toEqual(OLD)
  expect(PermissionSettingsSchema.parse(PERMISSIONS)).toEqual(PERMISSIONS)
  for (const value of ['staged', 'publishing', 'installed', 'failed', 'expired', 'cancelled'] as const) {
    const answer = state(value, value === 'installed' ? { installed_revision: R2, replaced_revision: R1 } : value === 'failed' ? { reason: 'the staged copy vanished' } : {})
    expect(StatusSchema.parse(answer)).toEqual(answer)
  }
  expect(ReviewSchema.safeParse({ ...REVIEW, revision: '2.0.0' }).success).toBe(false)
  expect(StatusSchema.safeParse({ ...state('installed'), state: undefined, status: 'installed' }).success).toBe(false)
})

describe('C-13 lost install answer through actual review UI (D15)', () => {
  it('D15#1: staged first, then expired at the next read; exact Not installed', async () => {
    readStatus = () => json(state(reads().length === 1 ? 'staged' : 'expired'))
    const { firstAt } = await mountAndInstall()
    expect(screen.getByText(CHECKING)).toBeInTheDocument()
    await advanceTo(firstAt + 1000)
    expect(screen.getByText('Not installed.', { exact: true })).toBeInTheDocument()
    expect(reads().map((call) => call.at)).toEqual([firstAt, firstAt + 1000])
    expect(screen.queryByText('Installed weather.', { exact: true })).not.toBeInTheDocument()
  })
  it('D15#2: installed means exact reviewed content revision, not the display version or stale R1 listing', async () => {
    readStatus = () => json(state('installed', { installed_revision: R2, replaced_revision: R1 }))
    const { firstAt } = await mountAndInstall()
    expectInstalled()
    expect(reads().map((call) => call.at)).toEqual([firstAt])
    expect(installs()).toHaveLength(1)
  })
  it('D15#3: all three seconds remain publishing; exact one-second cadence then Installed', async () => {
    readStatus = () => json(Date.now() - epoch - reads()[0].at < 3000 ? state('publishing') : state('installed', { installed_revision: R2, replaced_revision: R1 }))
    const { firstAt } = await mountAndInstall()
    expect(screen.getByText(CHECKING)).toBeInTheDocument()
    for (let second = 1; second <= 2; second++) {
      await advanceTo(firstAt + second * 1000)
      expect(screen.getByText(CHECKING)).toBeInTheDocument()
      expect(screen.queryByText('Installed weather.', { exact: true })).not.toBeInTheDocument()
      expect(reads().map((call) => call.at)).toEqual(pollTimes(firstAt, firstAt + second * 1000))
    }
    await advanceTo(firstAt + 2999)
    expect(screen.getByText(CHECKING)).toBeInTheDocument()
    expect(reads().map((call) => call.at)).toEqual(pollTimes(firstAt, firstAt + 2000))
    await tick(1)
    expectInstalled()
    expect(reads().map((call) => call.at)).toEqual(pollTimes(firstAt, firstAt + 3000))
    await tick(5000)
    expect(reads()).toHaveLength(4)
    expect(installs()).toHaveLength(1)
  })
  it('D15#4: no early cutoff; 60-second boundary stops automatic reads; Retry reads status, never resubmits', async () => {
    const { firstAt } = await mountAndInstall()
    const cutoff = installs()[0].at + 60_000
    await advanceTo(cutoff - 1)
    expect(screen.getByText(CHECKING)).toBeInTheDocument()
    const beforeCutoff = pollTimes(firstAt, cutoff - 1)
    expect(reads().map((call) => call.at)).toEqual(beforeCutoff)
    await tick(1)
    expectUnknown()
    // C-13 does not say whether to send the final read exactly at cutoff.
    // Either permitted sequence is exact; no arbitrary extras/different cadence.
    const atCutoff = reads().map((call) => call.at)
    const withFinalRead = [...beforeCutoff, cutoff]
    if (atCutoff.length === beforeCutoff.length + 1) {
      expect(cutoff - beforeCutoff[beforeCutoff.length - 1]).toBe(1000)
      expect(atCutoff).toEqual(withFinalRead)
    } else expect(atCutoff).toEqual(beforeCutoff)
    await tick(5000)
    expect(reads().map((call) => call.at)).toEqual(atCutoff)
    readStatus = () => json(state('installed', { installed_revision: R2, replaced_revision: R1 }))
    const beforeRetry = reads().length
    fireEvent.click(screen.getByRole('button', { name: /^retry$/i })); await tick()
    expect(reads()).toHaveLength(beforeRetry + 1)
    expect(reads().at(-1)?.at).toBe(cutoff + 5000)
    expectInstalled()
    expect(installs()).toHaveLength(1)
  })
  it('D15#5: failed replacement shows the exact reason even though weather R1 is still listed', async () => {
    readStatus = () => json(state('failed', { reason: 'the staged copy vanished' }))
    const { firstAt } = await mountAndInstall()
    expect(screen.getByText('Not installed: the staged copy vanished.', { exact: true })).toBeInTheDocument()
    expect(screen.queryByText('Installed weather.', { exact: true })).not.toBeInTheDocument()
    expect(reads().map((call) => call.at)).toEqual([firstAt])
    expect(installs()).toHaveLength(1)
  })
  for (const kind of ['404', 'network'] as const) it(`D15#6: status ${kind} gives exact unknown text; Retry is a fresh status read`, async () => {
    readStatus = () => {
      if (kind === 'network') throw new TypeError('F20 status read failed')
      const error: ErrorResponse = { error: 'review not found' }
      return json(error, 404)
    }
    const { firstAt } = await mountAndInstall()
    expectUnknown()
    expect(reads().map((call) => call.at)).toEqual([firstAt])
    readStatus = () => json(state('installed', { installed_revision: R2, replaced_revision: R1 }))
    fireEvent.click(screen.getByRole('button', { name: /^retry$/i })); await tick()
    expect(reads()).toHaveLength(2)
    expectInstalled()
    expect(installs()).toHaveLength(1)
  })
  it('C-13: an installed result for older R1 never asserts success for the reviewed R2', async () => {
    readStatus = () => json(state('installed', { installed_revision: R1, replaced_revision: null }))
    await mountAndInstall()
    expect(reads()).toHaveLength(1)
    expect(screen.queryByText('Installed weather.', { exact: true })).not.toBeInTheDocument()
    expect(useUiStore.getState().toasts.filter((toast) => toast.message.startsWith('Installed weather.'))).toEqual([])
  })
  it('D15#8: cancelled status reads Not installed without another Install', async () => {
    readStatus = () => json(state('cancelled'))
    await mountAndInstall()
    expect(screen.getByText('Not installed.', { exact: true })).toBeInTheDocument()
    expect(reads()).toHaveLength(1)
    expect(installs()).toHaveLength(1)
  })
})
