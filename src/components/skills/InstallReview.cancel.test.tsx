// InstallReview.cancel.test.tsx — C-13 "Cancel", User Story 7 AS10, S-94 (last
// And). Written in Skills & Tools fix round 4 for CHECK round 4, candidate 21:
// the Go rows prove the server's DELETE and the staged copy's removal, not what
// the person sees and presses. Real page -> New skill -> Upload a skill -> the
// actual review -> Cancel; only fetch and time are controlled (the fixture is the
// one InstallReview.lostAnswer.test.tsx uses).
//   C-13 Cancel: "before Install is pressed, Cancel sends DELETE and closes;
//     'Nothing was installed.' is true. After Install is pressed, Cancel is
//     disabled until a terminal state is known."
//   S-94: "pressing Cancel before Install discards the staged copy and shows
//     'Nothing was installed.'"
//   US-7 AS10: "the staged copy is discarded and nothing is installed; after
//     Install is pressed Cancel is disabled until the answer."
// The spec does not say whether "Nothing was installed." is inline text or a
// toast, so it is accepted in either place — the sentence itself is exact.
// Missing page/review is a loud BLOCKED; GREEN and mutation belong to CHECK.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import type {
  AppState, PermissionSettings, Skill, SkillInstallReview, SkillInstallReviewStatus,
  SkillMarketplaceStatus, UploadFilesResponse,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'

const pages = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requirePage(): Promise<ComponentType> {
  const load = pages['/src/components/skills/SkillsToolsPage.tsx']
  if (!load) throw new Error('BLOCKED: native SkillsToolsPage/install review is not implemented — required by C-13 Cancel, US-7 AS10, S-94; never replace it with a test-local driver')
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
  review_id: 'cancel-review-001', name: 'weather', version: '2.0.0', revision: R2,
  verified: false, verification: 'no_registry_hash', suggested_category: 'research',
  replaces_existing: true, has_local_changes_to_replace: false,
  expires_at: '2026-11-22T12:15:00Z',
}
const SOURCE = 'media://cancel-weather-source-001'
const MARKDOWN = '---\nname: weather\ndescription: Cancel UI fixture.\nmetadata:\n  category: research\n---\n\nCANCEL-UPLOADED-R2\n'
const NOTHING_INSTALLED = 'Nothing was installed.'
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
  name: 'weather.md', path: 'uploads/cancel/weather.md', ref: SOURCE,
  size: new TextEncoder().encode(MARKDOWN).length, content_type: 'text/markdown',
}] }
const REVIEW_PATH = `/api/v1/skills/install/review/${REVIEW.review_id}`
const state = (value: SkillInstallReviewStatus['state'], extra: Partial<SkillInstallReviewStatus> = {}): SkillInstallReviewStatus => ({
  review_id: REVIEW.review_id, state: value, revision: R2, installed_revision: null, replaced_revision: null, ...extra,
})
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

// not-wire-format: transport observations only.
type Seen = { method: string; path: string }
let seen: Seen[]
let readStatus: () => Response
let client: QueryClient
const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
const calls = (method: string, path: string) => seen.filter((call) => call.method === method && call.path === path)
const installs = () => calls('POST', '/api/v1/skills/install')
const deletes = () => seen.filter((call) => call.method === 'DELETE')

// The sentence, as inline text or as a toast.
const nothingInstalledShown = () =>
  screen.queryByText(NOTHING_INSTALLED, { exact: false }) !== null ||
  useUiStore.getState().toasts.some((toast) => toast.message.includes(NOTHING_INSTALLED))
const clearToasts = () => { for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id) }

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-22T12:00:00Z'))
  seen = []
  readStatus = () => json(state('publishing'))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
  document.cookie = 'csrf=cancel-review; path=/'
  clearToasts()
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const path = new URL(input instanceof Request ? input.url : String(input), 'http://localhost').pathname
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    seen.push({ method, path })
    if (method === 'DELETE' && path === REVIEW_PATH) return new Response(null, { status: 204 })
    if (method === 'GET' && path === REVIEW_PATH) return readStatus()
    // The Install answer is lost: the review stays open and non-terminal.
    if (method === 'POST' && path === '/api/v1/skills/install') throw new TypeError('Install response lost')
    if (method === 'POST' && path === '/api/v1/skills/install/review') return json(REVIEW)
    if (method === 'POST' && path === '/api/v1/upload') return json(UPLOAD)
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
  clearToasts()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

// Page -> New skill -> Upload a skill -> the actual review is open.
async function openReview() {
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
  expect(calls('POST', '/api/v1/skills/install/review'), 'the upload was staged for review').toHaveLength(1)
  expect(screen.getByRole('button', { name: /^install skill$/i }), 'the review is open').toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^cancel$/i }), 'the review offers Cancel').toBeInTheDocument()
}

it('instrument only: the sentence probe sees inline text and a toast, and sees neither when absent', () => {
  expect(nothingInstalledShown(), 'absent to start').toBe(false)
  const planted = document.createElement('p')
  planted.textContent = NOTHING_INSTALLED
  document.body.appendChild(planted)
  expect(nothingInstalledShown(), 'planted inline text is seen').toBe(true)
  planted.remove()
  expect(nothingInstalledShown()).toBe(false)
  useUiStore.getState().addToast({ message: NOTHING_INSTALLED, variant: 'default' })
  expect(nothingInstalledShown(), 'a planted toast is seen').toBe(true)
  clearToasts()
  expect(nothingInstalledShown()).toBe(false)
})

describe('C-13 Cancel through the actual install review (US-7 AS10, S-94)', () => {
  it('before Install: Cancel sends one DELETE for this review, closes it, installs nothing and says "Nothing was installed."', async () => {
    await openReview()
    expect(deletes(), 'nothing is discarded before Cancel is pressed').toEqual([])
    expect(nothingInstalledShown(), 'the sentence is not there before Cancel').toBe(false)

    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))
    await tick()

    expect(deletes()).toEqual([{ method: 'DELETE', path: REVIEW_PATH }])
    expect(installs(), 'US-7 AS10: nothing is installed').toEqual([])
    expect(screen.queryByRole('button', { name: /^install skill$/i }), 'the review is closed').toBeNull()
    expect(nothingInstalledShown(), 'S-94: "Nothing was installed." is shown').toBe(true)
  })

  it('after Install is pressed: Cancel is disabled until a terminal state is known, and sends no DELETE', async () => {
    await openReview()
    fireEvent.click(screen.getByRole('button', { name: /^install skill$/i }))
    await tick()
    expect(installs(), 'Install was pressed once').toHaveLength(1)

    const cancel = () => screen.getByRole('button', { name: /^cancel$/i })
    expect(cancel(), 'C-13: Cancel is disabled once Install is pressed').toBeDisabled()
    fireEvent.click(cancel())
    await tick()
    expect(deletes(), 'a disabled Cancel discards nothing').toEqual([])

    // Still publishing after the first polls: still disabled, still no DELETE.
    await tick(2000)
    expect(calls('GET', REVIEW_PATH).length, 'the lost answer is being checked').toBeGreaterThanOrEqual(1)
    expect(cancel()).toBeDisabled()
    expect(deletes()).toEqual([])

    // A terminal state arrives: the person is told it installed, and Cancel never discarded anything.
    readStatus = () => json(state('installed', { installed_revision: R2, replaced_revision: R1 }))
    await tick(1000)
    expect(screen.getByText('Installed weather.', { exact: true })).toBeInTheDocument()
    expect(deletes()).toEqual([])
    expect(nothingInstalledShown(), 'it did install, so the Cancel sentence never shows').toBe(false)
  })
})
