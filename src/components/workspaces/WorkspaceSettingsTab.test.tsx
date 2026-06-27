/**
 * WorkspaceSettingsTab — Project Instructions section tests.
 *
 * Covers the 4 behaviours of the instructions textarea:
 *   1. Hydration      — query resolves → textarea shows fetched content
 *   2. Save on edit   — typing triggers updateWorkspaceInstructions via useAutoSave
 *   3. DATA-LOSS GUARD— load error → Retry shown, updateWorkspaceInstructions NEVER called
 *   4. Clear          — editing to empty string still calls updateWorkspaceInstructions("")
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceSettingsTab } from './WorkspaceSettingsTab'
import type { Workspace } from '@/lib/api'

// ── Router stub ──────────────────────────────────────────────────────────────
// WorkspaceSettingsTab calls useNavigate and renders buttons that navigate to
// team tab routes. Stub the router primitives so the component renders in
// isolation without a real RouterProvider.
const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    Link: ({
      children,
      to,
      ...rest
    }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
  }
})

// ── UI store stub ────────────────────────────────────────────────────────────
const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

// ── API mock — partial passthrough, override the two instructions functions ──
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaceInstructions: vi.fn(),
    updateWorkspaceInstructions: vi.fn(),
  }
})

import * as api from '@/lib/api'

// ── Fixtures ─────────────────────────────────────────────────────────────────

const WORKSPACE_ID = 'ws-test-001'

const mockWorkspace: Workspace = {
  id: WORKSPACE_ID,
  name: 'Test Workspace',
  description: 'A test workspace',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  is_default: false,
  repository: '',
  core_team: [],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function renderTab() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceSettingsTab workspace={mockWorkspace} />
    </QueryClientProvider>,
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  mockNavigate.mockClear()
  // Default: resolves successfully so each test that needs an error can override.
  vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: '' })
  vi.mocked(api.updateWorkspaceInstructions).mockResolvedValue({ content: '' })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('WorkspaceSettingsTab — Project Instructions section', () => {
  /**
   * Test 1 — Hydration
   * When fetchWorkspaceInstructions resolves with "hello world" the textarea
   * value must equal "hello world".
   */
  it('hydrates the textarea from fetchWorkspaceInstructions', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'hello world' })

    renderTab()

    // The textarea is initially empty; wait for the query to resolve and the
    // useEffect to apply the fetched content.
    await waitFor(() => {
      const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
      expect(textarea).toHaveValue('hello world')
    })

    expect(api.fetchWorkspaceInstructions).toHaveBeenCalledWith(WORKSPACE_ID)
  })

  /**
   * Test 2 — Save on edit
   * After the data loads, changing the textarea triggers updateWorkspaceInstructions
   * once the useAutoSave debounce (500 ms default) fires.
   */
  it('calls updateWorkspaceInstructions after typing into the textarea', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'initial text' })

    renderTab()

    // Wait for hydration with real timers so the query resolves normally.
    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('initial text')
    })

    // Switch to fake timers NOW — after data is loaded — to control the debounce.
    vi.useFakeTimers()

    const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
    fireEvent.change(textarea, { target: { value: 'updated text' } })

    // Before the debounce fires the PUT must not have been sent.
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()

    // Advance past the 500 ms debounce.
    await act(async () => {
      vi.advanceTimersByTime(600)
    })

    // Switch back so waitFor can poll.
    vi.useRealTimers()

    await waitFor(() => {
      expect(api.updateWorkspaceInstructions).toHaveBeenCalledWith(WORKSPACE_ID, 'updated text')
    })
  })

  /**
   * Test 3 — DATA-LOSS GUARD (the most important assertion)
   *
   * When fetchWorkspaceInstructions REJECTS:
   *   (a) a Retry affordance must render
   *   (b) updateWorkspaceInstructions must NEVER be called
   *
   * This verifies the `{ disabled: instructionsError }` guard in useAutoSave
   * which prevents a failed load from auto-saving an empty string and wiping
   * the user's instructions.
   */
  it('shows Retry and does NOT call updateWorkspaceInstructions when load fails', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockRejectedValue(new Error('Network error'))

    renderTab()

    // Wait for the error state to render.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })

    // The error message should be visible.
    expect(screen.getByText(/could not load project instructions/i)).toBeInTheDocument()

    // Textarea must NOT render when there is a load error.
    expect(
      screen.queryByRole('textbox', { name: /workspace \/ project instructions/i }),
    ).not.toBeInTheDocument()

    // Advance fake timers to confirm no deferred save fires.
    vi.useFakeTimers()
    await act(async () => {
      vi.advanceTimersByTime(2000)
    })
    vi.useRealTimers()

    // The guard must hold: PUT was never called.
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()
  })

  /**
   * Test 4 — Clear (empty string is a valid value)
   * Editing an existing value to "" must still call updateWorkspaceInstructions
   * with an empty string — clearing is intentional.
   */
  it('calls updateWorkspaceInstructions with empty string when textarea is cleared', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'some instructions' })
    vi.mocked(api.updateWorkspaceInstructions).mockResolvedValue({ content: '' })

    renderTab()

    // Wait for hydration.
    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('some instructions')
    })

    // Switch to fake timers after data loaded.
    vi.useFakeTimers()

    const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
    fireEvent.change(textarea, { target: { value: '' } })

    // Advance past the debounce.
    await act(async () => {
      vi.advanceTimersByTime(600)
    })

    vi.useRealTimers()

    await waitFor(() => {
      expect(api.updateWorkspaceInstructions).toHaveBeenCalledWith(WORKSPACE_ID, '')
    })
  })
})
