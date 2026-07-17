/**
 * ProfileSection.test.tsx — FR-108 / US-5 ACs.
 *
 * Covers:
 * - Font-size slider sets --user-font-size on document.documentElement (AC1).
 * - Clamp: slider enforces 12–20px bounds (aria-valuemin/max).
 * - Persists across reload: preference is written to localStorage.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
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
import { fetchUserContext, updateUserContext } from '@/lib/api'

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

// ── Echo-race regression (P2 'Text input Auto save') ────────────────────────
//
// Root cause: the Workspace Context (USER.md) hydration effect
// (`useEffect(() => { if (userContextData) setUserContent(...) }, [userContextData])`)
// had NO dirty guard — a save's `invalidateQueries` refetch landing while the
// operator kept typing would silently overwrite the textarea with the stale
// pre-edit content. Fixed by adding `contextDirtyRef` (set on every onChange,
// cleared only when useAutoSave's `onSaved` reports the save snapshot is
// still current).
describe('ProfileSection — Workspace Context echo-race (autosave hydration must never revert an in-flight draft)', () => {
  it('typing during the save round-trip is never reverted by the invalidate-refetch echo, and the newer text still persists', async () => {
    // Three GETs: the initial hydration, the invalidate-refetch fired by
    // save #1 (server genuinely has 'first edit' at that point — but the
    // operator has ALREADY typed more locally, so this is a stale echo
    // relative to the live draft), and the invalidate-refetch fired by
    // save #2 (server now genuinely has the newest text).
    vi.mocked(fetchUserContext).mockReset()
      .mockResolvedValueOnce({ content: 'initial text' })
      .mockResolvedValueOnce({ content: 'first edit' })
      .mockResolvedValue({ content: 'first edit second edit' })

    // Both PUTs are manually controlled so the test can inspect the exact
    // moment save #1's stale echo lands WHILE save #2 is still queued/in
    // flight — the precise race window this fix closes.
    let resolvePut1!: () => void
    const putPromise1 = new Promise<void>((r) => { resolvePut1 = r })
    let resolvePut2!: () => void
    const putPromise2 = new Promise<void>((r) => { resolvePut2 = r })
    vi.mocked(updateUserContext).mockReset()
      .mockReturnValueOnce(putPromise1)
      .mockReturnValueOnce(putPromise2)

    renderSection()

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace context/i }),
      ).toHaveValue('initial text')
    })

    vi.useFakeTimers()
    const textarea = screen.getByRole('textbox', { name: /workspace context/i })

    // First edit — debounce fires (1500ms, raised from the 500ms default —
    // see ProfileSection.tsx's context useAutoSave call), the PUT goes out
    // and stays in flight.
    fireEvent.change(textarea, { target: { value: 'first edit' } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(updateUserContext).toHaveBeenCalledTimes(1)

    // Second edit — the operator keeps typing while the first PUT is still
    // unresolved. This debounce cycle fires too, but useAutoSave's own
    // serialization (isSavingRef) queues it (rerunPendingRef) instead of
    // dispatching a second, concurrent PUT.
    fireEvent.change(textarea, { target: { value: 'first edit second edit' } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(updateUserContext).toHaveBeenCalledTimes(1)
    expect(textarea).toHaveValue('first edit second edit')

    // Switch to real timers for the async settle/refetch chain below.
    vi.useRealTimers()

    // Resolve PUT #1. The save's own (un-awaited, fire-and-forget)
    // `invalidateQueries` triggers a refetch that resolves with the STALE
    // ("first edit") content — the server echo of save #1's own payload,
    // landing after the operator typed more. Save #2 (the queued rerun)
    // starts right behind it and immediately blocks on the still-unresolved
    // `putPromise2`, giving a clean checkpoint to inspect the draft
    // mid-race.
    await act(async () => {
      resolvePut1()
    })

    await waitFor(() => {
      expect(fetchUserContext).toHaveBeenCalledTimes(2)
    })
    // Save #2 must already be in flight (queued via useAutoSave's own
    // serialization) by the time save #1's echo has landed.
    await waitFor(() => {
      expect(updateUserContext).toHaveBeenCalledTimes(2)
    })

    // THE regression assertion: even though the query cache just updated
    // with the stale "first edit" echo, the textarea must still show the
    // NEWEST typed text — contextDirtyRef stayed armed because
    // useAutoSave's onSaved reported isCurrent=false for save #1 (the live
    // draft had already moved on when it resolved).
    expect(textarea).toHaveValue('first edit second edit')

    // Resolve PUT #2 — the queued re-save persists the newer text, and its
    // own invalidate-refetch (mocked to return the now-genuinely-current
    // value) settles cleanly.
    await act(async () => {
      resolvePut2()
    })

    await waitFor(() => {
      expect(fetchUserContext).toHaveBeenCalledTimes(3)
    })
    expect(updateUserContext).toHaveBeenNthCalledWith(1, 'first edit')
    expect(updateUserContext).toHaveBeenNthCalledWith(2, 'first edit second edit')
    expect(textarea).toHaveValue('first edit second edit')
  })
})
