// r15 S-6.4, §17.1 row 21; CHECK-10/11: exact badge, dialog labels,
// default and note; replace the prefilled input; exact copy request and
// refreshed own-copy row. Round 4 adds §13 case-insensitive name validation.
// Expectations are literal spec text, never imported production constants.
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

  it('a built-in row offers Make a copy', async () => {
    await renderSkillsScreen()
    expect(screen.getByRole('button', { name: /make a copy/i })).toBeInTheDocument()
  })

  it('the copy dialog commits via Create copy', async () => {
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

  // r15 §5.13/§13: validation is live, blocks submission and recovers when the
  // name becomes valid. A constant message/null cannot satisfy both states.
  const invalidMessage = 'Use letters, digits and single hyphens, up to 64 characters'
  const takenMessage = 'This name is already used'
  const builtinMessage = "This is a built-in skill's name"
  const nameCases = [
    ['bad_name', invalidMessage],
    ['BAD_NAME', invalidMessage],
    ['Bad_Name', invalidMessage],
    ['two--hyphens', invalidMessage],
    ['-leading', invalidMessage],
    ['trailing-', invalidMessage],
    ['a'.repeat(65), invalidMessage], // §5.13: maximum + 1.
    ['my-plan', takenMessage],
    ['MY-PLAN', takenMessage],
    ['mY-pLaN', takenMessage],
    ['plan', builtinMessage],
    ['PLAN', builtinMessage],
    ['PlAn', builtinMessage],
  ] as const

  it.each(nameCases)('copy name %s shows the exact refusal and recovers', async (name, message) => {
    const user = userEvent.setup()
    const existing: Skill = { ...planSkill, id: 'My-Plan', name: 'My-Plan', source: 'global', verified: false }
    vi.mocked(fetchSkills).mockResolvedValue([planSkill, existing])
    await renderSkillsScreen()
    await user.click(screen.getByRole('button', { name: 'Make a copy of plan', exact: true }))
    const input = await screen.findByLabelText('Name of your copy')
    const submit = screen.getByRole('button', { name: 'Create copy', exact: true })
    expect(submit).toBeEnabled()
    await user.clear(input)
    await user.type(input, name)
    expect(input).toHaveValue(name)
    expect(screen.getByText(message, { exact: true })).toBeInTheDocument()
    expect(submit).toBeDisabled()
    await user.click(submit)
    expect(copySkillMock).not.toHaveBeenCalled()

    await user.clear(input)
    await user.type(input, 'NEW-Copy9')
    expect(input).toHaveValue('NEW-Copy9')
    for (const error of [invalidMessage, takenMessage, builtinMessage]) {
      expect(screen.queryByText(error, { exact: true })).not.toBeInTheDocument()
    }
    expect(submit).toBeEnabled()
    expect(copySkillMock).not.toHaveBeenCalled()
  })

  it.each([1, 2, 63, 64])('accepts a valid %i-character name without a constant validation error', async (length) => {
    const user = userEvent.setup()
    await renderSkillsScreen()
    await user.click(screen.getByRole('button', { name: 'Make a copy of plan', exact: true }))
    const input = await screen.findByLabelText('Name of your copy')
    await user.clear(input)
    await user.type(input, 'A'.repeat(length)) // §5.13: min, min+1, max-1, max.
    expect(input).toHaveValue('A'.repeat(length))
    expect(screen.getByRole('button', { name: 'Create copy', exact: true })).toBeEnabled()
    for (const error of [invalidMessage, takenMessage, builtinMessage]) {
      expect(screen.queryByText(error, { exact: true })).not.toBeInTheDocument()
    }
  })
})
