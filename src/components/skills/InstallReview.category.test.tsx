// InstallReview.category.test.tsx — order 28 (S-55, S-56, S-27; Dataset D5;
// C-13). The install review's category contract, driven through the
// imported component (CHECK round 1 finding 2 / H2: the earlier file held
// only a file-existence guard that an empty placeholder passed):
//   S-56: a SKILL.md with metadata.category opens the review with that
//         category preselected (fixture category `research` → "Research");
//   S-55: a skill with no category requires one before approve — pressing
//         "Install skill" shows "Choose a category." and installs nothing;
//   S-27: trust Deny + an unverified skill disables "Install skill" with
//         "Unverified skills are set to Deny above. Choose Ask or Allow to
//         install this skill." (the screen half; the tool/API halves are
//         orders 6/17).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'

// The pack's BLOCKED seam (support.tsx::requireModule convention): the
// missing module is a named, loud failure — the acceptance oracles are the
// interaction tests below.
const reviewModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/InstallReview.tsx'])
async function requireInstallReview(): Promise<ComponentType<{ review: unknown }>> {
  expect(
    Object.keys(reviewModules),
    'BLOCKED: InstallReview is not implemented — required by skills-tools-redesign-spec C-13, S-55, S-56, S-27 and Dataset D5',
  ).toContain('/src/components/skills/InstallReview.tsx')
  const mod = (await reviewModules['/src/components/skills/InstallReview.tsx']()) as Record<string, unknown>
  expect(typeof mod.InstallReview, 'BLOCKED: InstallReview public export').toBe('function')
  return mod.InstallReview as ComponentType<{ review: unknown }>
}

let InstallReview: ComponentType<{ review: unknown }>

beforeEach(async () => {
  InstallReview = await requireInstallReview()
})

// not-wire-format: test-local projection of the review the Skills screen
// opens (C-13), cast until the public carrier lands.
interface ReviewFixture {
  reviewId: string
  skillName: string
  version: string
  category: string | null
  verified: boolean
  trust: 'allow_all' | 'warn_unverified' | 'block_unverified'
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, installSkillBySlug: vi.fn(), installSkillFromFile: vi.fn() }
})

function wrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      {children}
    </QueryClientProvider>
  )
}

const base: ReviewFixture = {
  reviewId: 'rev-001',
  skillName: 'weather',
  version: '1.0.0',
  category: null,
  verified: true,
  trust: 'warn_unverified',
}

describe('InstallReview — category (order 28)', () => {
  it('S-56: the category from the skill file is preselected', () => {
    render(<InstallReview review={{ ...base, category: 'research' } as never} />, { wrapper })
    // The Category control shows the file's category, human-readable.
    expect(screen.getByText('Research')).toBeInTheDocument()
  })

  it('S-55: without a category, pressing Install skill shows "Choose a category." and installs nothing', async () => {
    const user = userEvent.setup()
    render(<InstallReview review={base as never} />, { wrapper })

    await user.click(screen.getByRole('button', { name: /install skill/i }))
    expect(screen.getByText('Choose a category.')).toBeInTheDocument()
    // Nothing installed: the review stays answerable and no success text
    // appears (the install action never ran).
    expect(screen.queryByText(/^Installed /i)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /install skill/i })).toBeEnabled()
  })

  it('S-27: trust Deny disables Install skill for an unverified skill, with the trust reason', () => {
    render(
      <InstallReview review={{ ...base, verified: false, trust: 'block_unverified' } as never} />,
      { wrapper },
    )
    const install = screen.getByRole('button', { name: /install skill/i })
    expect(install).toBeDisabled()
    expect(install).toHaveAccessibleDescription(
      'Unverified skills are set to Deny above. Choose Ask or Allow to install this skill.',
    )
  })
})
