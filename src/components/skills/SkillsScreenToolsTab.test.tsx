/**
 * SkillsScreenToolsTab.test.tsx — Issue #338 ACs (US-E2).
 *
 * Covers:
 * 1. Built-in Tools tab shows category-level outcomes (no raw system.* IDs).
 * 2. A "Manage permissions → Security" link is visible.
 * 3. No raw system.* tool identifiers appear in the tab.
 * 4. Editing is not available (no buttons/controls for changing tool policy).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

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
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchTools: vi.fn(),
  }
})

import { fetchTools, fetchSkills, type Skill } from '@/lib/api'
import { SkillsScreen } from '@/components/screens/SkillsScreen'

const builtinSkill: Skill = {
  id: 'builtin-1',
  name: 'system-skill',
  version: '0.0.0',
  description: 'A built-in skill',
  author: '',
  source: 'builtin',
  verified: true,
  status: 'active',
}

const communitySkill: Skill = {
  id: 'community-1',
  name: 'community-skill',
  version: '1.2.3',
  description: 'A community skill',
  author: 'Jane Dev',
  source: 'global',
  verified: false,
  status: 'active',
}

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
  vi.mocked(fetchTools).mockResolvedValue([
    { name: 'system.config_get', scope: 'system', category: 'system', description: 'Get config', source: 'builtin' },
    { name: 'web_search', scope: 'general', category: 'web', description: 'Search the web', source: 'builtin' },
    { name: 'file.read', scope: 'general', category: 'fs', description: 'Read a file', source: 'builtin' },
    { name: 'file.write', scope: 'general', category: 'fs', description: 'Write a file', source: 'builtin' },
    { name: 'browser.navigate', scope: 'general', category: 'browser', description: 'Navigate to URL', source: 'builtin' },
  ])
})

describe('SkillsScreen — Built-in Tools tab overview (US-E2, #338)', () => {
  it('shows the Built-in Tools tab', async () => {
    renderScreen()
    await screen.findByText('Skills & Tools')
    expect(screen.getByRole('tab', { name: /Built-in Tools/i })).toBeInTheDocument()
  })

  it('switching to Built-in Tools tab shows category groups, not raw tool IDs', async () => {
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    // Should show category groups
    await waitFor(() => {
      expect(screen.getByTestId('tool-category-web')).toBeInTheDocument()
      expect(screen.getByTestId('tool-category-fs')).toBeInTheDocument()
      expect(screen.getByTestId('tool-category-browser')).toBeInTheDocument()
    })
  })

  it('does not show raw system.* tool identifiers', async () => {
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await waitFor(() => {
      expect(screen.queryByText('system.config_get')).not.toBeInTheDocument()
    })
  })

  it('does not show system-scope category when only system.* tools are in it', async () => {
    // The system-scope tool should be filtered out entirely
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await waitFor(() => {
      // system category group should not appear since system.* tools are excluded
      expect(screen.queryByTestId('tool-category-system')).not.toBeInTheDocument()
    })
  })

  it('shows a "Manage permissions" link to Security', async () => {
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await waitFor(() => {
      expect(screen.getByTestId('manage-permissions-link')).toBeInTheDocument()
    })
  })

  it('the manage-permissions link points to Security settings', async () => {
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await waitFor(() => {
      const link = screen.getByTestId('manage-permissions-link')
      expect(link).toHaveAttribute('href', '/settings?tab=security')
    })
  })

  it('shows tool count badge per category', async () => {
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await waitFor(() => {
      // fs category has 2 tools
      const fsCategory = screen.getByTestId('tool-category-fs')
      expect(fsCategory).toHaveTextContent('2 tools')
    })
  })

  it('no edit controls (buttons/checkboxes for policy) are present in the tab', async () => {
    const user = userEvent.setup()
    renderScreen()
    await screen.findByText('Skills & Tools')
    await user.click(screen.getByRole('tab', { name: /Built-in Tools/i }))
    await waitFor(() => {
      screen.getByTestId('tool-category-web')
    })
    // No checkboxes for tool policy editing
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  })
})

describe('SkillsScreen — Installed Skills source badge & delete gating', () => {
  it('built-in skill shows a "Built-in" badge and NO delete button', async () => {
    vi.mocked(fetchSkills).mockResolvedValueOnce([builtinSkill])
    renderScreen()
    // Default tab is "Installed Skills"; wait for the skill row to render.
    await screen.findByText('system-skill')
    expect(screen.getByText('Built-in')).toBeInTheDocument()
    expect(screen.queryByText('Community')).not.toBeInTheDocument()
    // No delete button for built-in skills.
    expect(
      screen.queryByRole('button', { name: /Remove system-skill/i })
    ).not.toBeInTheDocument()
  })

  it('community skill shows a "Community" badge and a delete button', async () => {
    vi.mocked(fetchSkills).mockResolvedValueOnce([communitySkill])
    renderScreen()
    await screen.findByText('community-skill')
    expect(screen.getByText('Community')).toBeInTheDocument()
    expect(screen.queryByText('Built-in')).not.toBeInTheDocument()
    // Delete button present for community/user-installed skills.
    expect(
      screen.getByRole('button', { name: /Remove community-skill/i })
    ).toBeInTheDocument()
  })

  it('hides the "by" author text when author is empty', async () => {
    vi.mocked(fetchSkills).mockResolvedValueOnce([builtinSkill])
    renderScreen()
    await screen.findByText('system-skill')
    expect(screen.queryByText(/^by /)).not.toBeInTheDocument()
  })

  it('shows the "by {author}" text when author is non-empty', async () => {
    vi.mocked(fetchSkills).mockResolvedValueOnce([communitySkill])
    renderScreen()
    await screen.findByText('community-skill')
    expect(screen.getByText('by Jane Dev')).toBeInTheDocument()
  })

  it('hides the placeholder "0.0.0" version chip', async () => {
    vi.mocked(fetchSkills).mockResolvedValueOnce([builtinSkill])
    renderScreen()
    await screen.findByText('system-skill')
    expect(screen.queryByText('v0.0.0')).not.toBeInTheDocument()
  })

  it('shows a real version chip for community skills', async () => {
    vi.mocked(fetchSkills).mockResolvedValueOnce([communitySkill])
    renderScreen()
    await screen.findByText('community-skill')
    expect(screen.getByText('v1.2.3')).toBeInTheDocument()
  })
})
