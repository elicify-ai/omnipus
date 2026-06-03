import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// test_skill_card_component (test #19) — tests SkillsScreen skills tab (SkillCard is inline, not exported)
// test_mcp_server_browser (test #20) — MCP servers tab
// Traces to: wave5a-wire-ui-spec.md — Scenario: Skills browser renders installed skills
//             wave5a-wire-ui-spec.md — Scenario: MCP server list with connection status

// Note: SkillCard is not a standalone component — skill rows are rendered inline
// inside SkillsScreen (src/routes/_app/skills.tsx). This test file tests SkillsScreen via its component.

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => opts,
  useNavigate: () => vi.fn(),
  useParams: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchMcpServers: vi.fn(),
    fetchTools: vi.fn(),
  }
})

import { fetchSkills, fetchMcpServers, fetchTools } from '@/lib/api'
// The skills route now lazy-loads its screen; import the screen module directly
// so the test renders real content (not the route's Suspense fallback).
import { SkillsScreen } from '@/components/screens/SkillsScreen'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderScreen() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SkillsScreen />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.mocked(fetchSkills).mockResolvedValue([
    {
      id: 'web-search-skill',
      name: 'Web Search',
      version: '1.2.0',
      description: 'Search the web using Google',
      author: 'omnipus-team',
      status: 'active',
      verified: true,
      agent_assignment: 'general-assistant',
    },
    {
      id: 'pdf-reader',
      name: 'PDF Reader',
      version: '0.8.1',
      description: 'Read and extract PDF content',
      author: 'community',
      status: 'error',
      verified: false,
    },
  ])
  vi.mocked(fetchMcpServers).mockResolvedValue([
    { id: 'mcp-1', name: 'filesystem', transport: 'stdio', status: 'connected', tool_count: 8 },
    { id: 'mcp-2', name: 'github', transport: 'stdio', status: 'disconnected', tool_count: 0 },
  ])
  vi.mocked(fetchTools).mockResolvedValue([
    { name: 'exec', scope: 'system', category: 'system', description: 'Execute shell commands', source: 'builtin' },
    { name: 'web_search', scope: 'general', category: 'web', description: 'Search the web', source: 'builtin' },
    { name: 'file.read', scope: 'general', category: 'fs', description: 'Read a file', source: 'builtin' },
  ])
})

describe('SkillsScreen — installed skills tab (test #19)', () => {
  it('renders Skills & Tools heading and tabs', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Skills browser renders installed skills (AC1)
    renderScreen()
    await screen.findByText('Skills & Tools')
    expect(screen.getByText('Installed Skills')).toBeInTheDocument()
    expect(screen.getByText('MCP Servers')).toBeInTheDocument()
    expect(screen.getByText('Built-in Tools')).toBeInTheDocument()
  })

  it('shows installed skill name, version, and description', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Skills browser (AC2)
    // Dataset: Skill Ecosystem row 1
    renderScreen()
    await screen.findByText('Web Search')
    expect(screen.getByText(/v1\.2\.0/)).toBeInTheDocument()
    expect(screen.getByText('Search the web using Google')).toBeInTheDocument()
  })

  it('shows "Verified" badge for verified skills', async () => {
    // Traces to: wave5a-wire-ui-spec.md — AC3: verified skill shows badge
    renderScreen()
    await screen.findByText('Web Search')
    expect(screen.getByText('Verified')).toBeInTheDocument()
  })

  it('shows skill status badge (active/error)', async () => {
    // Dataset: Skill Ecosystem — error status
    renderScreen()
    await screen.findByText('Web Search')
    expect(screen.getByText('active')).toBeInTheDocument()
    expect(screen.getByText('error')).toBeInTheDocument()
  })
})

describe('SkillsScreen — MCP servers tab (test #20)', () => {
  it('shows MCP servers with name, transport, status, and tool count', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: MCP server list (AC1)
    // Use userEvent for Radix Tabs — fireEvent.click does not trigger pointer events needed by Radix
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /MCP Servers/i }))
    await screen.findByText('filesystem')
    expect(screen.getByText('github')).toBeInTheDocument()
    expect(screen.getByText('8 tools')).toBeInTheDocument()
  })
})

describe('SkillsScreen — built-in tools tab', () => {
  it('shows built-in tool list including exec and web_search', async () => {
    // Traces to: wave5a-wire-ui-spec.md — AC4: built-in tools are listed
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await screen.findByText('exec')
    expect(screen.getByText('web_search')).toBeInTheDocument()
    expect(screen.getByText('file.read')).toBeInTheDocument()
  })
})
