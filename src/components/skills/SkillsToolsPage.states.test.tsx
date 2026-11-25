// SkillsToolsPage.states.test.tsx — order 30 (S-66, S-67, S-131, S-132; C-16,
// C-19 "The notice", FR-050, FR-084). The page's loading, failure and empty
// states, driven through the imported page with the network edge doubled
// (CHECK round 1 finding 2: the earlier file held only a file-existence
// guard):
//   S-66: while the skills request is unanswered, "Loading skills…" and
//         skeleton rows show, the count reads "…", and search, filters and
//         badges are disabled;
//   S-67: a failing tools request shows the cause with Retry and no badge
//         is enabled;
//   empty catalogue control: no rows and no error — the page renders its
//         empty state rather than a failure;
//   S-131 (added in fix round 4, Part F, after spec amendment b27c64109):
//         the Skills tab names the agents the upgrade has not finished, from
//         the read-only `upgrade_unfinished` list that GET and every PATCH
//         of /security/permissions serve (C-2). Expected text is the spec's
//         own sentence; the list is a literal wire fixture;
//   S-132 (same): the Omnipus tools load and the permissions do not — rows
//         show their GET /tools levels, read-only; "Permissions could not be
//         loaded." with Retry; after a successful Retry the badges work.
//
// RED, shown by a run: saved run fix4f8-02 (the frontend head f69d7a7b7 plus this file and the
// permissions double; 7 failed | 7 passed; ta/fix4-ts-fix4f8-02.log). Seven fail for the reason the
// amendment implies: six S-131 notice tests ("Unable to find an element with the text: /The upgrade has not
// finished for/": the page renders no notice) and the S-132 test "Permissions could not be loaded." (the
// sentence is nowhere on the page). Seven pass: S-66, S-67 and the empty-catalogue control (they existed
// before this round), plus four that hold on the dev page today and are controls, not product proof: "an
// empty list: no notice" and "after Jim's record is deleted: the member is absent: no notice" (both pass
// trivially while no notice exists at all; what keeps them honest is a CHECK mutant that always shows the
// notice), and the two S-132 tests that rows show the levels GET /tools gave them with no write possible and
// that the badges work after a successful Retry.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool, Skill } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
// The pack's BLOCKED seam (support.tsx::requireModule convention): the
// missing module is a named, loud failure — the acceptance oracles are the
// interaction tests below.
import type { ComponentType } from 'react'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-66, S-67',
  ).toContain('/src/components/skills/SkillsToolsPage.tsx')
  const mod = (await pageModules['/src/components/skills/SkillsToolsPage.tsx']()) as Record<string, unknown>
  expect(typeof mod.SkillsToolsPage, 'BLOCKED: SkillsToolsPage public export').toBe('function')
  return mod.SkillsToolsPage as ComponentType
}

let SkillsToolsPage: ComponentType

beforeEach(async () => {
  SkillsToolsPage = await requireSkillsToolsPage()
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchAgents: vi.fn(),
    fetchRegistryTools: vi.fn(),
    fetchBuiltinTools: vi.fn(),
    fetchProviders: vi.fn(),
    fetchSkillMarketplaceStatus: vi.fn(),
    searchSkills: vi.fn(),
    fetchMcpServers: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

import * as api from '@/lib/api'
import { expectPermissionPatches, useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {}, skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))

function pageWrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      {children}
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchAgents).mockResolvedValue([])
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

describe('SkillsToolsPage — states (order 30)', () => {
  it('S-66: while the skills request is pending, writing is disabled and the count reads "…"', () => {
    vi.mocked(api.fetchSkills).mockReturnValue(new Promise(() => {})) // never answers
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    expect(screen.getByText('Loading skills…')).toBeInTheDocument()
    expect(screen.getByText('…')).toBeInTheDocument() // the count placeholder
    // The catalogued SearchField renders type=search, whose role is searchbox
    // (CHECK round 4, candidate 13); the accessible name and the disabled
    // oracle (S-66: writing is disabled while the request is pending) are kept.
    expect(screen.getByRole('searchbox', { name: /search/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /permission filter/i })).toBeDisabled()
    // Skeleton rows: at least one loading placeholder is rendered.
    expect(screen.getAllByTestId(/skeleton/).length).toBeGreaterThan(0)
  })

  it('S-67: a failing tools request shows the cause with Retry and no badge is enabled', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([])
    vi.mocked(api.fetchRegistryTools).mockRejectedValue(new ApiError(0, 'Omnipus did not respond'))
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    expect(await screen.findByText('Omnipus did not respond')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    // No badge is enabled anywhere (no writable rows without data).
    for (const badge of screen.queryAllByRole('button', { name: /^(allow|ask|deny)$/i })) {
      expect(badge).toBeDisabled()
    }
  })

  it('empty control: an empty catalogue renders no rows and no error', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([])
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    await screen.findByRole('tab', { name: /^skills/i })
    expect(screen.queryByTestId(/tool-row-/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// S-131 and S-132 — added in Skills & Tools fix round 4, Part F, for the spec
// amendment of 2026-11-25 (commit b27c64109, ruling spec-gaps-1 on gaps (a)
// and (b)). Expected values are the spec's own sentences and the D-set's
// literals; none was read from the page. They cannot pass on a tree whose
// page lacks the notice or the partial state, and that is the point.
// ---------------------------------------------------------------------------

const PERMISSION_WORDS = /^(allow|ask|deny)$/i

// A row of GET /agents, with the members the notice reads.
function agentRow(id: string, name: string) {
  return { id, name, type: 'custom', status: 'active' }
}
function serveAgents(...rows: Array<ReturnType<typeof agentRow>>) {
  vi.mocked(api.fetchAgents).mockResolvedValue(rows as unknown as Awaited<ReturnType<typeof api.fetchAgents>>)
}

// C-2 row `PermissionSettings.upgrade_unfinished`: a read-only list the server serves on GET and on every
// PATCH of /security/permissions. It goes on the stored settings the permissions double serves, as the
// server's JSON carries it. The generated types do not have the member until the backend regenerates them,
// so it is set without declaring a wire type here.
function serveUpgradeUnfinished(list: Array<{ agent_id: string; reason: string }> | undefined) {
  const stored = permissions().stored as unknown as Record<string, unknown>
  if (list === undefined) delete stored.upgrade_unfinished
  else stored.upgrade_unfinished = list
}

function skill(id: string): Skill {
  return {
    has_local_changes: false,
    id,
    name: id,
    description: `Skill ${id}.`,
    source: 'global',
    status: 'active',
    verified: true,
    version: '1.0.0',
    revision: 'a'.repeat(64),
    category: 'tools',
    trust_approved: false,
  }
}

function tool(name: string, category: string, visibility: RegistryTool['visibility']): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category,
    source: 'builtin',
    visibility,
    visibility_locked: false,
    permission_fixed: false,
  }
}

async function openTab(name: RegExp) {
  await userEvent.setup().click(await screen.findByRole('tab', { name }))
}

// A group starts collapsed; its disclosure is the button carrying aria-expanded whose name starts with the label.
async function openGroup(label: RegExp) {
  const trigger = await waitFor(
    () => {
      const found = screen.getAllByRole('button', { name: label }).find((button) => button.hasAttribute('aria-expanded'))
      expect(found, `the ${label} group disclosure is present`).toBeDefined()
      return found as HTMLElement
    },
    { timeout: 3000 },
  )
  if (trigger.getAttribute('aria-expanded') === 'false') await userEvent.setup().click(trigger)
}

// The level control's accessible name. The spec's A11y list (line 964) says "What the model sees for <name>:
// <level>"; this matcher keeps the fixed start and the level at the end and leaves the middle free, so the
// partial-state rule is not tied to the name's exact middle (the name itself is a separate requirement).
function levelButton(row: HTMLElement, word: string): HTMLElement {
  return within(row).getByRole('button', { name: new RegExp(`^What the model sees\\b.*: ${word}$`) })
}

const permissionReads = () => permissions().seen.filter(({ method }) => method === 'GET').length

// The page, rendered and given time to show what the first permissions read answered.
async function renderSettled() {
  render(<SkillsToolsPage />, { wrapper: pageWrapper })
  await screen.findByRole('tab', { name: /^skills/i })
  await waitFor(() => expect(permissionReads(), 'instrument: the page read the permissions').toBeGreaterThan(0))
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 100))
  })
}

const NOTICE_WORDS = /The upgrade has not finished for/

// The notice is one callout (C-19 "The notice": "Callout: ..."). A second copy of the words outside any
// callout (a screen-reader announcement, say) is not a second notice, so only callouts are counted.
async function findNotice(): Promise<HTMLElement> {
  const found = await screen.findAllByText(NOTICE_WORDS)
  const callouts = new Set<HTMLElement>()
  for (const element of found) {
    const callout = element.closest('[data-slot="callout"]') as HTMLElement | null
    if (callout) callouts.add(callout)
  }
  expect(callouts.size, 'the notice sits in one callout (C-19 "The notice": "Callout: ...")').toBe(1)
  return [...callouts][0]
}

describe('SkillsToolsPage — the unfinished-upgrade notice (S-131; C-19 "The notice"; C-2 upgrade_unfinished)', () => {
  // S-131's Given and Then: Mia's write failed and Jim's record was skipped as unreadable.
  const S131_LIST = [
    { agent_id: 'mia', reason: 'write_failed' },
    { agent_id: 'jim', reason: 'unreadable' },
  ]
  // S-131, verbatim.
  const S131_SENTENCE =
    'The upgrade has not finished for 2 agents: Mia (its settings file could not be saved), Jim (its settings file could not be read). Their old skill limits still apply.'

  it('S-131: Mia (write_failed) and Jim (unreadable) are named in the spec sentence on the Skills tab', async () => {
    serveUpgradeUnfinished(S131_LIST)
    serveAgents(agentRow('mia', 'Mia'), agentRow('jim', 'Jim'))
    await renderSettled()

    expect(await findNotice()).toHaveTextContent(S131_SENTENCE)
  })

  it('names each agent by its name from GET /agents, not by its id', async () => {
    serveUpgradeUnfinished([{ agent_id: 'research-bot', reason: 'write_failed' }])
    serveAgents(agentRow('research-bot', 'Research Bot'))
    await renderSettled()

    const notice = await findNotice()
    expect(notice).toHaveTextContent('Research Bot (its settings file could not be saved)')
    expect(notice, 'the id is shown only for an agent GET /agents does not know').not.toHaveTextContent('research-bot')
  })

  it('names an agent GET /agents does not know by its agent_id', async () => {
    serveUpgradeUnfinished([{ agent_id: 'ghost', reason: 'unreadable' }])
    serveAgents(agentRow('mia', 'Mia'))
    await renderSettled()

    expect(await findNotice()).toHaveTextContent('ghost (its settings file could not be read)')
  })

  it('counts the entries: three entries read "for 3 agents" and each is named with its reason words', async () => {
    serveUpgradeUnfinished([
      { agent_id: 'mia', reason: 'write_failed' },
      { agent_id: 'jim', reason: 'unreadable' },
      { agent_id: 'ray', reason: 'write_failed' },
    ])
    serveAgents(agentRow('mia', 'Mia'), agentRow('jim', 'Jim'), agentRow('ray', 'Ray'))
    await renderSettled()

    const notice = await findNotice()
    expect(notice).toHaveTextContent('The upgrade has not finished for 3 agents:')
    expect(notice).toHaveTextContent('Mia (its settings file could not be saved)')
    expect(notice).toHaveTextContent('Jim (its settings file could not be read)')
    expect(notice).toHaveTextContent('Ray (its settings file could not be saved)')
    expect(notice).toHaveTextContent('Their old skill limits still apply.')
  })

  it('S-131 after Mia is retried: only Jim is listed and Mia is not named', async () => {
    serveUpgradeUnfinished([{ agent_id: 'jim', reason: 'unreadable' }])
    serveAgents(agentRow('mia', 'Mia'), agentRow('jim', 'Jim'))
    await renderSettled()

    // The wording for one agent is not fixed by the spec's template, so the sentence is not pinned whole.
    const notice = await findNotice()
    expect(notice).toHaveTextContent('Jim (its settings file could not be read)')
    expect(notice).toHaveTextContent('Their old skill limits still apply.')
    expect(notice).not.toHaveTextContent('Mia')
  })

  it.each([
    ['S-131 after Jim\'s record is deleted: the member is absent', undefined],
    ['an empty list', []],
  ] as const)('%s: no notice', async (_why, list) => {
    serveUpgradeUnfinished(list === undefined ? undefined : [...list])
    serveAgents(agentRow('mia', 'Mia'), agentRow('jim', 'Jim'))
    await renderSettled()

    expect(screen.queryByText(NOTICE_WORDS), 'the callout is gone').toBeNull()
  })

  // C-2: the list is "served by GET and by every PATCH response", and S-131 ends with the callout gone once the
  // member is. Inferred: the page takes the member from the answer to each save as well as from the first read.
  it('follows the latest answer: still shown after a save that the server still lists it in, gone after one it does not', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([skill('weather')])
    serveUpgradeUnfinished([{ agent_id: 'jim', reason: 'unreadable' }])
    serveAgents(agentRow('jim', 'Jim'))
    const user = userEvent.setup()
    await renderSettled()
    await findNotice()
    await openGroup(/^tools/i)
    const row = await screen.findByTestId('skill-row-weather')

    await user.click(within(row).getByRole('button', { name: /^allow$/i }))
    await waitFor(() => expectPermissionPatches(permissions(), { skill_policies: { weather: 'allow' } }))
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument())
    expect(await findNotice(), 'the server still lists Jim in the answer to the save').toHaveTextContent('Jim (its settings file could not be read)')

    serveUpgradeUnfinished(undefined) // Jim's record file was deleted
    await user.click(within(screen.getByTestId('skill-row-weather')).getByRole('button', { name: /^ask$/i }))
    await waitFor(() =>
      expectPermissionPatches(permissions(), { skill_policies: { weather: 'allow' } }, { skill_policies: { weather: 'ask' } }),
    )
    await waitFor(() => expect(screen.queryByText(NOTICE_WORDS), 'the answer to the save no longer lists Jim').toBeNull(), { timeout: 3000 })
  })
})

describe('SkillsToolsPage — Omnipus tools load but permissions fail (S-132; FR-050)', () => {
  // S-132's Given: GET /tools answers; GET /security/permissions fails with "Omnipus did not respond".
  beforeEach(() => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([
      tool('send_email', 'communication', 'name_only'),
      tool('fetch_url', 'web', 'full'),
    ])
    permissions().stored.policies = { send_email: 'ask', fetch_url: 'ask' }
    permissions().failNextRead(500, 'Omnipus did not respond')
  })

  it('the tab says "Permissions could not be loaded." with a Retry beside it', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/^omnipus tools/i)

    const sentence = await screen.findByText('Permissions could not be loaded.')
    const container = (sentence.closest('[data-slot="callout"]') ?? screen.getByRole('tabpanel')) as HTMLElement
    expect(within(container).getByRole('button', { name: /^retry$/i })).toBeEnabled()
  })

  it('the rows show the levels GET /tools gave them, and no badge or level control can write', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/^omnipus tools/i)
    await openGroup(/^communication/i)
    await openGroup(/^web/i)

    const email = await screen.findByTestId('tool-row-send_email')
    const fetchUrl = await screen.findByTestId('tool-row-fetch_url')
    expect(levelButton(email, 'Name only'), 'send_email shows the level GET /tools gave it').toBeDisabled()
    expect(levelButton(fetchUrl, 'Full'), 'fetch_url shows the level GET /tools gave it').toBeDisabled()
    for (const row of [email, fetchUrl]) {
      const badges = within(row).getAllByRole('button', { name: PERMISSION_WORDS })
      expect(badges, 'Allow, Ask and Deny are all there').toHaveLength(3)
      for (const badge of badges) expect(badge, 'a badge cannot write without the permissions').toBeDisabled()
    }
    expect(permissions().patches(), 'nothing was written').toEqual([])
  })

  it('after a successful Retry the badges work', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const user = userEvent.setup()
    await openTab(/^omnipus tools/i)
    await openGroup(/^communication/i)
    await screen.findByTestId('tool-row-send_email')
    const readsBefore = permissionReads()

    await user.click((await screen.findAllByRole('button', { name: /^retry$/i }))[0])

    await waitFor(() => expect(permissionReads(), 'Retry reads the permissions again').toBeGreaterThan(readsBefore))
    const allow = () => within(screen.getByTestId('tool-row-send_email')).getByRole('button', { name: /^allow$/i })
    await waitFor(() => expect(allow()).toBeEnabled())
    expect(screen.queryByText('Permissions could not be loaded.'), 'the message goes once the read succeeds').toBeNull()

    await user.click(allow())
    await waitFor(() => expectPermissionPatches(permissions(), { policies: { send_email: 'allow' } }))
  })
})
