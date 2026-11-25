// Unit tests for ToolsAndPermissions — FR-027, FR-029, FR-086, MAJ-008
//
// Updated for #333 (US-D2): ToolsAndPermissions delegates to the shared
// ToolPolicyEditor. The role presets (Cautious/Balanced/Full access) are gone
// with the Skills & Tools redesign (spec T-Q6 A, US-3 AS1): a save is driven by
// one row's Allow / Ask / Deny badge (US-3 AS8: a per-agent value that is
// changed "saves on its own"), and the global value the badges are floored by
// is read from GET /security/permissions (C-22 row 6).
//
// Individual tool rows are now inside collapsed CategorySection accordions.
// Fence badge (FR-086 / MAJ-008) is no longer rendered inline in ToolsAndPermissions
// — it was part of the old raw-grid UI that was replaced by ToolPolicyEditor.
//
// Tests:
//  1. Calls fetchRegistryTools (GET /api/v1/tools)
//  2. Source badge for MCP tools visible in the MCP section
//  3. Shell/fs conflict banner (still rendered by ToolsAndPermissions directly)
//  4. ToolPolicyEditor shows no role preset control (US-3 AS1, T-Q6 A)
//  5. No write fires for locked agents (B-2 / #332)
//  6. NO spurious PUT fires when opening Tools tab (server hydration ≠ user edit)
//  7. Real policy change (a row badge) triggers the re-auth-gated save path (not a bare 403)
//  8. 403 re-auth path: gate detects re-auth 403, retries with token (Spec-6 FR-12.2)
//  9. Latest-wins: edits made while a save is in-flight are not dropped (bug fix)

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Mock the agent-tools edge of the API module. updateAgentTools accepts an
// optional reAuthToken. The global value is NOT mocked here: the real client
// reads it from GET /security/permissions through the fetch double below (C-22
// row 6 retires GET /security/tool-policies, the tab's last reader).
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGodMode: vi.fn(),
  reAuth: vi.fn(),
  isApiError: vi.fn(() => false),
}))

// Mock the useReAuthGate module so runGated is a controllable spy.
// The factory returns a fresh spy per test (reset in beforeEach).
const mockRunGated = vi.fn(async (fn: (token: string) => unknown) => fn(''))

vi.mock('@/components/settings/useReAuthGate', () => ({
  useReAuthGate: () => ({
    runGated: mockRunGated,
    dialog: null,
    open: false,
  }),
  isReAuthCancelled: vi.fn(() => false),
}))

import * as api from '@/lib/api'
import type { RegistryTool, AgentToolsCfg } from '@/lib/api'
// ApiError is imported from its own module, as before: the re-auth 403 below
// is built from the class itself, not from whatever the mocked barrel exposes.
import { ApiError } from '@/lib/api-error'
import { ToolsAndPermissions } from './ToolsAndPermissions'
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

// The global value the agent's badges are floored by (Skills & Tools). Only the
// fetch edge is doubled. The empty map is the global value this file always
// used; a test that clicks a badge states the value it needs in its own Given.
const permissions = useSkillPermissionNetwork(() => ({
  policies: {}, skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))

// reAuth403 mirrors the exact 403 the backend's requireReAuth gate returns.
// useReAuthGate detects it by body/message match and replays a consent token.
function reAuth403() {
  return new ApiError(
    403,
    "You don't have permission to perform this action.",
    { body: '{"error":"this change requires re-typing your password — call POST /api/v1/auth/reauth first"}' },
  )
}

// MCPServerPicker depends on server data — mock it to simplify tests
vi.mock('./MCPServerPicker', () => ({
  MCPServerPicker: () => null,
}))

// AutoSaveIndicator — mock to simplify
vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))

// useUiStore — mock addToast to avoid real store setup
vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: () => void }) => unknown) =>
    selector({ addToast: vi.fn() }),
}))

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
}

function renderWithQuery(ui: React.ReactElement) {
  const qc = makeQueryClient()
  return render(
    <QueryClientProvider client={qc}>
      {ui}
    </QueryClientProvider>
  )
}

const BUILTIN_TOOL: RegistryTool = {
  name: 'read_file',
  scope: 'general',
  category: 'filesystem',
  description: 'Read file contents',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}

const MCP_TOOL: RegistryTool = {
  name: 'mcp_search',
  scope: 'general',
  category: 'web',
  description: 'Search via MCP',
  source: 'mcp',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}

const ADMIN_TOOL: RegistryTool = {
  name: 'system.config.set',
  scope: 'core',
  category: 'system',
  description: 'Set system configuration',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}

const DEFAULT_TOOLS_CFG: AgentToolsCfg = {
  builtin: {
    policies: {},
  },
}
const TOOLS_REVISION = '1'.repeat(64)
const toolsResponse = (config: AgentToolsCfg = DEFAULT_TOOLS_CFG, revision = TOOLS_REVISION) => ({
  revision,
  override_names: [],
  config,
  tools: [],
  skills: [], // C-4: AgentToolsResponse.skills is required
})

const NOOP_CHANGE = () => {}

beforeEach(() => {
  vi.clearAllMocks()
  // Reset the runGated spy back to the pass-through default each test.
  mockRunGated.mockImplementation(async (fn: (token: string) => unknown) => fn(''))

  vi.mocked(api.fetchRegistryTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL])
  vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse())
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
  vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: false, persisted: false, available: true, supported: true })
  // Default: updateAgentTools succeeds and returns the same config
  vi.mocked(api.updateAgentTools).mockResolvedValue(toolsResponse())
})

afterEach(() => {
  vi.useRealTimers()
})

// A person's single edit on a tool row (US-3 AS8): open the Files group, then
// take the row's badge once it is enabled — which means the tools draft and the
// global value have both landed. A click before that is a no-op by design (see
// ToolsAndPermissions.hydration.test.tsx), so every caller awaits this.
async function openFilesGroup() {
  const grid = await screen.findByTestId('category-grid')
  fireEvent.click(within(grid).getByRole('button', { name: /files/i }))
}

async function enabledBadge(tool: string, value: 'Allow' | 'Ask' | 'Deny') {
  const row = await screen.findByTestId(`tool-row-${tool}`)
  const badge = within(row).getByRole('button', { name: value })
  await waitFor(() => expect(badge).toBeEnabled())
  return badge
}

// The same badge, found without waiting: for the fake-timer sections below,
// where a waitFor would sit on a faked clock. Callers have awaited
// enabledBadge for the row first.
function rowBadge(tool: string, value: 'Allow' | 'Ask' | 'Deny') {
  return within(screen.getByTestId(`tool-row-${tool}`)).getByRole('button', { name: value })
}

// The tab's two reads have been sent and answered: its own tools (GET
// /agents/{id}/tools) and the global value (GET /security/permissions). A test
// that expects a control to STAY disabled awaits this first, so it can tell
// "disabled by design" from "not loaded yet".
async function readsSettled() {
  await waitFor(() => expect(api.fetchAgentTools).toHaveBeenCalled())
  await waitFor(() => expect(permissions().seen.some(({ method }) => method === 'GET')).toBe(true))
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
}

describe('ToolsAndPermissions — new endpoint (FR-027, FR-029)', () => {
  it('calls fetchRegistryTools (GET /api/v1/tools), not /tools/builtin', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(api.fetchRegistryTools).toHaveBeenCalledTimes(1)
    })
  })

  it('renders the ToolPolicyEditor with no role preset control after the registry loads (US-3 AS1)', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    // The editor and its category grid are on screen first, so the absences
    // below are about the rendered editor, not about a tab that has not rendered.
    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="category-grid"]')).toBeInTheDocument()
    })
    // EXPECTATION FLIPPED by the spec (CHECK round 4, candidate 9): US-3 AS1
    // "Given any agent's Tools tab, When it opens, Then no role preset control
    // is shown"; T-Q6 A removes the presets from every agent's Tools tab. The
    // old expectation (three preset buttons present) is the retired behaviour.
    for (const id of ['preset-cautious', 'preset-balanced', 'preset-full_access']) {
      expect(document.querySelector(`[data-testid="${id}"]`), id).not.toBeInTheDocument()
    }
    for (const name of ['Cautious', 'Balanced', 'Full access']) {
      expect(screen.queryByRole('button', { name }), name).not.toBeInTheDocument()
    }
  })
})

// ADR-092 J14: per-tool "Under Auto" marker, sourced from the registry's
// `auto_approve` field (contract-first, ToolRegistryEntry.auto_approve).
// Read-only, and shown ONLY on a row whose resolved policy is "ask".
describe('ToolsAndPermissions — ADR-092 J14 "Under Auto" marker', () => {
  it('shows "Auto: runs inside workspace" on a tool resolved to Ask that the registry classifies runs_if_args', async () => {
    const user = userEvent.setup()
    const toolWithAuto: RegistryTool = { ...BUILTIN_TOOL, auto_approve: 'runs_if_args' }
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([toolWithAuto])
    vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse({
      builtin: { policies: { [BUILTIN_TOOL.name]: 'ask' } },
    }))

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    const categoryGrid = await screen.findByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /files/i }))
    const row = await screen.findByTestId(`tool-row-${BUILTIN_TOOL.name}`)
    expect(within(row).getByTestId(`auto-approve-marker-${BUILTIN_TOOL.name}`)).toHaveTextContent('Auto: runs inside workspace')
  })

  it('renders no marker once the same tool is set to Allow', async () => {
    const user = userEvent.setup()
    const toolWithAuto: RegistryTool = { ...BUILTIN_TOOL, auto_approve: 'runs_if_args' }
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([toolWithAuto])
    vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse({
      builtin: { policies: { [BUILTIN_TOOL.name]: 'allow' } },
    }))

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    const categoryGrid = await screen.findByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /files/i }))
    const row = await screen.findByTestId(`tool-row-${BUILTIN_TOOL.name}`)
    expect(within(row).queryByTestId(`auto-approve-marker-${BUILTIN_TOOL.name}`)).not.toBeInTheDocument()
  })
})

describe('ToolsAndPermissions — source badge (FR-027)', () => {
  it('MCP tools section is present when MCP tools are in the registry', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).toBeInTheDocument()
    })
  })

  it('MCP tools section is absent when no MCP tools in registry', async () => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([BUILTIN_TOOL])

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).not.toBeInTheDocument()
    })
  })
})

describe('ToolsAndPermissions — system.* in flat category grid (US-1 / AC5 / FR-103 / #357)', () => {
  // The old system-disclosure-wrapper §3 is removed per Issue #357.
  // system.* tools now appear in the main category grid under the "System" label.

  beforeEach(() => {
    // Registry includes only the system tool (category='system').
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([
      { ...ADMIN_TOOL, scope: 'core' },
    ])
    vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse({
      builtin: { policies: { 'system.config.set': 'allow' } },
    }))
  })

  it('system.* tool (category=system) appears in the flat category grid, NOT a separate disclosure', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={{ builtin: { policies: { 'system.config.set': 'allow' } } }}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      // system-disclosure-wrapper must NOT exist (removed in #357).
      expect(document.querySelector('[data-testid="system-disclosure-wrapper"]')).not.toBeInTheDocument()
      // Category grid must be present with a system category pill.
      expect(document.querySelector('[data-testid="category-grid"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="category-pill-system"]')).toBeInTheDocument()
    })
  })

  it('system.* tool is accessible inside the system category section in the grid', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={{ builtin: { policies: { 'system.config.set': 'allow' } } }}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      // Category grid and system category pill should be present.
      expect(document.querySelector('[data-testid="category-pill-system"]')).toBeInTheDocument()
    })
  })

  it('does not render fence badge inline (no downgraded-to-ask text by default)', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={{ builtin: { policies: { 'system.config.set': 'allow' } } }}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="category-grid"]')).toBeInTheDocument()
    })
    // "downgraded to ask" text must not appear in collapsed state.
    expect(screen.queryByText(/downgraded to ask/i)).not.toBeInTheDocument()
  })
})

describe('ToolsAndPermissions — shell/fs conflict banner', () => {
  const SHELL_TOOL: RegistryTool = {
    name: 'bash',
    scope: 'general',
    category: 'shell',
    description: 'Run a shell command in the workspace',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  }
  const FS_TOOLS: RegistryTool[] = [
    { name: 'write_file', scope: 'general', category: 'filesystem', description: 'Write file', source: 'builtin', visibility: 'full', visibility_locked: false, permission_fixed: false },
    { name: 'read_file', scope: 'general', category: 'filesystem', description: 'Read file', source: 'builtin', visibility: 'full', visibility_locked: false, permission_fixed: false },
    { name: 'list_directory', scope: 'general', category: 'filesystem', description: 'List directory', source: 'builtin', visibility: 'full', visibility_locked: false, permission_fixed: false },
  ]

  beforeEach(() => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([SHELL_TOOL, ...FS_TOOLS])
    vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse())
    // The empty global map this block re-mocked is the file's default one.
  })

  it('banner renders when bash is allow and a filesystem tool is deny', async () => {
    const conflictTools: AgentToolsCfg = {
      builtin: {
        policies: { write_file: 'deny' },
      },
    }
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={conflictTools}
        onChange={NOOP_CHANGE}
      />,
    )
    await waitFor(() => {
      expect(screen.getByTestId('shell-fs-conflict-banner')).toBeInTheDocument()
    })
  })

  it('banner hidden when bash is deny', async () => {
    const noConflictTools: AgentToolsCfg = {
      builtin: {
        policies: { bash: 'deny', write_file: 'deny' },
      },
    }
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={noConflictTools}
        onChange={NOOP_CHANGE}
      />,
    )
    await waitFor(() => {
      expect(screen.queryByTestId('shell-fs-conflict-banner')).not.toBeInTheDocument()
    })
  })

  it('banner hidden when no filesystem tool is denied', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />,
    )
    await waitFor(() => {
      expect(screen.queryByTestId('shell-fs-conflict-banner')).not.toBeInTheDocument()
    })
  })

  it('banner text is visible when conflict exists', async () => {
    const conflictTools: AgentToolsCfg = {
      builtin: {
        policies: { read_file: 'deny' },
      },
    }
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={conflictTools}
        onChange={NOOP_CHANGE}
      />,
    )
    await waitFor(() => {
      const banner = screen.getByTestId('shell-fs-conflict-banner')
      expect(banner.textContent).toMatch(/bash/i)
      expect(banner.textContent).toMatch(/won.t stop the shell/i)
    })
  })
})

// The role preset selector describe (US-D2 / #333) is retired with the
// presets: US-3 AS1 "no role preset control is shown", FR-022 and T-Q6 A, and
// the flipped test in the first describe above now pins that. Its second case,
// the gated save, is ported to the control that replaced the preset: one row's
// badge (US-3 AS8). The retired presence case ("shows Cautious, Balanced, Full
// access preset buttons") has no equivalent and is deleted; the spec's
// regression table ("Presets ... Deleted with the feature (T-Q6 A) — the
// deletion is the expected change, not a weakened test") names the preset
// cases of the editor's own suites, and this file follows the same rule.
describe('ToolsAndPermissions — a row edit saves through the re-auth gate (US-3 AS8 / #333)', () => {
  it('clicking Ask on a tool row calls updateAgentTools via runGated (re-auth gated)', async () => {
    // Given: Skills & Tools holds read_file at Allow, so Ask is a value the
    // agent may take (US-3 AS2: only a looser badge is locked).
    permissions().stored.policies = { read_file: 'allow' }
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await openFilesGroup()
    const ask = await enabledBadge('read_file', 'Ask')

    fireEvent.click(ask)

    // The real save goes through updateAgentTools (re-auth gated mutation).
    // One click is one sparse override (Hard Constraint #6, layer 2): the map
    // and override_names carry exactly the clicked tool, with the revision the
    // tab hydrated from. There is no default_policy field.
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({
          revision: TOOLS_REVISION,
          override_names: ['read_file'],
          config: expect.objectContaining({
            builtin: expect.objectContaining({
              policies: { read_file: 'ask' },
            }),
          }),
        }),
        '', // empty reAuthToken on first attempt (runGated optimistic call)
      )
    })
  })
})

describe('ToolsAndPermissions — locked agent (B-2 / US-D5 / #332)', () => {
  it('shows read-only notice for locked agents', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isLocked={true}
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(screen.getByTestId('locked-agent-readonly-notice')).toBeInTheDocument()
    })
  })

  it('does NOT call updateAgentTools for locked agent', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isLocked={true}
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })
    expect(api.updateAgentTools).not.toHaveBeenCalled()
  })
})

// W2c — field matrix (docs/internal/architecture/agent-types-field-matrix.md):
// tools_cfg is "—" for subagent_3p. The parent AgentProfile already hides the
// whole Tools & Permissions section for external agents; this component adds
// a defense-in-depth guard (disabled editor + explanatory notice + autosave
// gated off) for any other caller.
describe('ToolsAndPermissions — external-cli agent (subagent_3p) is read-only (field matrix, W2c)', () => {
  it('shows the external-CLI explanatory notice for agentType=subagent_3p', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="ext-1"
        agentType="subagent_3p"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(screen.getByTestId('external-cli-tools-notice')).toBeInTheDocument()
    })
    // The locked-agent notice is a different message and must not co-render.
    expect(screen.queryByTestId('locked-agent-readonly-notice')).toBeNull()
  })

  it('does NOT show the notice for a native agent kind (Main)', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('external-cli-tools-notice')).toBeNull()
  })

  // The role preset buttons this pair used (CHECK round 4, candidate 9) are
  // gone (US-3 AS1, T-Q6 A); the same two invariants — the editor is disabled
  // for an external worker, and no write can come out of it — are pinned on a
  // row's badges, the control that replaced them. The Main-agent test earlier in
  // this file clicks the very same badge of the very same fixture and saves, so
  // a disabled badge here is the agent kind's doing, not an unloaded tab.
  it('renders the ToolPolicyEditor disabled for agentType=subagent_3p', async () => {
    permissions().stored.policies = { read_file: 'allow' }
    renderWithQuery(
      <ToolsAndPermissions
        agentId="ext-1"
        agentType="subagent_3p"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await openFilesGroup()
    await readsSettled()
    const row = await screen.findByTestId('tool-row-read_file')
    for (const name of ['Allow', 'Ask', 'Deny']) {
      expect(within(row).getByRole('button', { name }), name).toBeDisabled()
    }
  })

  it('does NOT call updateAgentTools for agentType=subagent_3p even on a badge click attempt', async () => {
    permissions().stored.policies = { read_file: 'allow' }
    renderWithQuery(
      <ToolsAndPermissions
        agentId="ext-1"
        agentType="subagent_3p"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await openFilesGroup()
    await readsSettled()
    const row = await screen.findByTestId('tool-row-read_file')
    // A disabled native <button> does not dispatch its click handler.
    fireEvent.click(within(row).getByRole('button', { name: 'Ask' }))
    // Wait past the 500 ms autosave debounce: a save queued by this click would
    // fire inside that window, so a shorter wait could not have seen it.
    await new Promise((r) => setTimeout(r, 700))
    expect(api.updateAgentTools).not.toHaveBeenCalled()
    expect(mockRunGated).not.toHaveBeenCalled()
  })

  it('does NOT show the AutoSaveIndicator save-status row for agentType=subagent_3p', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="ext-1"
        agentType="subagent_3p"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })
    expect(screen.queryByText(/override.*Default:/i)).toBeNull()
  })
})

describe('ToolsAndPermissions — no spurious PUT on tab open (bug fix)', () => {
  it('does NOT call updateAgentTools when the Tools tab opens and data arrives from server', async () => {
    // This is the core regression test. When agentToolsData arrives from the
    // GET /agents/{id}/tools query, the component must hydrate its local editorValue
    // without triggering updateAgentTools. Previously, the load-sync useEffect
    // called onChange(agentToolsData.config) which caused the parent to mark
    // formData dirty and fire the autoSave PUT — resulting in a 403 because no
    // re-auth token was present.
    const serverConfig: AgentToolsCfg = {
      builtin: {
        policies: {},
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse(serverConfig))

    // Parent starts with a slightly different config (simulates the gap between
    // agent.tools_cfg from the main GET and the dedicated tools GET result).
    const parentConfig: AgentToolsCfg = {
      builtin: { policies: {} },
    }

    renderWithQuery(
      <ToolsAndPermissions
        agentId="react-dev"
        agentType="Main"
        tools={parentConfig}
        onChange={NOOP_CHANGE}
      />
    )

    // Wait for the tools query to settle.
    await waitFor(() => {
      expect(api.fetchAgentTools).toHaveBeenCalledWith('react-dev')
    })

    // Give time for any spurious mutation to fire.
    await new Promise((r) => setTimeout(r, 50))

    // ZERO writes must have fired — this is the regression guard.
    expect(api.updateAgentTools).not.toHaveBeenCalled()
    // And runGated must NOT have been called (no user interaction occurred).
    expect(mockRunGated).not.toHaveBeenCalled()
  })

  it('does NOT call updateAgentTools when agentToolsData differs from parent tools on open', async () => {
    // More adversarial case: server data has extra per-tool overrides that the
    // parent's toolsCfg does not. The load-sync must still not fire a PUT.
    const serverConfig: AgentToolsCfg = {
      builtin: {
        policies: { read_file: 'allow', write_file: 'deny' },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue(toolsResponse(serverConfig))

    const parentConfig: AgentToolsCfg = {
      builtin: { policies: {} },
    }

    renderWithQuery(
      <ToolsAndPermissions
        agentId="react-dev"
        agentType="Main"
        tools={parentConfig}
        onChange={NOOP_CHANGE}
      />
    )

    await waitFor(() => {
      expect(api.fetchAgentTools).toHaveBeenCalledWith('react-dev')
    })

    await new Promise((r) => setTimeout(r, 50))

    // No PUT — even when the server data differs from the parent prop.
    expect(api.updateAgentTools).not.toHaveBeenCalled()
    expect(mockRunGated).not.toHaveBeenCalled()
  })
})

describe('ToolsAndPermissions — re-auth-gated save on real edit', () => {
  // The two preset clicks that drove this describe (Cautious, Balanced) are
  // replaced by two different edits on the row that replaced them (US-3 AS8,
  // CHECK round 4, candidate 9): Deny, and Allow. With the Ask edit in the
  // describe above, each of the three values goes through the gate and is
  // saved as the value the person clicked, not as a fixed one.
  it('calls runGated then updateAgentTools when user changes policy', async () => {
    // When the user clicks a badge (a real policy change), the save must flow
    // through runGated → updateAgentTools(agentId, cfg, token). The mock
    // runGated passes '' as the token on the optimistic first attempt.
    vi.mocked(api.updateAgentTools).mockResolvedValue(toolsResponse())
    permissions().stored.policies = { read_file: 'allow' }

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    await openFilesGroup()
    const deny = await enabledBadge('read_file', 'Deny')

    // No runGated before user interaction
    expect(mockRunGated).not.toHaveBeenCalled()

    fireEvent.click(deny)

    await waitFor(() => {
      // runGated must have been called (re-auth gate ran for the user's edit)
      expect(mockRunGated).toHaveBeenCalledTimes(1)
    })

    // updateAgentTools was called through the gate with the token: one sparse
    // override for the clicked tool, at the clicked value. There is no
    // default_policy field on the wire anymore.
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({
          revision: TOOLS_REVISION,
          override_names: ['read_file'],
          config: expect.objectContaining({
            builtin: expect.objectContaining({ policies: { read_file: 'deny' } }),
          }),
        }),
        '', // token from runGated's optimistic pass
      )
    })
  })

  it('calls runGated then updateAgentTools when the person sets Allow on a row', async () => {
    vi.mocked(api.updateAgentTools).mockResolvedValue(toolsResponse())
    permissions().stored.policies = { read_file: 'allow' }

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    await openFilesGroup()
    const allow = await enabledBadge('read_file', 'Allow')

    fireEvent.click(allow)

    await waitFor(() => {
      expect(mockRunGated).toHaveBeenCalledTimes(1)
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({
          override_names: ['read_file'],
          config: expect.objectContaining({
            builtin: expect.objectContaining({ policies: { read_file: 'allow' } }),
          }),
        }),
        '', // token arg always present (re-auth gate path)
      )
    })
  })
})

describe('ToolsAndPermissions — 403 re-auth path (Spec-6 FR-12.2)', () => {
  // This suite exercises the negative path: the first save attempt returns the
  // re-auth 403, the gate intercepts it, and the retry is replayed with the
  // minted consent token. Mirrors SecuritySection.test.tsx ~line 476.
  //
  // Implementation note: useReAuthGate is mocked at the file level so dialog=null.
  // We simulate the full 2-phase gate behavior by overriding mockRunGated to:
  //   1. Call fn('') — the optimistic first attempt (no token).
  //   2. If fn('') throws a re-auth 403, call fn('reauth_tok') — the retry.
  // This validates that the component correctly wires the mutation through
  // runGated (not calling updateAgentTools directly) so the re-auth interception
  // point is present and the token is forwarded on retry.

  it('retries updateAgentTools with consent token after 403 re-auth gate fires', async () => {
    // First call (token='') throws the re-auth 403; second call (token='reauth_tok') succeeds.
    vi.mocked(api.updateAgentTools)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce(toolsResponse())

    // Override mockRunGated to simulate the 2-phase gate: detect re-auth 403,
    // then retry fn with the minted token — mirroring what the real useReAuthGate
    // hook does (runGated: fn('') → 403 → awaitToken → fn('reauth_tok')).
    mockRunGated.mockImplementation(async (fn: (token: string) => unknown) => {
      try {
        return await fn('')
      } catch (err) {
        // Detect re-auth 403 by matching the body the backend sends.
        const isReAuth =
          err instanceof ApiError &&
          err.status === 403 &&
          (err.body ?? '').includes('re-typing your password')
        if (!isReAuth) throw err
        // Replay with a minted consent token (simulates the user completing re-auth).
        return await fn('reauth_tok')
      }
    })

    const onChange = vi.fn()
    permissions().stored.policies = { read_file: 'allow' }

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={onChange}
      />
    )

    await openFilesGroup()
    const ask = await enabledBadge('read_file', 'Ask')

    // No save before user interaction.
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    // User sets Ask on the row (US-3 AS8; the role preset this test clicked
    // before is retired, US-3 AS1) — triggers the gated save.
    fireEvent.click(ask)

    // The gate ran: first attempt with '' returned 403, gate retried with 'reauth_tok'.
    await waitFor(() => {
      expect(mockRunGated).toHaveBeenCalledTimes(1)
      expect(api.updateAgentTools).toHaveBeenCalledTimes(2)
    })

    // First attempt: no token (optimistic).
    expect(vi.mocked(api.updateAgentTools).mock.calls[0][2]).toBe('')
    // Retry: consent token forwarded.
    expect(vi.mocked(api.updateAgentTools).mock.calls[1][2]).toBe('reauth_tok')

    // After success, onChange must have been called to sync parent state.
    await waitFor(() => {
      expect(onChange).toHaveBeenCalledWith(DEFAULT_TOOLS_CFG)
    })
  })

  it('does NOT fire a spurious PUT when the Tools tab opens (hydration-guard still holds after fix)', async () => {
    // Regression guard: the debounce/skip-while-pending fix must not break the
    // hydration guard. No user interaction = no save.
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    await waitFor(() => {
      expect(api.fetchAgentTools).toHaveBeenCalledWith('agent-1')
    })

    await new Promise((r) => setTimeout(r, 50))

    expect(api.updateAgentTools).not.toHaveBeenCalled()
    expect(mockRunGated).not.toHaveBeenCalled()
  })
})

describe('ToolsAndPermissions — latest-wins: no edit dropped during in-flight save', () => {
  // Regression test for the isPending-drop bug. Previously, handleEditorChange
  // had `if (saveMutation.isPending) return` which silently dropped every edit
  // made while a save was in-flight, and the onSuccess→refetch snap-back
  // reverted the editor to the stale saved config.
  //
  // The fix: useAutoSave debounces edits with a ref (latestDataRef) so the
  // latest value is ALWAYS persisted — coalesced rapid edits into a trailing
  // save — and isDraftReady gates hydration so the editor never snaps back.
  //
  // FIX 3 (useAutoSave serialization, agent fallback_models UAT bug):
  // useAutoSave now also guarantees at most ONE save is ever
  // in flight — a debounce firing while a prior save is still outstanding no
  // longer dispatches a second, concurrent request (which previously could
  // let a stale save's response arrive at the server AFTER a fresher one and
  // silently clobber it via last-write-wins full-resource PUT semantics).
  // Instead it queues and re-fires against the LATEST data once the
  // outstanding request settles. This test's assertions are updated to match:
  // C's save no longer fires WHILE A is in-flight — it fires immediately
  // after A settles, still carrying C's (latest) data.
  //
  // The role presets this describe used for its three edits are retired (US-3
  // AS1, T-Q6 A; CHECK round 4, candidate 9). The three edits are now the
  // person's three badge clicks on two rows: the invariants they exercise
  // (debounce, one save in flight, latest wins, no snap-back) are unchanged.
  //
  // Test strategy (fake timers throughout, except during initial data load):
  //   1. Load editor (real timers for queries to resolve).
  //   2. Switch to fake timers.
  //   3. Edit A: read_file → Ask → debounce starts.
  //   4. Advance 600ms → A's save fires (blocked by a deferred promise).
  //   5. While in-flight: edit B: read_file → Deny → debounce starts.
  //   6. While in-flight: edit C: write_file → Ask → debounce resets.
  //   7. Advance 600ms → C's debounce fires, but the save is QUEUED (not
  //      dispatched) because A is still in flight — still only 1 call.
  //   8. Resolve A's save → the queued save for C fires immediately after,
  //      as the second updateAgentTools call, carrying the person's latest
  //      state: read_file Deny (B) and write_file Ask (C).
  //   9. Assert updateAgentTools was called exactly twice, and the SECOND
  //      call carried both rows' latest values (latest-wins), not A's Ask on
  //      read_file (stale A) and not B's state alone.

  it('latest-wins: edits B and C made while A is in-flight are not dropped; C persists', async () => {
    // Add write_file to the registry so edit C lands on a different row from
    // edits A and B. The saved map after C then holds BOTH rows' latest values
    // (read_file Deny, write_file Ask), which no earlier edit alone produces:
    // from the PUT payload alone we can tell "C won" from "A persisted" or
    // "B persisted". With one row only, C would collapse onto A or B.
    const WRITE_FILE_TOOL: RegistryTool = {
      name: 'write_file', scope: 'general', category: 'filesystem', description: 'Write file', source: 'builtin',
      visibility: 'full',
      visibility_locked: false,
      permission_fixed: false,
    }
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL, WRITE_FILE_TOOL])
    vi.mocked(api.fetchBuiltinTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL, WRITE_FILE_TOOL])
    // Both rows' global value is Allow, so Ask and Deny are the agent's own,
    // stricter values (US-3: an agent value only ever tightens the global one).
    permissions().stored.policies = { read_file: 'allow', write_file: 'allow' }

    // Deferred promise to block the FIRST updateAgentTools call (A's save).
    let resolveFirstSave!: (v: ReturnType<typeof toolsResponse>) => void
    const firstSavePromise = new Promise<ReturnType<typeof toolsResponse>>(
      (resolve) => { resolveFirstSave = resolve },
    )

    // The state the person ends on (edits B and C) — the expected final
    // persisted value, as the server would acknowledge it.
    const finalCfg = {
      builtin: { policies: { read_file: 'deny', write_file: 'ask' } },
    }

    vi.mocked(api.updateAgentTools)
      .mockReturnValueOnce(firstSavePromise) // A: blocked
      .mockResolvedValue(toolsResponse(finalCfg as typeof DEFAULT_TOOLS_CFG)) // C and beyond

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    // Wait for the editor to hydrate with REAL timers (queries are async).
    // Enablement (not just presence) of both rows' badges confirms hydration
    // landed before the fake-timer section clicks them.
    await openFilesGroup()
    await enabledBadge('read_file', 'Ask')
    await enabledBadge('write_file', 'Ask')
    // Confirm isDraftReady gate opened (agentToolsData arrived).
    await waitFor(() => {
      expect(api.fetchAgentTools).toHaveBeenCalledWith('agent-1')
    })
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    // Switch to fake timers NOW — queries have resolved; only debounce timers remain.
    vi.useFakeTimers()

    // Edit A: read_file → Ask.
    fireEvent.click(rowBadge('read_file', 'Ask'))

    // Advance past the useAutoSave debounce (500ms) → A's save fires.
    await act(async () => { vi.advanceTimersByTime(600) })

    // A's save is now in-flight (blocked by firstSavePromise). It carries the
    // person's one edit and nothing else: the agent's overrides are sparse.
    expect(api.updateAgentTools).toHaveBeenCalledTimes(1)
    const firstCallCfg = vi.mocked(api.updateAgentTools).mock.calls[0][1]
    expect(firstCallCfg).toMatchObject({ revision: TOOLS_REVISION })
    // `config` is optional in AgentToolsUpdateRequest; a body without it fails
    // this equality (undefined is not the map), so the chain adds no leniency.
    expect(firstCallCfg.config?.builtin?.policies).toEqual({ read_file: 'ask' })

    // Edit B while A is in-flight: read_file → Deny.
    // Does NOT call updateAgentTools yet — the debounce resets.
    fireEvent.click(rowBadge('read_file', 'Deny'))
    await act(async () => { vi.advanceTimersByTime(100) })

    // Edit C while A is still in-flight: write_file → Ask.
    // B's debounce was cleared; C starts a fresh 500ms debounce.
    fireEvent.click(rowBadge('write_file', 'Ask'))
    await act(async () => { vi.advanceTimersByTime(100) })

    // A is still blocked — only 1 call so far. B and C are still debouncing.
    expect(api.updateAgentTools).toHaveBeenCalledTimes(1)

    // Advance past C's debounce (500ms total − 100ms already passed = 400ms more).
    // C's debounce fires, but useAutoSave is now serialized (FIX 3): since A
    // is still in flight, C's save is QUEUED rather than dispatched — still
    // only 1 call. This is the behavior that closes the UAT data-loss bug:
    // no second request goes out while one is outstanding, so there is no
    // network-ordering race for the server to resolve incorrectly.
    await act(async () => { vi.advanceTimersByTime(500) })
    expect(api.updateAgentTools).toHaveBeenCalledTimes(1)

    // Now resolve A — the queued save for C fires immediately afterward,
    // as the second call, strictly AFTER A settled (never concurrently).
    resolveFirstSave(toolsResponse(DEFAULT_TOOLS_CFG, '2'.repeat(64)))
    // Allow Promise microtasks to flush.
    await act(async () => { vi.advanceTimersByTime(100) })

    expect(api.updateAgentTools).toHaveBeenCalledTimes(2)

    // The second call carries the person's latest state of BOTH rows (C wins,
    // and B's earlier edit is not lost): read_file Deny is neither A's Ask
    // (stale A) nor absent (a dropped B), and write_file Ask exists only in C.
    // The map is exactly those two rows: the overrides stay sparse.
    const secondCallCfg = vi.mocked(api.updateAgentTools).mock.calls[1][1]
    expect(secondCallCfg).toMatchObject({ revision: '2'.repeat(64) })
    expect(secondCallCfg.config?.builtin?.policies).toEqual({ read_file: 'deny', write_file: 'ask' })
    // Order is not part of the contract; both edited rows are the agent's own.
    expect([...secondCallCfg.override_names].sort()).toEqual(['read_file', 'write_file'])

    // No further save fires — resolving A's queued follow-up did not
    // trigger a spurious third save.
    await act(async () => { vi.advanceTimersByTime(100) })
    expect(api.updateAgentTools).toHaveBeenCalledTimes(2)
  })

  it('editor does NOT snap back to stale A config after A saves while C is the latest edit', async () => {
    // Additional check: after A saves and invalidates the cache, the agentToolsData
    // refetch must NOT overwrite the editor with A's stale value. The isDraftReady
    // gate blocks the hydration useEffect once the user has edited.
    const staleCfg = {
      builtin: { policies: {} },
    }

    // fetchAgentTools: first call returns DEFAULT, re-fetch after A saves returns
    // a config without A's edit (a stale read). This simulates the snap-back
    // scenario: were the draft to take it, the editor would drop the person's
    // Ask, which differs from the draft and would fire a second save.
    vi.mocked(api.fetchAgentTools)
      .mockResolvedValueOnce(toolsResponse())
      .mockResolvedValue(toolsResponse(staleCfg as typeof DEFAULT_TOOLS_CFG))

    vi.mocked(api.updateAgentTools).mockResolvedValue(toolsResponse(staleCfg as typeof DEFAULT_TOOLS_CFG))
    permissions().stored.policies = { read_file: 'allow' }

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    // Wait for initial hydration (real timers). Enablement, not just
    // presence — the badge click below must be post-hydration.
    await openFilesGroup()
    const ask = await enabledBadge('read_file', 'Ask')
    await waitFor(() => expect(api.fetchAgentTools).toHaveBeenCalledWith('agent-1'))
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    // Edit A: read_file → Ask — fires after debounce (real timers, waitFor will wait).
    fireEvent.click(ask)

    // Wait for A's save to fire (useAutoSave debounces 500ms; waitFor polls up to 2s).
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledTimes(1)
    }, { timeout: 2000 })

    // After A saves, the cache is invalidated and fetchAgentTools re-fires
    // returning staleCfg. The editor must NOT revert because isDraftReady
    // blocks the hydration effect once the user has edited.
    // Give the refetch + any re-render time to propagate.
    await new Promise((r) => setTimeout(r, 200))

    // No second save must have fired from the snap-back. A snap-back would
    // change editorValue → useAutoSave would see a diff → debounce → second PUT.
    // Waiting 700ms (> debounce) to be sure no trailing save appears.
    await new Promise((r) => setTimeout(r, 700))
    expect(api.updateAgentTools).toHaveBeenCalledTimes(1)
  })
})
