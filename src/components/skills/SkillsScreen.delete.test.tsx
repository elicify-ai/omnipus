import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (opts: { component: React.ComponentType }) => opts,
  useNavigate: () => vi.fn(),
  useParams: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchTools: vi.fn().mockResolvedValue([]),
    deleteSkill: vi.fn(),
  }
})

import { fetchSkills, deleteSkill, type Skill } from '@/lib/api'
import { SkillsScreen } from '@/components/screens/SkillsScreen'

const skill: Skill = {
  revision: '3'.repeat(64),
  id: 'web-research',
  name: 'Web Research',
  version: '1.0.0',
  description: 'Search the web',
  author: 'community',
  source: 'global',
  status: 'active',
  verified: false,
}

function renderScreen() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <SkillsScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([skill])
  vi.mocked(deleteSkill).mockReset().mockResolvedValue({
    revision: skill.revision,
    persistence_status: 'complete',
    activation_status: 'active',
    changed_fields: ['installed'],
  })
})

describe('SkillsScreen — remove by skill id', () => {
  it('deletes using the skill id, not the display name', async () => {
    renderScreen()
    await screen.findByText('Web Research')
    fireEvent.click(screen.getByLabelText('Remove Web Research'))
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    await waitFor(() => {
      expect(deleteSkill).toHaveBeenCalledWith('web-research', skill.revision)
    })
    expect(deleteSkill).not.toHaveBeenCalledWith('Web Research', expect.anything())
  })
})
