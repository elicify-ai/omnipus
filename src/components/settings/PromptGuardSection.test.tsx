import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPromptGuardLevel: vi.fn(),
    updatePromptGuardLevel: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
    selector({ role: 'admin', user: { username: 'testadmin' } }),
  ),
}))

import { fetchPromptGuardLevel, updatePromptGuardLevel } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { PromptGuardSection } from './PromptGuardSection'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PromptGuardSection />
    </QueryClientProvider>
  )
}

const mockAddToast = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: mockAddToast } as never)
  vi.mocked(useAuthStore).mockImplementation(
    ((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
      selector({ role: 'admin', user: { username: 'testadmin' } })) as never,
  )
})

// =====================================================================
// Three radios with canonical values + subtitles
// =====================================================================

describe('PromptGuardSection — radio rendering', () => {
  it('renders three radios', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    renderSection()
    await waitFor(() => expect(screen.getAllByRole('radio')).toHaveLength(3))
  })

  it('renders low level with its subtitle', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    renderSection()
    await waitFor(() => screen.getByText(/minimal sanitization/i))
    expect(screen.getByText(/minimal sanitization/i)).toBeInTheDocument()
  })

  it('renders medium level with its subtitle', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    renderSection()
    await waitFor(() => screen.getByText(/balanced sanitization/i))
    expect(screen.getByText(/balanced sanitization/i)).toBeInTheDocument()
  })

  it('renders high level with its subtitle', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    renderSection()
    await waitFor(() => screen.getByText(/aggressive sanitization/i))
    expect(screen.getByText(/aggressive sanitization/i)).toBeInTheDocument()
  })
})

// =====================================================================
// Autosave fires updatePromptGuardLevel on radio click (no Save button)
// =====================================================================

describe('PromptGuardSection — autosave', () => {
  it('clicking high radio fires updatePromptGuardLevel immediately (no Save button)', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    vi.mocked(updatePromptGuardLevel).mockResolvedValue({
      saved: true,
      requires_restart: false,
      applied_level: 'high',
    })

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    const radios = screen.getAllByRole('radio')
    fireEvent.click(radios[2]) // high is the third

    await waitFor(() => {
      expect(updatePromptGuardLevel).toHaveBeenCalledWith('high')
    })
  })

  it('no Save button is rendered', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })

  it('shows SaveStatus "Saving…" while mutation is in flight', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    vi.mocked(updatePromptGuardLevel).mockImplementation(
      () => new Promise((resolve) => setTimeout(() => resolve({ saved: true, requires_restart: false, applied_level: 'high' }), 50))
    )

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[2])

    await waitFor(() => {
      expect(screen.getByText(/saving/i)).toBeInTheDocument()
    })
  })

  it('shows SaveStatus "Saved" after successful mutation', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'low', requires_restart: false })
    vi.mocked(updatePromptGuardLevel).mockResolvedValue({
      saved: true,
      requires_restart: false,
      applied_level: 'medium',
    })

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[1]) // medium

    await waitFor(() => {
      expect(screen.getByText(/saved/i)).toBeInTheDocument()
    })
  })

  it('no restart badge when response has requires_restart: false', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })
    vi.mocked(updatePromptGuardLevel).mockResolvedValue({
      saved: true,
      requires_restart: false,
      applied_level: 'high',
    })

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[2])

    await waitFor(() => {
      expect(updatePromptGuardLevel).toHaveBeenCalled()
    })
    expect(screen.queryByText(/restart required/i)).not.toBeInTheDocument()
  })

  it('shows error toast on save failure', async () => {
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'low', requires_restart: false })
    vi.mocked(updatePromptGuardLevel).mockRejectedValue(new Error('network error'))

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[1])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' })
      )
    })
  })
})

// =====================================================================
// Non-admin: no Save button
// =====================================================================

describe('PromptGuardSection — non-admin', () => {
  it('does not render a Save button for non-admin', async () => {
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
        selector({ role: 'user', user: { username: 'testuser' } })) as never,
    )
    vi.mocked(fetchPromptGuardLevel).mockResolvedValue({ level: 'medium', requires_restart: false })

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })
})
