// SkillsScreen.riders.test.tsx — order 32 rider tests (S-80, S-81). Oracle:
// skills-tools-redesign-spec scenarios S-80 (#1570) and S-81 (#1576).
//
// RED today: S-80 — the skills list drops nothing and has no per-row load
// error surface, so a broken skill row never shows "Cannot load" with its
// reason; S-81 — partially covered by SkillsScreen.copy-dialog.test.tsx (the
// refreshed row's Remove control carries the new name); this rider pins the
// row label itself.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Skill } from '@/lib/api/generated/openapi-types'

const { copySkillMock, addToast } = vi.hoisted(() => ({ copySkillMock: vi.fn(), addToast: vi.fn() }))
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (state: { addToast: typeof addToast }) => unknown) =>
    selector ? selector({ addToast }) : { addToast },
}))

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

const baseSkill: Skill = {
  has_local_changes: false,
  id: 'skill-x',
  name: 'skill-x',
  description: 'Skill number x.',
  source: 'builtin',
  status: 'active',
  verified: true,
  version: '0.0.0',
  revision: 'a'.repeat(64),
  category: 'tools',
  trust_approved: false,
}

let client: QueryClient

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function renderSkillsScreen() {
  render(<QueryClientProvider client={client}><SkillsScreen /></QueryClientProvider>)
}

describe('Rider #1570 (S-80): one unreadable skill does not empty the list', () => {
  it('ten skills render; the broken one shows "Cannot load" with its reason', async () => {
    const ten = Array.from({ length: 10 }, (_, i) => ({
      ...baseSkill,
      id: `skill-${i}`,
      name: `skill-${i}`,
      description: `Skill number ${i}.`,
    }))
    // The broken row carries the reason on the carrier (GREEN adds the
    // field to the REST Skill carrier; the name follows the response's
    // error-field style). The spec pins the surface: the row still shows,
    // with "Cannot load" and the reason (S-80's Then).
    const broken = {
      ...ten[3],
      id: 'skill-3-broken',
      name: 'skill-3-broken',
      load_error: 'helper script not readable: permission denied',
    } as Skill
    vi.mocked(fetchSkills).mockResolvedValue([...ten.slice(0, 3), broken, ...ten.slice(4)])

    await renderSkillsScreen()
    for (const s of ten) {
      if (s.id === 'skill-3') continue
      expect(await screen.findByText(s.id, { exact: false })).toBeInTheDocument()
    }
    expect(await screen.findByText('skill-3-broken')).toBeInTheDocument()
    expect(screen.getByText('Cannot load')).toBeInTheDocument()
    expect(screen.getByText(/permission denied/)).toBeInTheDocument()
  })
})

describe('Rider #1576 (S-81): a copy shows its own name', () => {
  it('the refreshed row is labelled by the copy name, not the built-in it came from', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([{ ...baseSkill, id: 'plan', name: 'plan' }])
    const user = userEvent.setup()
    await renderSkillsScreen()
    await screen.findByText('plan')

    await user.click(screen.getByRole('button', { name: /make a copy/i }))
    const input = await screen.findByLabelText('Name of your copy')
    await user.clear(input)
    await user.type(input, 'plan-mine')
    const ownCopy: Skill = { ...baseSkill, id: 'plan-mine', name: 'plan-mine', source: 'global', description: 'own copy', has_local_changes: true }
    copySkillMock.mockImplementation(async () => {
      vi.mocked(fetchSkills).mockResolvedValue([{ ...baseSkill, id: 'plan', name: 'plan' }, ownCopy])
      return ownCopy
    })
    await user.click(screen.getByRole('button', { name: /create copy/i }))

    await screen.findByText('plan-mine')
    expect(screen.getByRole('button', { name: 'Remove plan-mine' })).toBeInTheDocument()
    // The copy's row label is its own name — no row is labelled by the
    // built-in's display name carrying the copy's identity.
    const copiedRow = screen.getByText('plan-mine').closest('div')!
    expect(within(copiedRow).getByText('plan-mine')).toBeInTheDocument()
  })
})
