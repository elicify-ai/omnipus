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

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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
import { ToolsAndPermissions } from './ToolsAndPermissions'

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
    default_policy: 'allow',
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
    default_policy: 'allow',
    policies: {},
  })
  // Default: updateAgentTools succeeds and returns the same config
  vi.mocked(api.updateAgentTools).mockResolvedValue({
    config: DEFAULT_TOOLS_CFG,
    tools: [],
  })
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
        builtin: { default_policy: 'allow', policies: { 'system.config.set': 'allow' } },
      },
      tools: [],
    })
  })

  it('system.* tool (category=system) appears in the flat category grid, NOT a separate disclosure', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={{ builtin: { default_policy: 'allow', policies: { 'system.config.set': 'allow' } } }}
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
        tools={{ builtin: { default_policy: 'allow', policies: { 'system.config.set': 'allow' } } }}
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
        tools={{ builtin: { default_policy: 'allow', policies: { 'system.config.set': 'allow' } } }}
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
    name: 'workspace_shell',
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
    vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({ default_policy: 'allow', policies: {} })
  })

  it('banner renders when workspace_shell is allow and a filesystem tool is deny', async () => {
    const conflictTools: AgentToolsCfg = {
      builtin: {
        default_policy: 'allow',
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

  it('banner hidden when workspace_shell is deny', async () => {
    const noConflictTools: AgentToolsCfg = {
      builtin: {
        default_policy: 'allow',
        policies: { workspace_shell: 'deny', write_file: 'deny' },
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
        default_policy: 'allow',
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
      expect(banner.textContent).toMatch(/workspace_shell/i)
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
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({
          builtin: expect.objectContaining({
            default_policy: 'ask',
            policies: {},
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
        default_policy: 'allow',
        policies: {},
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({ config: serverConfig, tools: [] })

    // Parent starts with a slightly different config (simulates the gap between
    // agent.tools_cfg from the main GET and the dedicated tools GET result).
    const parentConfig: AgentToolsCfg = {
      builtin: { default_policy: 'allow', policies: {} },
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
        default_policy: 'deny',
        policies: { read_file: 'allow', write_file: 'deny' },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({ config: serverConfig, tools: [] })

    const parentConfig: AgentToolsCfg = {
      builtin: { default_policy: 'allow', policies: {} },
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

    // updateAgentTools was called through the gate with the token
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'agent-1',
        expect.objectContaining({ builtin: expect.objectContaining({ default_policy: 'ask' }) }),
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
