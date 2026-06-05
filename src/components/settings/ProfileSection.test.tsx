/**
 * ProfileSection.test.tsx — FR-108 / US-5 ACs.
 *
 * Covers:
 * - Font-size slider sets --user-font-size on document.documentElement (AC1).
 * - Clamp: slider enforces 12–20px bounds (aria-valuemin/max).
 * - Persists across reload: preference is written to localStorage.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── ResizeObserver polyfill (jsdom lacks it; Radix Slider needs it) ─────────

global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchUserContext: vi.fn().mockResolvedValue({ content: '' }),
    updateUserContext: vi.fn().mockResolvedValue(undefined),
    changePassword: vi.fn().mockResolvedValue({ success: true }),
    isApiError: actual.isApiError,
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

import { ProfileSection } from './ProfileSection'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ProfileSection />
    </QueryClientProvider>
  )
}

// ── Setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  // Clear localStorage before each test so initial state is clean
  localStorage.clear()
  // Reset the CSS variable
  document.documentElement.style.removeProperty('--user-font-size')
  vi.clearAllMocks()
})

afterEach(() => {
  document.documentElement.style.removeProperty('--user-font-size')
})

// ── Font-size tests ───────────────────────────────────────────────────────────

describe('ProfileSection — FR-108: font-size drives --user-font-size CSS var', () => {
  it('sets --user-font-size on mount to the default (14px)', async () => {
    renderSection()
    await waitFor(() => {
      // Default font size is 14
      const val = document.documentElement.style.getPropertyValue('--user-font-size')
      expect(val).toBe('14px')
    })
  })

  it('sets --user-font-size when localStorage has a saved value', async () => {
    localStorage.setItem('omnipus_pref_font_size', '18')
    renderSection()
    await waitFor(() => {
      const val = document.documentElement.style.getPropertyValue('--user-font-size')
      expect(val).toBe('18px')
    })
  })

  it('shows current font size label in the UI', async () => {
    localStorage.setItem('omnipus_pref_font_size', '16')
    renderSection()
    await waitFor(() => {
      // The displayed label shows current size
      expect(screen.getByText('16px')).toBeInTheDocument()
    })
  })

  it('persists font-size preference to localStorage on mount', async () => {
    renderSection()
    // After initial render with default 14, auto-save kicks in and writes to localStorage
    await waitFor(() => {
      const saved = localStorage.getItem('omnipus_pref_font_size')
      expect(saved).not.toBeNull()
    })
  })

  it('slider min is 12 and max is 20 (bounds enforcement)', async () => {
    renderSection()
    await waitFor(() => {
      const slider = screen.getByRole('slider')
      expect(slider).toHaveAttribute('aria-valuemin', '12')
      expect(slider).toHaveAttribute('aria-valuemax', '20')
    })
  })

  it('shows 12px bound label and 20px bound label', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByText('12px')).toBeInTheDocument()
      expect(screen.getByText('20px')).toBeInTheDocument()
    })
  })
})

describe('ProfileSection — font-size persistence across reload', () => {
  it('reads saved preference from localStorage on mount (18px)', async () => {
    localStorage.setItem('omnipus_pref_font_size', '18')
    renderSection()
    await waitFor(() => {
      // Should display 18px in the label
      expect(screen.getByText('18px')).toBeInTheDocument()
      // CSS var should be set to 18px
      const val = document.documentElement.style.getPropertyValue('--user-font-size')
      expect(val).toBe('18px')
    })
  })

  it('reads saved preference from localStorage on mount (max bound 20)', async () => {
    localStorage.setItem('omnipus_pref_font_size', '20')
    renderSection()
    await waitFor(() => {
      // At the max boundary, both the active-size label and the boundary label show "20px"
      expect(screen.getAllByText('20px').length).toBeGreaterThanOrEqual(1)
      const val = document.documentElement.style.getPropertyValue('--user-font-size')
      expect(val).toBe('20px')
    })
  })

  it('reads saved preference from localStorage on mount (min bound 12)', async () => {
    localStorage.setItem('omnipus_pref_font_size', '12')
    renderSection()
    await waitFor(() => {
      // At the min boundary, both the active-size label and the boundary label show "12px"
      expect(screen.getAllByText('12px').length).toBeGreaterThanOrEqual(1)
      const val = document.documentElement.style.getPropertyValue('--user-font-size')
      expect(val).toBe('12px')
    })
  })
})

describe('ProfileSection — appearance section renders', () => {
  it('renders the Appearance section heading', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/appearance/i)).toBeInTheDocument()
    })
  })

  it('renders the font size label', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/font size/i)).toBeInTheDocument()
    })
  })

  it('renders the slider element', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByRole('slider')).toBeInTheDocument()
    })
  })
})

describe('ProfileSection — CSS variable is set on every re-render', () => {
  it('CSS var is set immediately on mount without any user interaction', async () => {
    renderSection()
    // The useEffect runs synchronously in the test environment
    await act(async () => {})
    const val = document.documentElement.style.getPropertyValue('--user-font-size')
    // Could be 14px (default) or whatever was loaded from localStorage
    expect(val).toMatch(/^\d+px$/)
  })
})
