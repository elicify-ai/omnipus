// ToolPolicyEditor.locks.test.tsx — Skills & Tools redesign (#1963, epic
// #1519, page F), order 22 (S-19, S-22, S-24; FR-023, FR-024; Dataset D1
// screen rows).
//
// Oracle: skills-tools-redesign-spec.md FR-023 ("In an agent's Tools tab,
// badges looser than the global value MUST be disabled, with the tooltip
// 'The global setting is Ask. Set it to Allow on Skills & Tools first.'
// (or 'The global setting is Deny. Change it on Skills & Tools first.')")
// and the D1 screen rows (agent looser than global → disabled with the
// reason; the stored looser value is kept, never widened silently).
// FR-024: the agent Tools tab's Skills group lists the granted skills with
// Allow / Ask / Deny, or reads "No skills granted. Grant skills on this
// agent's Skills tab." S-22's screen half: Omnipus's `write-skill` row has
// its Ask badge disabled with the visible reason "Omnipus takes no per-agent
// Ask; set Ask globally or use Deny.", linked with aria-describedby — and a
// disabled control is never clickable (the 422 API half is order 14).
//
// CHECK round 1 F4 strengthening: the category is expanded through its
// disclosure the way a person does, every badge query is scoped with
// `within` to its own tool row, badge names are exact (anchored — no
// "Always Allow" or other containing text matches), and a disabled badge's
// no-op is OBSERVED through the onChange spy, not inferred from its
// disabled state.
//
// RED today: the editor takes globalPolicies but does not disable or
// explain looser agent values, so every guard below fails; the Skills group
// does not exist yet, so the S-22/S-24 cases fail at their named BLOCKED
// guard (FR-024's Skills group is required by spec line 90 / FR-024).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool, AgentToolsCfg } from '@/lib/api'
import type { AgentSkillEntry } from '@/lib/api/generated/openapi-types'
import { ToolPolicyEditor } from './ToolPolicyEditor'

const tools: RegistryTool[] = [
  {
    name: 'write_file',
    description: 'Write a file.',
    scope: 'core',
    category: 'file',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  },
]

function mountEditor(
  value: { policies: Record<string, 'allow' | 'ask' | 'deny'> },
  globalPolicies: { policies: Record<string, 'allow' | 'ask' | 'deny'> },
  onChange: (next: { policies: Record<string, 'allow' | 'ask' | 'deny'> }) => void = () => {},
) {
  return render(
    <ToolPolicyEditor
      tools={tools}
      value={value}
      globalPolicies={globalPolicies}
      onChange={onChange}
    />,
  )
}

// The category groups start collapsed (ToolPolicyEditor's per-category
// DisclosureRow); expand the Files group the way a person does — through its
// disclosure control (CHECK round 1 F4; the editor's category label for
// 'file' is "Files").
async function expandFilesCategory() {
  const user = userEvent.setup()
  const trigger = screen
    .getAllByRole('button', { name: /^files/i })
    .find((b) => b.getAttribute('aria-expanded') !== undefined)
  if (trigger && trigger.getAttribute('aria-expanded') === 'false') {
    await user.click(trigger)
  }
}

// The write_file row, scoped with `within` so no other control can satisfy
// the query (CHECK round 1 F4).
function fileRow() {
  return within(screen.getByTestId('tool-row-write_file'))
}

describe('ToolPolicyEditor — global locks (order 22)', () => {
  it('S-19/D1#3: an agent Allow under a global Ask is disabled with the Ask tooltip', async () => {
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'ask' } })
    await expandFilesCategory()
    const allow = fileRow().getByRole('button', { name: /^allow$/i })
    expect(allow).toBeDisabled()
    expect(allow).toHaveAccessibleDescription(/the global setting is ask/i)
  })

  it('S-19/D1#4: an agent Allow under a global Deny is disabled with the Deny tooltip', async () => {
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'deny' } })
    await expandFilesCategory()
    const allow = fileRow().getByRole('button', { name: /^allow$/i })
    expect(allow).toBeDisabled()
    expect(allow).toHaveAccessibleDescription(/the global setting is deny/i)
  })

  it('S-22 screen half (D1 row 8): a disabled looser control is observed to never change the value', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'ask' } }, onChange)
    await expandFilesCategory()
    const allow = fileRow().getByRole('button', { name: /^allow$/i })
    await user.click(allow)
    // The no-op is OBSERVED, not inferred: the click fires no change and the
    // stored looser value is kept, never widened silently through the lock.
    expect(onChange).not.toHaveBeenCalled()
    expect(fileRow().getByRole('button', { name: /^allow$/i })).toBeDisabled()
  })

  it('FR-023: the tooltip names where to change it', async () => {
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'ask' } })
    await expandFilesCategory()
    const allow = fileRow().getByRole('button', { name: /^allow$/i })
    expect(allow).toHaveAccessibleDescription(/skills & tools/i)
  })
})

// ── S-22 and S-24: the Skills group on the real agent Tools tab ────────────
//
// FR-024 puts the Skills group on the agent's Tools tab (spec line 90:
// ToolPolicyEditor gains it, hosted by ToolsAndPermissions). The tests drive
// the real tab so the data path is the real one — fetchAgentTools already
// carries the generated AgentSkillEntry[] (contract-first #8, C-4 landed);
// only the rendering is missing. Every mock is at the network edge.
import { ToolsAndPermissions } from '../agents/ToolsAndPermissions'

vi.mock('@/lib/api', () => ({
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGlobalToolPolicies: vi.fn(),
  fetchProviders: vi.fn(),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchGodMode: vi.fn(),
  isApiError: vi.fn(() => false),
}))
vi.mock('@/hooks/useAutoSave', () => ({
  useAutoSave: vi.fn(() => ({ status: 'idle', error: undefined, lastSavedAt: undefined, saveNow: vi.fn() })),
}))
vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))
vi.mock('../agents/MCPServerPicker', () => ({
  MCPServerPicker: () => null,
}))
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

import * as api from '@/lib/api'

// S-22's Given: Omnipus's Tools tab, its `write-skill` row (Omnipus's own
// skill, granted; global Ask; no per-agent value — per-agent Ask is refused
// for Omnipus).
const WRITE_SKILL: AgentSkillEntry = {
  key: 'write-skill',
  name: 'write-skill',
  display_name: 'Write Skill',
  granted: true,
  project_workspace: null,
  configured_policy: null,
  global_policy: 'ask',
  effective_policy: 'ask',
  unverified: false,
}

const OMNIPUS_TOOLS: AgentToolsCfg = { builtin: { policies: {} } }
const revision = '0'.repeat(64)

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
}

function tabWrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeQueryClient()}>{children}</QueryClientProvider>
}

// The Skills group renders inside the editor. A person sees it as a group of
// the agent's granted skills; the pack's BLOCKED convention names the
// missing element loudly instead of failing on a bare locator.
function skillsGroup(): HTMLElement {
  const group = screen.queryByTestId('skills-group')
  if (!group) {
    throw new Error(
      'BLOCKED: the agent Tools tab Skills group (FR-024; spec C-table line 90: ToolPolicyEditor gains the Skills group) is not implemented',
    )
  }
  return group
}

describe('ToolPolicyEditor — Skills group (order 22: S-22, S-24)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([])
    vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
    vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
    vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({ policies: {} })
    vi.mocked(api.fetchProviders).mockResolvedValue([])
    vi.mocked(api.fetchGodMode).mockResolvedValue({
      enabled: false,
      persisted: true,
      available: true,
      supported: true,
    })
  })

  it('S-22: Omnipus write-skill Ask badge is disabled with the Omnipus reason, and clicking it changes nothing', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: OMNIPUS_TOOLS,
      tools: [],
      skills: [WRITE_SKILL],
    })
    vi.mocked(api.updateAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: OMNIPUS_TOOLS,
      tools: [],
      skills: [WRITE_SKILL],
    })

    render(
      <ToolsAndPermissions
        agentId="omnipus"
        agentType="Main"
        tools={OMNIPUS_TOOLS}
        onChange={onChange}
      />,
      { wrapper: tabWrapper },
    )
    const group = skillsGroup()

    const ask = within(group).getByRole('button', { name: /^ask$/i })
    expect(ask).toBeDisabled()
    // The visible reason is linked with aria-describedby (S-22's exact text).
    expect(ask).toHaveAccessibleDescription(
      'Omnipus takes no per-agent Ask; set Ask globally or use Deny.',
    )
    // No click path: the disabled badge fires no change and no write.
    await user.click(ask)
    expect(onChange).not.toHaveBeenCalled()
    expect(api.updateAgentTools).not.toHaveBeenCalled()
  })

  it('S-24: an agent with no granted skills reads the grant-here sentence', async () => {
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: OMNIPUS_TOOLS,
      tools: [],
      skills: [],
    })

    render(
      <ToolsAndPermissions
        agentId="agent-custom"
        agentType="Main"
        tools={OMNIPUS_TOOLS}
        onChange={() => {}}
      />,
      { wrapper: tabWrapper },
    )
    const group = skillsGroup()
    expect(
      within(group).getByText("No skills granted. Grant skills on this agent's Skills tab."),
    ).toBeInTheDocument()
  })
})
