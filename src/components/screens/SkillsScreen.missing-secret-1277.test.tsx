import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { McpServer } from '@/lib/api/generated/openapi-types'

// Mock the external API retrieval edge, never SkillsScreen, the MCP cards,
// Query, Tabs or their rendering of the generated status/reason payload.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchMcpServers: vi.fn(),
    fetchTools: vi.fn().mockResolvedValue([]),
  }
})

import { fetchMcpServers } from '@/lib/api'
import { SkillsScreen } from './SkillsScreen'

// #1277 founder 2026-10-12; accepted CHECK F3: the REST diagnostic must be
// visible when a person opens MCP Servers, not just a bare error badge.
const missingRef = 'QA1277_UI_MCP_TOKEN'
const reason = `Secret reference ${missingRef} is missing from the credential store.`
const bad: McpServer = {
  id: 'qa-broken-connector',
  name: 'Broken connector',
  transport: 'http',
  enabled: true,
  status: 'error',
  status_reason: reason,
  tool_count: 0,
}
const healthy: McpServer = {
  id: 'qa-healthy-connector',
  name: 'Healthy connector',
  transport: 'http',
  enabled: true,
  status: 'connected',
  tool_count: 1,
  tools: ['qa_probe'],
}

let client: QueryClient

async function renderMcpServers(servers: McpServer[]) {
  vi.mocked(fetchMcpServers).mockResolvedValue(servers)
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(<QueryClientProvider client={client}><SkillsScreen /></QueryClientProvider>)
  await userEvent.setup().click(screen.getByRole('tab', { name: 'MCP Servers' }))
  await screen.findByText(servers[0].name)
}

function serverCard(name: string): HTMLElement {
  // Card's published base variant is rounded-lg. No test-only row export or
  // production marker is added; the actual owning card supplies the boundary.
  const card = screen.getByText(name).closest('div.rounded-lg')
  if (!(card instanceof HTMLElement)) throw new Error(`Instrument: no real server card for ${name}`)
  return card
}

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  client?.clear()
})

describe('#1277 MCP credential-error reachability', () => {
  it('renders the exact missing-secret reason on the errored MCP card, never its healthy sibling', async () => {
    await renderMcpServers([bad, healthy])
    const affected = serverCard(bad.name)
    const good = serverCard(healthy.name)
    expect(within(affected).getByText('error')).toBeVisible()
    expect(within(good).getByText('connected')).toBeVisible()
    expect(within(good).getByText('1 tools')).toBeVisible()
    expect(within(good).queryByText(reason)).not.toBeInTheDocument()
    expect(within(affected).getByText(reason)).toBeVisible()
  })

  it('keeps a healthy MCP card connected with no phantom reference error', async () => {
    await renderMcpServers([healthy])
    const card = serverCard(healthy.name)
    expect(within(card).getByText('connected')).toBeVisible()
    expect(within(card).getByRole('switch', { name: 'Disable Healthy connector' })).toBeChecked()
    expect(within(card).getByText('1 tools')).toBeVisible()
    expect(within(card).queryByText('error')).not.toBeInTheDocument()
    expect(screen.queryByText(reason)).not.toBeInTheDocument()
  })
})
