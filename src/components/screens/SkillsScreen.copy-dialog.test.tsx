// SkillsScreen.copy-dialog.test.tsx — RED tests, landing/skills (T9): the
// S-6.4 screen legs (§17.1 row 21, T-N3). A built-in row offers "Make a
// copy"; the copy dialog takes a name and commits via "Create copy"; the
// built-in row itself has no delete control and shows its read-only badge
// (the last two are the baseline-consistent legs of the scenario).
// Traces to: skills-spec-r13.md S-6.4, §17.1 row 21.
//
// RED expectation: no "Make a copy" control or copy dialog exists on the
// baseline — those assertions fail; the badge/no-delete legs pass on the
// baseline and are never cited as RED evidence.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Skill } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchTools: vi.fn().mockResolvedValue([]),
    copySkill: vi.fn(),
  }
})

import { fetchSkills, copySkill } from '@/lib/api'
import { SkillsScreen } from './SkillsScreen'

const planSkill: Skill = {
  id: 'plan',
  name: 'plan',
  description: 'Plan before acting.',
  source: 'builtin',
  status: 'active',
  verified: true,
  version: '0.0.0',
}

let client: QueryClient

beforeEach(() => {
  vi.mocked(fetchSkills).mockResolvedValue([planSkill])
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function renderSkillsScreen() {
  render(<QueryClientProvider client={client}><SkillsScreen /></QueryClientProvider>)
  // The Installed Skills tab is the default content surface.
  await screen.findByText('plan')
}

describe('SkillsScreen — S-6.4 built-in copy flow', () => {
  it('a built-in row shows its read-only badge (baseline-consistent leg)', async () => {
    await renderSkillsScreen()
    expect(screen.getByText('Built-in')).toBeInTheDocument()
  })

  it('a built-in row has no delete control (baseline-consistent leg)', async () => {
    await renderSkillsScreen()
    expect(screen.queryByRole('button', { name: 'Remove plan' })).not.toBeInTheDocument()
  })

  it('a built-in row offers Make a copy — RED until T9 lands', async () => {
    await renderSkillsScreen()
    expect(screen.getByRole('button', { name: /make a copy/i })).toBeInTheDocument()
  })

  it('the copy dialog commits via Create copy — RED until T9 lands', async () => {
    const user = userEvent.setup()
    await renderSkillsScreen()
    await user.click(screen.getByRole('button', { name: /make a copy/i }))

    const input = await screen.findByLabelText(/copy name/i)
    await user.type(input, 'plan-copy')
    await user.click(screen.getByRole('button', { name: /create copy/i }))

    expect(vi.mocked(copySkill)).toHaveBeenCalledWith('plan', 'plan-copy')
  })
})
