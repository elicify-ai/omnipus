// r15 S-6.4, §17.1 row 21; CHECK-10/11: exact badge, dialog labels,
// default and note; replace the prefilled input; exact copy request and
// refreshed own-copy row. Only the no-delete leg is baseline-consistent.
// The component/API edge is mocked here; real adapters have separate tests.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Skill } from '@/lib/api/generated/openapi-types'

const { copySkillMock } = vi.hoisted(() => ({ copySkillMock: vi.fn() }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchTools: vi.fn().mockResolvedValue([]),
    copySkill: copySkillMock,
  }
})

import { fetchSkills } from '@/lib/api'
import { SkillsScreen } from './SkillsScreen'

const planSkill: Skill = {
  id: 'plan',
  name: 'plan',
  description: 'Plan before acting.',
  source: 'builtin',
  status: 'active',
  verified: true,
  version: '0.0.0',
  revision: 'a'.repeat(64), // REST Skill carrier requires revision, including built-ins.
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
  it('a built-in row shows the complete r15 read-only badge', async () => {
    await renderSkillsScreen()
    expect(screen.getByText('Built-in · read-only')).toBeInTheDocument()
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

    const input = await screen.findByLabelText('Name of your copy')
    expect(input).toHaveValue('plan-copy')
    expect(screen.getByText('Your copy will not update with Omnipus. Grant it to agents in their settings.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'my-plan')
    const ownCopy: Skill = { ...planSkill, id: 'my-plan', name: 'my-plan', source: 'global', description: 'own copy' }
    copySkillMock.mockImplementation(async () => {
      vi.mocked(fetchSkills).mockResolvedValue([planSkill, ownCopy])
      return ownCopy
    })
    await user.click(screen.getByRole('button', { name: /create copy/i }))

    expect(copySkillMock).toHaveBeenCalledExactlyOnceWith('plan', 'my-plan')
    await screen.findByText('my-plan')
    await waitFor(() => expect(vi.mocked(fetchSkills).mock.calls.length).toBeGreaterThan(1))
    expect(screen.getByRole('button', { name: 'Remove my-plan' })).toBeInTheDocument()
  })
})
