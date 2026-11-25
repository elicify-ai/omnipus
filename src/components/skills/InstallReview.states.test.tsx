// InstallReview.states.test.tsx — the "Install review" row of the spec's "UI
// Screens and States" table (spec line 938), with C-13, C-3 and S-27. Written in
// Skills & Tools fix round 4, Part C, on the team-lead's ruling: "Install review
// and Approval card rows: write them this round ... They are spec requirements of
// this feature, and today nothing pins those strings."
// Real page -> New skill -> Upload a skill -> the actual review; only fetch and
// time are controlled (the harness is the one InstallReview.cancel.test.tsx uses).
//
// Each expected string below is the spec's own:
//   Loading  "Checking the skill…" while staging (Cancel usable, discards);
//            "Installing…" after Install (Cancel disabled).
//   Error    "server reason"; trust Deny refusal (C-13: "Trust changed between
//            review and Install: re-checked at publish; Deny → 403 (C-3 text)", and
//            S-28's text); "This review expired. Open the skill again to review
//            it." (410); "The skill changed after review. Review it again." (409).
//   Partial  "verification unavailable: 'Not verified: the catalog did not answer.'
//            and trust applies" (C-13 `unavailable` -> verified false).
//   Success  the toast "Installed <name>. It is set to Ask: an agent needs your yes
//            to use it unless Auto-approve is on. No agent is granted it yet; grant
//            it on an agent's Skills tab."
// Not repeated here, because other files already pin them: "Could not confirm the
// install. Checking…" and "Couldn't confirm the install…" (InstallReview.lostAnswer),
// "Nothing was installed." and Cancel-disabled-after-Install with a lost answer
// (InstallReview.cancel), S-27's trust Deny text (InstallReview.category).
//
// Where a sentence appears is not specified (inline in the review or as a toast),
// so each is accepted in either place, and a server sentence may carry a lead-in
// such as "Not installed: " — the sentence itself is exact. A refusal text is the
// server's own sentence (spec C-13), so the page must show it as sent.
// "Installing…" is read from the moment Install is pressed (the person's wait for
// the answer), not only after the answer: "after Install" in the spec row. A second,
// control test keeps it while the answered install is still `publishing`.
// The 422 case uses the upload route, which stands in for the catalog download: both
// go through POST /skills/install/review, and the page cannot tell the sources apart.
// Spec-silent, left out on purpose: whether Cancel during staging sends a DELETE for
// a review that arrives after it (not asserted; only "nothing is left to install");
// the placement and wording of a generic server reason.
//
// RED, shown by a run: ta/fix4-ts-fix4h-01.log (frontend head f69d7a7b7 plus this
// overlay, direct_exit=1, 5 failed | 9 passed). The five failures are the spec's own
// strings that the page does not show today:
//   "Checking the skill…" while staging           -> the staging wait is never named;
//   Cancel while staging                          -> no Cancel button exists until the
//     review opens ("expected null not to be null"; the rest of that test — Cancel
//     pressed, nothing installed, the late review never opens — did not run, so it is
//     unproven until a Cancel exists);
//   "Installing…" from the Install press          -> not shown while the answer is awaited
//     (the control below shows it once the answer is in and the status is `publishing`);
//   "Not verified: the catalog did not answer."   -> appears nowhere in the page;
//   the full success toast of the spec row        -> the page shows a shorter sentence.
// The nine that pass today are the instrument probe (it sees an inline sentence, a toast,
// and neither when absent), the `publishing` control, the five Error sentences shown as
// the server sent them (500, 403, 410, 409, and the 422 staging refusal) and the two
// Partial controls (`no_registry_hash`, `matched` do NOT say "Not verified"). Those nine
// are control evidence, not product proof.
// An earlier run of this file, fix4g-01 (13 failed | 1 passed), is NOT a result: it
// uploaded through user-event under fake timers, which never returns (see the comment in
// InstallReview.lostAnswer.test.tsx::mountAndInstall); this version sends a plain change
// event. Missing page/review is a loud BLOCKED; GREEN and mutation belong to CHECK.
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
  if (!load) throw new Error('BLOCKED: native SkillsToolsPage/install review is not implemented — required by the spec row "Install review" (UI Screens and States), C-13; never replace it with a test-local driver')
  const module = await load()
  expect(typeof module.SkillsToolsPage, 'BLOCKED: actual SkillsToolsPage public export').toBe('function')
  return module.SkillsToolsPage as ComponentType
}
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(), useNavigate: () => vi.fn(), useParams: () => ({}),
}))

const R1 = '1111111111111111111111111111111111111111111111111111111111111111'
const NAME = 'notes-helper'
const REVIEW: SkillInstallReview = {
  review_id: 'states-review-001', name: NAME, version: '1.0.0', revision: R1,
  verified: false, verification: 'no_registry_hash', suggested_category: 'research',
  replaces_existing: false, has_local_changes_to_replace: false,
  expires_at: '2026-11-22T12:15:00Z',
}
const SOURCE = 'media://states-notes-helper-source-001'
const MARKDOWN = `---\nname: ${NAME}\ndescription: States UI fixture.\nmetadata:\n  category: research\n---\n\nSTATES-UPLOADED\n`
const INSTALLED: Skill = {
  id: NAME, name: NAME, description: 'States UI fixture.', version: '1.0.0', revision: R1,
  category: 'research', source: 'global', status: 'active',
  verified: false, trust_approved: false, has_local_changes: false,
}
const PERMISSIONS: PermissionSettings = {
  policies: {}, skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}
const APP_STATE: AppState = { onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } }
const MARKETPLACE: SkillMarketplaceStatus = { enabled: false, registries: [] }
const UPLOAD: UploadFilesResponse = { files: [{
  name: `${NAME}.md`, path: 'uploads/states/notes-helper.md', ref: SOURCE,
  size: new TextEncoder().encode(MARKDOWN).length, content_type: 'text/markdown',
}] }
const REVIEW_PATH = `/api/v1/skills/install/review/${REVIEW.review_id}`
const reviewStatus = (value: SkillInstallReviewStatus['state'], extra: Partial<SkillInstallReviewStatus> = {}): SkillInstallReviewStatus => ({
  review_id: REVIEW.review_id, state: value, revision: R1, installed_revision: null, replaced_revision: null, ...extra,
})
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const refusal = (status: number, error: string) => json({ error }, status)

// not-wire-format: transport observations only.
type Seen = { method: string; path: string }
type Answer = () => Response | Promise<Response>
let seen: Seen[]
let answers: { review: Answer; install: Answer; status: Answer }
let skills: Skill[]
let client: QueryClient
const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
const calls = (method: string, path: string) => seen.filter((call) => call.method === method && call.path === path)
const installs = () => calls('POST', '/api/v1/skills/install')

// An answer the test releases itself: the request stays in flight until then.
function hold() {
  let release!: (response: Response) => void
  const promise = new Promise<Response>((resolve) => { release = resolve })
  return { answer: (() => promise) as Answer, release }
}

// A sentence as inline text or as a toast (the spec does not say which).
const shown = (text: string) =>
  screen.queryAllByText(text, { exact: false }).length > 0 ||
  useUiStore.getState().toasts.some((toast) => toast.message.includes(text))
const clearToasts = () => { for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id) }

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-22T12:00:00Z'))
  seen = []
  skills = []
  answers = {
    review: () => json(REVIEW),
    install: () => json(INSTALLED),
    status: () => json(reviewStatus('installed', { installed_revision: R1 })),
  }
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
  document.cookie = 'csrf=states-review; path=/'
  clearToasts()
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input, init) => {
    const path = new URL(input instanceof Request ? input.url : String(input), 'http://localhost').pathname
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    seen.push({ method, path })
    if (method === 'GET' && path === REVIEW_PATH) return answers.status()
    if (method === 'DELETE' && path === REVIEW_PATH) return new Response(null, { status: 204 })
    if (method === 'POST' && path === '/api/v1/skills/install/review') return answers.review()
    if (method === 'POST' && path === '/api/v1/skills/install') return answers.install()
    if (method === 'POST' && path === '/api/v1/upload') return json(UPLOAD)
    const bootstrap: Record<string, unknown> = {
      '/api/v1/skills': skills, '/api/v1/tools': [], '/api/v1/security/permissions': PERMISSIONS,
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

// Page -> New skill -> Upload a skill: the upload is sent to be staged.
async function startUpload() {
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
  fireEvent.change(inputs[0], { target: { files: [new File([MARKDOWN], `${NAME}.md`, { type: 'text/markdown' })] } })
  await tick()
  expect(calls('POST', '/api/v1/skills/install/review'), 'the upload was sent to be staged').toHaveLength(1)
}
async function openReview() {
  await startUpload()
  expect(screen.getByRole('button', { name: /^install skill$/i }), 'the review is open').toBeInTheDocument()
}
async function pressInstall() {
  fireEvent.click(screen.getByRole('button', { name: /^install skill$/i }))
  await tick()
  expect(installs(), 'Install was pressed once').toHaveLength(1)
}

it('instrument only: the sentence probe sees inline text and a toast, and sees neither when absent', () => {
  const sentence = 'Checking the skill…'
  expect(shown(sentence), 'absent to start').toBe(false)
  const planted = document.createElement('p')
  planted.textContent = sentence
  document.body.appendChild(planted)
  expect(shown(sentence), 'planted inline text is seen').toBe(true)
  planted.remove()
  expect(shown(sentence)).toBe(false)
  useUiStore.getState().addToast({ message: sentence, variant: 'default' })
  expect(shown(sentence), 'a planted toast is seen').toBe(true)
  clearToasts()
  expect(shown(sentence)).toBe(false)
})

describe('Install review, Loading (spec line 938)', () => {
  it('while the skill is staged the page says "Checking the skill…"', async () => {
    answers.review = hold().answer
    await startUpload()
    expect(shown('Checking the skill…'), 'the staging wait is named').toBe(true)
  })

  it('while the skill is staged Cancel is usable; pressed, nothing is installed and the late review never opens', async () => {
    const staging = hold()
    answers.review = staging.answer
    await startUpload()

    const cancel = screen.queryByRole('button', { name: /^cancel$/i })
    expect(cancel, 'Cancel is offered while the skill is staged').not.toBeNull()
    expect(cancel, 'Cancel is usable while the skill is staged').toBeEnabled()

    fireEvent.click(cancel!)
    await tick()
    expect(installs(), 'nothing was installed').toEqual([])
    expect(screen.queryByRole('button', { name: /^install skill$/i }), 'no review is open').toBeNull()

    // The staging answer arrives after Cancel: the discarded skill is not offered.
    staging.release(json(REVIEW))
    await tick()
    expect(screen.queryByRole('button', { name: /^install skill$/i }), 'a cancelled staging never opens a review').toBeNull()
    expect(installs()).toEqual([])
  })

  it('after Install is pressed, while its answer is awaited, the page says "Installing…" and Cancel is disabled', async () => {
    const publishing = hold()
    answers.install = publishing.answer
    await openReview()
    expect(shown('Installing…'), 'control: not before Install is pressed').toBe(false)

    await pressInstall()

    expect.soft(shown('Installing…'), 'the install wait is named from the press').toBe(true)
    expect(screen.getByRole('button', { name: /^cancel$/i }), 'C-13: Cancel is disabled after Install is pressed').toBeDisabled()
    const again = screen.queryByRole('button', { name: /^install skill$/i })
    if (again) expect(again, 'a second Install press is not possible while the first is in flight').toBeDisabled()
  })

  it('control: once the install is answered and the status still says publishing, the page keeps saying "Installing…" with Cancel disabled', async () => {
    answers.status = () => json(reviewStatus('publishing'))
    await openReview()

    await pressInstall()
    await tick(1500)

    expect(calls('GET', REVIEW_PATH).length, 'the status is being read').toBeGreaterThanOrEqual(1)
    expect(shown('Installing…'), 'still installing while the status says publishing').toBe(true)
    expect(screen.getByRole('button', { name: /^cancel$/i })).toBeDisabled()
    expect(shown('Installed ' + NAME), 'not reported installed before a terminal state').toBe(false)
  })
})

describe('Install review, Error (spec line 938, C-13)', () => {
  const REFUSALS: Array<{ name: string; code: number; text: string }> = [
    // The server's own reason is shown as sent. The sentence is a fixture's: the spec fixes no generic wording.
    { name: 'a server reason', code: 500, text: 'The skills folder could not be written.' },
    { name: 'trust Deny, set after the review (C-13: re-checked at publish; the C-3 text)', code: 403, text: 'This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed.' },
    { name: 'an expired review', code: 410, text: 'This review expired. Open the skill again to review it.' },
    { name: 'a skill that changed after review', code: 409, text: 'The skill changed after review. Review it again.' },
  ]
  it.each(REFUSALS)('$name: the person reads the server\'s sentence and nothing is reported installed', async ({ code, text }) => {
    answers.install = () => refusal(code, text)
    await openReview()
    expect(shown(text), 'control: the sentence is not there before Install is pressed').toBe(false)

    await pressInstall()

    expect(shown(text), `the ${code} sentence is shown`).toBe(true)
    expect(shown('Installed ' + NAME), 'a refused install is never reported as installed').toBe(false)
  })

  it('a refusal while staging (the 422 checksum sentence of C-13) is shown as sent and opens no review', async () => {
    const text = "The downloaded skill does not match the catalog's checksum. Nothing was installed."
    answers.review = () => refusal(422, text)
    await startUpload()

    expect(shown(text), 'C-13: the 422 sentence is shown').toBe(true)
    expect(screen.queryByRole('button', { name: /^install skill$/i }), 'nothing staged, so nothing to install').toBeNull()
    expect(installs()).toEqual([])
  })
})

describe('Install review, Partial and Success (spec line 938, C-13)', () => {
  const NOT_VERIFIED = 'Not verified: the catalog did not answer.'

  it('a review whose catalog lookup got no answer says "Not verified: the catalog did not answer."', async () => {
    answers.review = () => json({ ...REVIEW, verification: 'unavailable', verified: false })
    await openReview()
    expect(shown(NOT_VERIFIED)).toBe(true)
  })

  it.each([
    { name: 'a catalog that publishes no hash', review: { ...REVIEW, verification: 'no_registry_hash' as const, verified: false } },
    { name: 'a matched archive', review: { ...REVIEW, verification: 'matched' as const, verified: true } },
  ])('control: $name does not say the catalog did not answer', async ({ review }) => {
    answers.review = () => json(review)
    await openReview()
    expect(shown(NOT_VERIFIED), 'the registry did answer the lookup').toBe(false)
  })

  it('a successful install shows the full success sentence: set to Ask, no agent granted, where to grant', async () => {
    await openReview()
    skills = [INSTALLED]

    await pressInstall()
    await tick(1000)

    expect(shown(
      `Installed ${NAME}. It is set to Ask: an agent needs your yes to use it unless Auto-approve is on. ` +
      "No agent is granted it yet; grant it on an agent's Skills tab.",
    ), 'the spec row\'s success toast').toBe(true)
  })
})
