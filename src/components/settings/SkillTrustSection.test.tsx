import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkillTrust: vi.fn(),
    updateSkillTrust: vi.fn(),
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

import { fetchSkillTrust, updateSkillTrust } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { SkillTrustSection } from './SkillTrustSection'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SkillTrustSection />
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
// Three radios render with canonical values
// =====================================================================

describe('SkillTrustSection — radio rendering', () => {
  it('renders three radios with canonical level values', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })

    renderSection()

    await waitFor(() => {
      const radios = screen.getAllByRole('radio')
      expect(radios).toHaveLength(3)
    })

    const radios = screen.getAllByRole('radio')
    const values = radios.map((r) => r.getAttribute('aria-checked'))
    expect(radios.length).toBe(3)
    expect(values).toContain('true')
  })

  it('renders block_unverified radio', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    renderSection()
    await waitFor(() => screen.getByText(/block skills without a verifiable hash/i))
    expect(screen.getByText(/block skills without a verifiable hash/i)).toBeInTheDocument()
  })

  it('renders warn_unverified radio', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    renderSection()
    await waitFor(() => screen.getByText(/warn but allow/i))
    expect(screen.getByText(/warn but allow/i)).toBeInTheDocument()
  })

  it('renders allow_all radio with its subtitle', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    renderSection()
    await waitFor(() => screen.getByText(/accept any skill/i))
    expect(screen.getByText(/accept any skill/i)).toBeInTheDocument()
  })

  it('does not render any uppercase level values', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    renderSection()
    await waitFor(() => screen.getAllByRole('radio'))
    const content = document.body.textContent ?? ''
    expect(content).not.toMatch(/BLOCK_UNVERIFIED|WARN_UNVERIFIED|ALLOW_ALL/)
  })
})

// =====================================================================
// Selecting allow_all shows warning panel
// =====================================================================

describe('SkillTrustSection — allow_all warning', () => {
  it('shows amber warning panel when allow_all is selected', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    vi.mocked(updateSkillTrust).mockResolvedValue({
      saved: true,
      requires_restart: false,
      applied_level: 'allow_all',
    })
    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))

    const radios = screen.getAllByRole('radio')
    fireEvent.click(radios[2])

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
    expect(screen.getByRole('alert')).toHaveTextContent(/supply-chain/i)
  })

  it('does not show warning panel when warn_unverified is selected', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

// =====================================================================
// Autosave fires updateSkillTrust on radio click (no Save button)
// =====================================================================

describe('SkillTrustSection — autosave', () => {
  it('clicking block_unverified radio fires updateSkillTrust immediately (no Save button)', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    vi.mocked(updateSkillTrust).mockResolvedValue({
      saved: true,
      requires_restart: false,
      applied_level: 'block_unverified',
    })

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))

    const radios = screen.getAllByRole('radio')
    fireEvent.click(radios[0]) // block_unverified

    await waitFor(() => {
      expect(updateSkillTrust).toHaveBeenCalledWith('block_unverified')
    })
  })

  it('no Save button is rendered for admin', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })

  it('shows SaveStatus "Saving…" while mutation is in flight', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    vi.mocked(updateSkillTrust).mockImplementation(
      () => new Promise((resolve) => setTimeout(() => resolve({ saved: true, requires_restart: false, applied_level: 'block_unverified' }), 50))
    )

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[0])

    await waitFor(() => {
      expect(screen.getByText(/saving/i)).toBeInTheDocument()
    })
  })

  it('invalidates pending-restart query (not a local badge) when server returns requires_restart: true', async () => {
    // The "restart required" signal is now wired through the shared pending-restart
    // store (queryClient.invalidateQueries) rather than a component-local badge that
    // would be lost on unmount or Advanced-disclosure collapse. Verify that
    // invalidation is triggered when the response carries requires_restart: true.
    const { QueryClient: QC } = await import('@tanstack/react-query')
    const client = new QC({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries')

    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    vi.mocked(updateSkillTrust).mockResolvedValue({
      saved: true,
      requires_restart: true,
      applied_level: 'block_unverified',
    })

    const { render: testRender } = await import('@testing-library/react')
    const { QueryClientProvider: QCP } = await import('@tanstack/react-query')
    const { SkillTrustSection: STS } = await import('./SkillTrustSection')

    testRender(
      <QCP client={client}>
        <STS />
      </QCP>
    )

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[0])

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: expect.arrayContaining(['pending-restart']) })
      )
    })
  })

  it('shows error toast when mutation fails', async () => {
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })
    vi.mocked(updateSkillTrust).mockRejectedValue(new Error('network error'))

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    fireEvent.click(screen.getAllByRole('radio')[0])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' })
      )
    })
  })
})

// =====================================================================
// Non-admin: no Save button, radios disabled
// =====================================================================

describe('SkillTrustSection — non-admin', () => {
  it('does not render a Save button for non-admin', async () => {
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
        selector({ role: 'user', user: { username: 'testuser' } })) as never,
    )
    vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' })

    renderSection()

    await waitFor(() => screen.getAllByRole('radio'))
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })
})
