import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockRunGated = vi.fn(async (fn: (token: string) => unknown) => fn(''))

// The agent-tools edge of the API module is doubled. The global value is not:
// the real client reads it from GET /security/permissions through the fetch
// double below (C-22 row 6 retires GET /security/tool-policies, this tab's
// last reader).
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

vi.mock('@/components/settings/useReAuthGate', () => ({
  useReAuthGate: () => ({ runGated: mockRunGated, dialog: null, open: false }),
  isReAuthCancelled: vi.fn(() => false),
}))

vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))

vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: () => void }) => unknown) => selector({ addToast: vi.fn() }),
}))

import * as api from '@/lib/api'
import type { RegistryTool, AgentToolsCfg, McpServer } from '@/lib/api'
import { ToolsAndPermissions } from './ToolsAndPermissions'
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

// The global value this file's earlier global-policies double returned
// (read_file Allow). Only the fetch edge is doubled.
useSkillPermissionNetwork(() => ({
  policies: { read_file: 'allow' },
  skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))

const READ_FILE: RegistryTool = {
  name: 'read_file',
  scope: 'general',
  category: 'filesystem',
  description: 'Read file contents',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}
const REVISION = '2'.repeat(64)
const SERVER: McpServer = {
  id: 'docs',
  name: 'Docs',
  enabled: true,
  transport: 'stdio',
  status: 'connected',
  tool_count: 2,
}

function renderWithQuery(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  mockRunGated.mockImplementation(async (fn: (token: string) => unknown) => fn(''))
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([READ_FILE])
  vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: false, persisted: false, available: true, supported: true })
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([SERVER])
  vi.mocked(api.updateAgentTools).mockImplementation(async (_id, cfg) => ({
    revision: REVISION,
    override_names: cfg.override_names,
    config: cfg.config ?? { builtin: { policies: {} } },
    tools: [],
    skills: [],
  }))
})

afterEach(() => { vi.useRealTimers() })

// Browser acceptance run 3 (2026-09-18, .local/adr090/mcp-browser-acceptance-3)
// directly captured only the failure surface: a 30 s PUT timeout with no
// request ever sent. The click-in-window timing - the Assign switch clicked
// while GET /agents/{id}/tools was still in flight, the edit overwritten by
// the hydration effect while isDraftReady was false - is the diagnosis
// inferred from source, not an observation recorded by the run. The
// deferred-query test below reproduces that window deterministically and is
// what actually proves the mechanism. The controls must be inert until
// hydration lands.
describe('ToolsAndPermissions - pre-hydration edit window', () => {
  it('keeps connector assignment disabled until the tools draft hydrates', async () => {
    let resolveHydration!: (value: Awaited<ReturnType<typeof api.fetchAgentTools>>) => void
    vi.mocked(api.fetchAgentTools).mockImplementation(
      () => new Promise((resolve) => { resolveHydration = resolve }),
    )
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    // The MCP section renders from the mcp-servers query alone - hydration
    // is still pending here, which is exactly the run 3 window.
    expect(await screen.findByTestId('mcp-assignment-section')).toBeInTheDocument()
    expect(screen.getByLabelText('Assign Docs')).toBeDisabled()

    resolveHydration({
      revision: REVISION,
      override_names: [],
      config: { builtin: { policies: { read_file: 'allow' } }, mcp: { servers: [] } },
      tools: [],
      skills: [],
    })
    await waitFor(() => expect(screen.getByLabelText('Assign Docs')).toBeEnabled())
    fireEvent.click(screen.getByLabelText('Assign Docs'))
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'mia',
        expect.objectContaining({
          config: expect.objectContaining({ mcp: { servers: [{ id: 'docs' }] } }),
        }),
        '',
      )
    }, { timeout: 5000 })
  })
})
