/**
 * SkillBrowser.test.tsx
 *
 * Covers two flows:
 *
 * A. ClawHub search + install-by-slug (v0.1.0):
 *    - a query renders results (display_name + summary)
 *    - clicking Install calls installSkillBySlug(slug) and invalidates ['skills']
 *    - a 502 search error shows the "registry is unavailable" message (no crash)
 *    - an empty result set shows the no-results state
 *
 * B. Local SKILL.md file install (US-E4, #340) — retained as a secondary path:
 *    - selecting a file shows the confirm dialog with the unverified notice
 *    - capabilities from frontmatter are listed
 *    - confirming calls installSkillFromFile
 *    - a non-hash error toasts (not silent); a 409 hash-mismatch shows the dialog
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast }),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    installSkillFromFile: vi.fn(),
    installSkillBySlug: vi.fn(),
    searchSkills: vi.fn(),
  }
})

import {
  installSkillFromFile,
  installSkillBySlug,
  searchSkills,
  ApiError,
} from '@/lib/api'
import type { SkillSearchResult } from '@/lib/api'
import { SkillBrowser } from './SkillBrowser'

const SKILL_MD_WITH_CAPS = `---
name: Test Skill
version: 1.0.0
capabilities:
  - Read files from the workspace
  - Execute shell commands
  - Access the web
---
# Test Skill
This is a test skill.
`

const SKILL_MD_NO_CAPS = `---
name: Simple Skill
version: 0.1.0
---
# Simple Skill
`

const RESULTS: SkillSearchResult[] = [
  {
    slug: 'web-search',
    display_name: 'Web Search',
    summary: 'Search the web and summarize results.',
    version: '1.4.0',
    score: 0.98,
    registry_name: 'clawhub',
    owner_handle: 'acme',
  },
  {
    slug: 'pdf-reader',
    display_name: 'PDF Reader',
    summary: 'Extract text from PDF files.',
    version: '0.2.1',
    owner_handle: 'tools',
  },
]

let queryClient: QueryClient

function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
}

function withClient(ui: ReactElement): ReactElement {
  return <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
}

function renderBrowser(open = true) {
  const onOpenChange = vi.fn()
  render(withClient(<SkillBrowser open={open} onOpenChange={onOpenChange} />))
  return { onOpenChange }
}

/** Helper: create a File object from text content. */
function makeFile(name: string, content: string): File {
  return new File([content], name, { type: 'text/markdown' })
}

beforeEach(() => {
  vi.clearAllMocks()
  queryClient = makeQueryClient()
  vi.mocked(installSkillFromFile).mockResolvedValue(undefined as never)
  vi.mocked(installSkillBySlug).mockResolvedValue({
    id: 'web-search',
    name: 'web-search',
    version: '1.4.0',
    status: 'active',
    verified: false,
  } as never)
  vi.mocked(searchSkills).mockResolvedValue(RESULTS)
})

// ── A. ClawHub search + install-by-slug ──────────────────────────────────────

describe('SkillBrowser — ClawHub search', () => {
  it('shows the empty-query hint before any search', () => {
    renderBrowser()
    expect(screen.getByText(/Type to search the ClawHub registry/i)).toBeInTheDocument()
    expect(vi.mocked(searchSkills)).not.toHaveBeenCalled()
  })

  it('renders results (display_name + summary) for a query', async () => {
    renderBrowser()
    await userEvent.type(screen.getByTestId('skill-search-input'), 'web')
    await waitFor(() => {
      expect(vi.mocked(searchSkills)).toHaveBeenCalledWith('web')
    })
    expect(await screen.findByText('Web Search')).toBeInTheDocument()
    expect(screen.getByText('Search the web and summarize results.')).toBeInTheDocument()
    expect(screen.getByText('PDF Reader')).toBeInTheDocument()
    // version chip + owner handle
    expect(screen.getByText('v1.4.0')).toBeInTheDocument()
    expect(screen.getByText('by acme')).toBeInTheDocument()
  })

  it('clicking Install calls installSkillBySlug(slug) and invalidates ["skills"]', async () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    renderBrowser()
    await userEvent.type(screen.getByTestId('skill-search-input'), 'web')
    await screen.findByText('Web Search')

    await userEvent.click(screen.getByTestId('skill-install-web-search'))

    await waitFor(() => {
      expect(vi.mocked(installSkillBySlug)).toHaveBeenCalledWith('web-search', '1.4.0')
    })
    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['skills'] })
    })
    // success toast + row marked Installed
    expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
    expect(await screen.findByText('Installed')).toBeInTheDocument()
  })

  it('shows the "registry unavailable" message on a 502 search error (no crash)', async () => {
    vi.mocked(searchSkills).mockRejectedValue(
      new ApiError(502, 'Bad gateway — skill registry is unreachable.'),
    )
    renderBrowser()
    await userEvent.type(screen.getByTestId('skill-search-input'), 'web')
    expect(
      await screen.findByText(/Skill registry is unavailable, try again/i),
    ).toBeInTheDocument()
    // search results container still rendered — component did not crash
    expect(screen.getByTestId('skill-search-results')).toBeInTheDocument()
  })

  it('shows the no-results state when the registry returns an empty array', async () => {
    vi.mocked(searchSkills).mockResolvedValue([])
    renderBrowser()
    await userEvent.type(screen.getByTestId('skill-search-input'), 'nope')
    expect(await screen.findByText(/No skills found for/i)).toBeInTheDocument()
  })

  it('toasts a clear message when install fails with 409 (already installed)', async () => {
    vi.mocked(installSkillBySlug).mockRejectedValue(
      new ApiError(409, 'This conflicts with the current state.'),
    )
    renderBrowser()
    await userEvent.type(screen.getByTestId('skill-search-input'), 'web')
    await screen.findByText('Web Search')
    await userEvent.click(screen.getByTestId('skill-install-web-search'))
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({
          variant: 'error',
          message: expect.stringContaining('already installed'),
        }),
      )
    })
  })
})

// ── B. Local SKILL.md file install (US-E4, #340) ─────────────────────────────

describe('SkillBrowser — install confirm flow (US-E4, #340)', () => {
  it('shows the install-from-file button', () => {
    renderBrowser()
    expect(screen.getByText(/Install from file/i)).toBeInTheDocument()
  })

  it('shows the confirm dialog with unverified notice after file selection', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByTestId('skill-install-confirm-dialog')).toBeInTheDocument()
      expect(screen.getByTestId('unverified-notice')).toBeInTheDocument()
    })
  })

  it('shows declared capabilities from SKILL.md frontmatter', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByText('Read files from the workspace')).toBeInTheDocument()
      expect(screen.getByText('Execute shell commands')).toBeInTheDocument()
      expect(screen.getByText('Access the web')).toBeInTheDocument()
    })
  })

  it('shows "no capabilities declared" when frontmatter has none', async () => {
    renderBrowser()
    const file = makeFile('simple.md', SKILL_MD_NO_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByText(/No capabilities declared/i)).toBeInTheDocument()
    })
  })

  it('cancelling the confirm dialog does not call installSkillFromFile', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByText('Cancel'))
    await waitFor(() => {
      expect(vi.mocked(installSkillFromFile)).not.toHaveBeenCalled()
    })
  })

  it('confirming the dialog calls installSkillFromFile', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(vi.mocked(installSkillFromFile)).toHaveBeenCalledWith(SKILL_MD_WITH_CAPS, 'my-skill.md')
    })
  })

  it('shows a success toast after successful file install', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
    })
  })
})

describe('SkillBrowser — file install error handling (US-E4, #340)', () => {
  it('shows a toast for a non-hash install error (not silent)', async () => {
    vi.mocked(installSkillFromFile).mockRejectedValue(new Error('network timeout'))
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({
          variant: 'error',
          message: expect.stringContaining('network timeout'),
        }),
      )
    })
  })

  it('shows the hash-mismatch dialog (not a toast) for a 409 ApiError with body', async () => {
    const hashError = new ApiError(
      409,
      'This conflicts with the current state. Please refresh and try again.',
      { body: '{"expected":"abc123","got":"def456"}' },
    )
    vi.mocked(installSkillFromFile).mockRejectedValue(hashError)
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('skill-hash-mismatch-dialog')).toBeInTheDocument()
      expect(screen.getByText('abc123')).toBeInTheDocument()
      expect(screen.getByText('def456')).toBeInTheDocument()
      expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
    })
  })
})
