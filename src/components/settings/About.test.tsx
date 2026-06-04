/**
 * About.test.tsx — FR-109 / US-6 ACs.
 *
 * Covers:
 * - AC1: real octopus logo (img element, not placeholder "O") renders.
 * - AC2: GitHub link points to https://github.com/elicify-ai/omnipus.
 * - AC3: version shows the build version, not literally "dev".
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks ─────────────────────────────────────────────────────────────────────

// SVG imports return a string path in vitest (configured via vite)
vi.mock('@/assets/logo/omnipus-logo.svg', () => ({
  default: '/test-logo.svg',
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAboutInfo: vi.fn(),
    isApiError: actual.isApiError,
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

import { fetchAboutInfo } from '@/lib/api'
import { AboutSection } from './AboutSection'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeAboutInfo(version = '0.1.0-rc1') {
  return {
    version,
    go_version: 'go1.22.0',
    os: 'linux',
    arch: 'amd64',
    uptime_seconds: 3600,
  }
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AboutSection />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ── AC1: real logo renders ────────────────────────────────────────────────────

describe('AboutSection — FR-109 AC1: real logo renders', () => {
  it('renders an img element for the Omnipus logo (not a placeholder "O")', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo())
    renderSection()

    // img with the logo testId must be present
    await waitFor(() => {
      const logo = screen.getByTestId('omnipus-logo')
      expect(logo.tagName).toBe('IMG')
    })
  })

  it('does not render the placeholder "O" text in a div', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo())
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('omnipus-logo')).toBeInTheDocument()
    })
    // The single capital letter "O" placeholder div must not exist
    // (The "Omnipus" heading is fine — we target the logo area specifically)
    expect(screen.queryByText(/^O$/)).not.toBeInTheDocument()
  })

  it('logo img has an accessible alt attribute', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo())
    renderSection()
    await waitFor(() => {
      const logo = screen.getByTestId('omnipus-logo')
      expect(logo).toHaveAttribute('alt')
      expect(logo.getAttribute('alt')).not.toBe('')
    })
  })
})

// ── AC2: GitHub link correct ──────────────────────────────────────────────────

describe('AboutSection — FR-109 AC2: GitHub URL is elicify-ai', () => {
  it('GitHub link points to https://github.com/elicify-ai/omnipus', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo())
    renderSection()
    await waitFor(() => {
      const link = screen.getByRole('link', { name: /view on github/i })
      expect(link).toHaveAttribute('href', 'https://github.com/elicify-ai/omnipus')
    })
  })

  it('GitHub link does NOT point to the old omnipus-ai org', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo())
    renderSection()
    await waitFor(() => {
      const link = screen.getByRole('link', { name: /view on github/i })
      expect(link.getAttribute('href')).not.toContain('omnipus-ai/omnipus')
    })
  })

  it('GitHub link opens in a new tab (target=_blank)', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo())
    renderSection()
    await waitFor(() => {
      const link = screen.getByRole('link', { name: /view on github/i })
      expect(link).toHaveAttribute('target', '_blank')
    })
  })
})

// ── AC3: version not literally "dev" ──────────────────────────────────────────

describe('AboutSection — FR-109 AC3: version not "dev"', () => {
  it('shows the real build version when the API returns a semver', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo('0.1.0-rc1'))
    renderSection()
    await waitFor(() => {
      const versionEl = screen.getByTestId('build-version')
      expect(versionEl.textContent).toBe('0.1.0-rc1')
    })
  })

  it('does NOT show the literal string "dev" when API returns "dev"', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo('dev'))
    renderSection()
    await waitFor(() => {
      const versionEl = screen.getByTestId('build-version')
      expect(versionEl.textContent).not.toBe('dev')
    })
  })

  it('shows a meaningful fallback when version is "dev"', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo('dev'))
    renderSection()
    await waitFor(() => {
      const versionEl = screen.getByTestId('build-version')
      // Should be a non-empty human-readable string
      expect(versionEl.textContent?.trim().length).toBeGreaterThan(0)
      // Specifically we use "development build" as the fallback
      expect(versionEl.textContent).toBe('development build')
    })
  })

  it('shows version from any non-dev semver value', async () => {
    vi.mocked(fetchAboutInfo).mockResolvedValue(makeAboutInfo('1.2.3'))
    renderSection()
    await waitFor(() => {
      const versionEl = screen.getByTestId('build-version')
      expect(versionEl.textContent).toBe('1.2.3')
    })
  })
})

// ── Error state ───────────────────────────────────────────────────────────────

describe('AboutSection — error state', () => {
  it('shows error message when API fails (after retries)', async () => {
    vi.mocked(fetchAboutInfo).mockRejectedValue(new Error('Network error'))
    // Use a client with no retries so the error appears immediately
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AboutSection />
      </QueryClientProvider>
    )
    await waitFor(() => {
      expect(screen.getByText(/could not fetch system info/i)).toBeInTheDocument()
    }, { timeout: 3000 })
  })
})
