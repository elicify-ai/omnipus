// Unit tests for ToolsAndPermissions — FR-027, FR-029, FR-086, MAJ-008
//
// Updated for #333 (US-D2): ToolsAndPermissions now delegates to the shared
// ToolPolicyEditor. The four ad-hoc presets (Unrestricted/Cautious/Standard/Minimal)
// are replaced by Cautious/Balanced/Full access role presets.
//
// Individual tool rows are now inside collapsed CategorySection accordions.
// Fence badge (FR-086 / MAJ-008) is no longer rendered inline in ToolsAndPermissions
// — it was part of the old raw-grid UI that was replaced by ToolPolicyEditor.
//
// Tests:
//  1. Calls fetchRegistryTools (GET /api/v1/tools)
//  2. Source badge for MCP tools visible in the MCP section
//  3. Shell/fs conflict banner (still rendered by ToolsAndPermissions directly)
//  4. ToolPolicyEditor role preset buttons (Cautious/Balanced/Full access) present
//  5. No write fires for locked agents (B-2 / #332)
//  6. NO spurious PUT fires when opening Tools tab (server hydration ≠ user edit)
//  7. Real policy change triggers the re-auth-gated save path (not a bare 403)
//  8. 403 re-auth path: gate detects re-auth 403, retries with token (Spec-6 FR-12.2)
//  9. Latest-wins: edits made while a save is in-flight are not dropped (bug fix)

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Mock the API module. updateAgentTools now accepts an optional reAuthToken.
vi.mock('@/lib/api', () => ({
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGlobalToolPolicies: vi.fn(),
  reAuth: vi.fn(),
  isApiError: vi.fn(() => false),
  REAUTH_HEADER: 'X-Reauth-Token',
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
// ApiError must be imported from its own module, not from the @/lib/api mock,
// because the mock factory for @/lib/api does not re-export class constructors.
import { ApiError } from '@/lib/api-error'
import { ToolsAndPermissions } from './ToolsAndPermissions'

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
}

const MCP_TOOL: RegistryTool = {
  name: 'mcp_search',
  scope: 'general',
  category: 'web',
  description: 'Search via MCP',
  source: 'mcp',
}

const ADMIN_TOOL: RegistryTool = {
  name: 'system.config.set',
  scope: 'core',
  category: 'system',
  description: 'Set system configuration',
  source: 'builtin',
}

const DEFAULT_TOOLS_CFG: AgentToolsCfg = {
  builtin: {
    policies: {},
  },
}

const NOOP_CHANGE = () => {}

beforeEach(() => {
  vi.clearAllMocks()
  // Reset the runGated spy back to the pass-through default each test.
  mockRunGated.mockImplementation(async (fn: (token: string) => unknown) => fn(''))

  vi.mocked(api.fetchRegistryTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL])
  vi.mocked(api.fetchAgentTools).mockResolvedValue({
    config: DEFAULT_TOOLS_CFG,
    tools: [],
  })
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({
    policies: {},
  })
  // Default: updateAgentTools succeeds and returns the same config
  vi.mocked(api.updateAgentTools).mockResolvedValue({
    config: DEFAULT_TOOLS_CFG,
    tools: [],
  })
})

afterEach(() => {
  vi.useRealTimers()
})

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

  it('renders ToolPolicyEditor (role preset buttons) after registry loads', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      // New role presets (Cautious/Balanced/Full access) are shown instead of old presets
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="preset-balanced"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="preset-full_access"]')).toBeInTheDocument()
    })
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
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: {
        builtin: { policies: { 'system.config.set': 'allow' } },
      },
      tools: [],
    })
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
  }
  const FS_TOOLS: RegistryTool[] = [
    { name: 'write_file', scope: 'general', category: 'filesystem', description: 'Write file', source: 'builtin' },
    { name: 'read_file', scope: 'general', category: 'filesystem', description: 'Read file', source: 'builtin' },
    { name: 'list_directory', scope: 'general', category: 'filesystem', description: 'List directory', source: 'builtin' },
  ]

  beforeEach(() => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([SHELL_TOOL, ...FS_TOOLS])
    vi.mocked(api.fetchAgentTools).mockResolvedValue({ config: DEFAULT_TOOLS_CFG, tools: [] })
    vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({ policies: {} })
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

describe('ToolsAndPermissions — role preset selector (US-D2 / #333)', () => {
  it('shows Cautious, Balanced, Full access preset buttons', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="preset-balanced"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="preset-full_access"]')).toBeInTheDocument()
    })
  })

  it('clicking Cautious preset calls updateAgentTools via runGated (re-auth gated)', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })

    fireEvent.click(document.querySelector('[data-testid="preset-cautious"]')!)

    // The real save goes through updateAgentTools (re-auth gated mutation).
    // The registry for this describe block is [read_file, mcp_search] (see
    // beforeEach) — Cautious expands to a COMPLETE map over exactly those
    // tools, each explicitly set to 'ask'. There is no default_policy field.
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({
          builtin: expect.objectContaining({
            policies: { read_file: 'ask', mcp_search: 'ask' },
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

  it('renders the ToolPolicyEditor disabled for agentType=subagent_3p', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="ext-1"
        agentType="subagent_3p"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })
    const cautious = document.querySelector('[data-testid="preset-cautious"]') as HTMLButtonElement
    expect(cautious.disabled).toBe(true)
  })

  it('does NOT call updateAgentTools for agentType=subagent_3p even on a preset click attempt', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="ext-1"
        agentType="subagent_3p"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )
    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })
    // A disabled native <button> does not dispatch its click handler.
    fireEvent.click(document.querySelector('[data-testid="preset-cautious"]')!)
    await new Promise((r) => setTimeout(r, 50))
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
    vi.mocked(api.fetchAgentTools).mockResolvedValue({ config: serverConfig, tools: [] })

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
    vi.mocked(api.fetchAgentTools).mockResolvedValue({ config: serverConfig, tools: [] })

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
  it('calls runGated then updateAgentTools when user changes policy', async () => {
    // When the user clicks a preset (a real policy change), the save must flow
    // through runGated → updateAgentTools(agentId, cfg, token). The mock
    // runGated passes '' as the token on the optimistic first attempt.
    vi.mocked(api.updateAgentTools).mockResolvedValue({ config: DEFAULT_TOOLS_CFG, tools: [] })

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })

    // No runGated before user interaction
    expect(mockRunGated).not.toHaveBeenCalled()

    fireEvent.click(document.querySelector('[data-testid="preset-cautious"]')!)

    await waitFor(() => {
      // runGated must have been called (re-auth gate ran for the user's edit)
      expect(mockRunGated).toHaveBeenCalledTimes(1)
    })

    // updateAgentTools was called through the gate with the token. The
    // registry is [read_file, mcp_search] (beforeEach) — Cautious expands to
    // a complete map over exactly those tools, each set to 'ask'. There is no
    // default_policy field on the wire anymore.
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({
          builtin: expect.objectContaining({ policies: { read_file: 'ask', mcp_search: 'ask' } }),
        }),
        '', // token from runGated's optimistic pass
      )
    })
  })

  it('calls runGated then updateAgentTools when switching to Balanced preset', async () => {
    vi.mocked(api.updateAgentTools).mockResolvedValue({ config: DEFAULT_TOOLS_CFG, tools: [] })

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-balanced"]')).toBeInTheDocument()
    })

    fireEvent.click(document.querySelector('[data-testid="preset-balanced"]')!)

    await waitFor(() => {
      expect(mockRunGated).toHaveBeenCalledTimes(1)
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.any(Object),
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
      .mockResolvedValueOnce({ config: DEFAULT_TOOLS_CFG, tools: [] })

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

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={onChange}
      />
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })

    // No save before user interaction.
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    // User clicks the Cautious preset — triggers the gated save.
    fireEvent.click(document.querySelector('[data-testid="preset-cautious"]')!)

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
  // FIX 3 (useAutoSave serialization, agent fallback_models UAT bug,
  // 2026-07-11): useAutoSave now also guarantees at most ONE save is ever
  // in flight — a debounce firing while a prior save is still outstanding no
  // longer dispatches a second, concurrent request (which previously could
  // let a stale save's response arrive at the server AFTER a fresher one and
  // silently clobber it via last-write-wins full-resource PUT semantics).
  // Instead it queues and re-fires against the LATEST data once the
  // outstanding request settles. This test's assertions are updated to match:
  // C's save no longer fires WHILE A is in-flight — it fires immediately
  // after A settles, still carrying C's (latest) data.
  //
  // Test strategy (fake timers throughout, except during initial data load):
  //   1. Load editor (real timers for queries to resolve).
  //   2. Switch to fake timers.
  //   3. Click preset A (Cautious) → debounce starts.
  //   4. Advance 600ms → A's save fires (blocked by a deferred promise).
  //   5. While in-flight: click preset B (Balanced) → debounce starts.
  //   6. While in-flight: click preset C (Full access) → debounce resets.
  //   7. Advance 600ms → C's debounce fires, but the save is QUEUED (not
  //      dispatched) because A is still in flight — still only 1 call.
  //   8. Resolve A's save → the queued save for C fires immediately after,
  //      as the second updateAgentTools call, carrying C's config.
  //   9. Assert updateAgentTools was called exactly twice, and the SECOND
  //      call used Full access config (latest-wins), not Cautious (stale A).

  it('latest-wins: edits B and C made while A is in-flight are not dropped; C persists', async () => {
    // Add write_file to the registry so Balanced (which sets write_file:'ask'
    // per the §2.1 override table) and Full access (write_file:'allow')
    // produce genuinely DIFFERENT complete maps. With only read_file/mcp_search
    // in scope neither preset has an override for either tool, so Balanced and
    // Full access would collapse into an identical map and we could no longer
    // tell "C won" from "B won" from the PUT payload alone.
    const WRITE_FILE_TOOL: RegistryTool = {
      name: 'write_file', scope: 'general', category: 'filesystem', description: 'Write file', source: 'builtin',
    }
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL, WRITE_FILE_TOOL])
    vi.mocked(api.fetchBuiltinTools).mockResolvedValue([BUILTIN_TOOL, MCP_TOOL, WRITE_FILE_TOOL])

    // Deferred promise to block the FIRST updateAgentTools call (A's save).
    let resolveFirstSave!: (v: { config: typeof DEFAULT_TOOLS_CFG; tools: [] }) => void
    const firstSavePromise = new Promise<{ config: typeof DEFAULT_TOOLS_CFG; tools: [] }>(
      (resolve) => { resolveFirstSave = resolve },
    )

    // Full access config (edit C) — this is the expected final persisted value.
    const fullAccessCfg = {
      builtin: { policies: { read_file: 'allow', mcp_search: 'allow', write_file: 'allow' } },
    }

    vi.mocked(api.updateAgentTools)
      .mockReturnValueOnce(firstSavePromise) // A: blocked
      .mockResolvedValue({ config: fullAccessCfg as typeof DEFAULT_TOOLS_CFG, tools: [] }) // C and beyond

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    // Wait for the editor to hydrate with REAL timers (queries are async).
    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })
    // Confirm isDraftReady gate opened (agentToolsData arrived).
    await waitFor(() => {
      expect(api.fetchAgentTools).toHaveBeenCalledWith('agent-1')
    })
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    // Switch to fake timers NOW — queries have resolved; only debounce timers remain.
    vi.useFakeTimers()

    // Edit A: click Cautious preset — complete map, every known tool → 'ask'.
    fireEvent.click(document.querySelector('[data-testid="preset-cautious"]')!)

    // Advance past the useAutoSave debounce (500ms) → A's save fires.
    await act(async () => { vi.advanceTimersByTime(600) })

    // A's save is now in-flight (blocked by firstSavePromise).
    expect(api.updateAgentTools).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.updateAgentTools).mock.calls[0][1]).toMatchObject({
      builtin: expect.objectContaining({
        policies: { read_file: 'ask', mcp_search: 'ask', write_file: 'ask' },
      }),
    })

    // Edit B while A is in-flight: Balanced (write_file → 'ask' per §2.1;
    // read_file/mcp_search → 'allow', no override for either).
    // Does NOT call updateAgentTools yet — the debounce resets.
    fireEvent.click(document.querySelector('[data-testid="preset-balanced"]')!)
    await act(async () => { vi.advanceTimersByTime(100) })

    // Edit C while A is still in-flight: Full access (every tool → 'allow').
    // B's debounce was cleared; C starts a fresh 500ms debounce.
    fireEvent.click(document.querySelector('[data-testid="preset-full_access"]')!)
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
    resolveFirstSave({ config: DEFAULT_TOOLS_CFG, tools: [] })
    // Allow Promise microtasks to flush.
    await act(async () => { vi.advanceTimersByTime(100) })

    expect(api.updateAgentTools).toHaveBeenCalledTimes(2)

    // The second call must use Full access's complete map (C wins, not A or B) —
    // write_file:'allow' distinguishes it from Balanced's write_file:'ask'.
    const secondCallCfg = vi.mocked(api.updateAgentTools).mock.calls[1][1]
    expect(secondCallCfg).toMatchObject({
      builtin: expect.objectContaining({
        policies: { read_file: 'allow', mcp_search: 'allow', write_file: 'allow' },
      }),
    })

    // No further save fires — resolving A's queued follow-up did not
    // trigger a spurious third save.
    await act(async () => { vi.advanceTimersByTime(100) })
    expect(api.updateAgentTools).toHaveBeenCalledTimes(2)
  })

  it('editor does NOT snap back to stale A config after A saves while C is the latest edit', async () => {
    // Additional check: after A saves and invalidates the cache, the agentToolsData
    // refetch must NOT overwrite the editor with A's stale value. The isDraftReady
    // gate blocks the hydration useEffect once the user has edited.
    const cautionsCfg = {
      builtin: { policies: {} },
    }

    // fetchAgentTools: first call returns DEFAULT, re-fetch after A saves returns
    // Cautious (the value A persisted). This simulates the stale snap-back scenario.
    vi.mocked(api.fetchAgentTools)
      .mockResolvedValueOnce({ config: DEFAULT_TOOLS_CFG, tools: [] })
      .mockResolvedValue({ config: cautionsCfg as typeof DEFAULT_TOOLS_CFG, tools: [] })

    vi.mocked(api.updateAgentTools).mockResolvedValue({
      config: cautionsCfg as typeof DEFAULT_TOOLS_CFG,
      tools: [],
    })

    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={NOOP_CHANGE}
      />
    )

    // Wait for initial hydration (real timers).
    await waitFor(() => {
      expect(document.querySelector('[data-testid="preset-cautious"]')).toBeInTheDocument()
    })
    await waitFor(() => expect(api.fetchAgentTools).toHaveBeenCalledWith('agent-1'))
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    // Edit A: Cautious — fires after debounce (real timers, waitFor will wait).
    fireEvent.click(document.querySelector('[data-testid="preset-cautious"]')!)

    // Wait for A's save to fire (useAutoSave debounces 500ms; waitFor polls up to 2s).
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledTimes(1)
    }, { timeout: 2000 })

    // After A saves, the cache is invalidated and fetchAgentTools re-fires
    // returning cautionsCfg. The editor must NOT revert because isDraftReady
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
