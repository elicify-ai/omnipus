/**
 * MessageInput.slash.test.tsx — O11 slash-command autocomplete.
 *
 * Covers:
 *  - Typing /cl shows "Clear session" suggestion (only active skills + /clear survive).
 *  - Typing /re does NOT show Recall or Remember (removed — no real dispatch).
 *  - Trailing space after command text closes the menu.
 *  - ArrowDown clamps at length-1 (no overflow past last item).
 *  - Enter applies the highlighted suggestion and for /clear calls startNewSession().
 *  - Skills filtered to status === 'active' only.
 *  - Escape closes the suggestion list.
 *  - Stale-index shrink case (MAJOR 3): ArrowDown then backspace does not crash.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks ─────────────────────────────────────────────────────────────────────

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({ useUiStore: vi.fn(() => ({ addToast })) }))

const mockStartNewSession = vi.fn()
vi.mock('@/store/session', () => ({
  useSessionStore: (selector: (s: { startNewSession: () => void }) => unknown) =>
    selector({ startNewSession: mockStartNewSession }),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    transcribeAudio: vi.fn(),
    isApiError: actual.isApiError,
    fetchSkills: vi.fn().mockResolvedValue([
      { id: 'web-research', name: 'Web Research', description: 'Research the web', status: 'active' },
      { id: 'code-review', name: 'Code Review', description: 'Review code', status: 'inactive' },
    ]),
  }
})

vi.mock('@/store/connection', () => ({
  useConnectionStore: () => ({ isConnected: true }),
}))

vi.mock('@/store/chat', () => ({
  useChatStore: () => ({ sendMessage: vi.fn(), cancelStream: vi.fn(), isStreaming: false, cancelStage: null }),
}))

import { MessageInput } from './MessageInput'

function renderInput() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MessageInput />
    </QueryClientProvider>,
  )
}

function getTextarea() {
  return screen.getByLabelText('Message input')
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('MessageInput slash-command autocomplete', () => {
  it('typing /cl shows "Clear session" suggestion', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeInTheDocument()
      expect(screen.getByText('Clear session')).toBeInTheDocument()
    })
  })

  it('typing /re does NOT show Recall or Remember suggestions', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/re' } })
    // Wait for a tick so the debounce/memo settles.
    await act(async () => { await Promise.resolve() })
    // The listbox might still appear (if skills match /re), but Recall and
    // Remember must never be present since they are removed from built-ins.
    expect(screen.queryByText('Recall')).not.toBeInTheDocument()
    expect(screen.queryByText('Remember')).not.toBeInTheDocument()
  })

  it('trailing space after command text closes the menu', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/clear' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    // Add a trailing space — the slash query now contains a space and the menu
    // should close.
    fireEvent.change(getTextarea(), { target: { value: '/clear ' } })
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument())
  })

  it('ArrowDown clamps at length-1 (does not overflow past last item)', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    const textarea = getTextarea()
    // Press ArrowDown many times — more than the list length.
    for (let i = 0; i < 10; i++) {
      fireEvent.keyDown(textarea, { key: 'ArrowDown' })
    }
    // No crash, and the active item is still in the DOM (last item).
    const options = screen.getAllByRole('option')
    const selected = options.find((o) => o.getAttribute('aria-selected') === 'true')
    expect(selected).toBeDefined()
    // The selected index is clamped to the last item (not beyond the list).
    expect(options.indexOf(selected!)).toBe(options.length - 1)
  })

  it('Enter applies the /clear suggestion and calls startNewSession', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    // ArrowDown to highlight "Clear session" (it's the first built-in and
    // first item in the filtered list for "/cl").
    fireEvent.keyDown(getTextarea(), { key: 'ArrowDown' })
    // The first item is already selected (index 0) — pressing Enter applies it.
    // Actually it starts at 0, so pressing Enter immediately works.
    fireEvent.change(getTextarea(), { target: { value: '/clear' } })
    await waitFor(() => expect(screen.getByText('Clear session')).toBeInTheDocument())
    fireEvent.keyDown(getTextarea(), { key: 'Enter' })
    await waitFor(() => {
      expect(mockStartNewSession).toHaveBeenCalledTimes(1)
    })
    // Composer is cleared.
    expect(getTextarea()).toHaveValue('')
  })

  it('active skills appear in the suggestion list; inactive skills do not', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/web' } })
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeInTheDocument()
      expect(screen.getByText('Web Research')).toBeInTheDocument()
    })
    // "Code Review" is inactive — must not appear.
    expect(screen.queryByText('Code Review')).not.toBeInTheDocument()
  })

  it('Escape closes the suggestion list', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    fireEvent.keyDown(getTextarea(), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument())
  })

  it('stale-index shrink case: typing then backspacing does not crash', async () => {
    renderInput()
    // Type "/clear" — shows 1 built-in item.
    fireEvent.change(getTextarea(), { target: { value: '/clear' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    // Press ArrowDown to move index to 0 (only 1 item).
    fireEvent.keyDown(getTextarea(), { key: 'ArrowDown' })
    // Backspace to "/clea" — list might now be shorter or the same.
    fireEvent.change(getTextarea(), { target: { value: '/clea' } })
    // Backspace again to "/cl" — list may differ; MAJOR 3 fix clamps the index.
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => {
      // No crash — the listbox is still rendered and an option is accessible.
      const options = screen.getAllByRole('option')
      expect(options.length).toBeGreaterThan(0)
    })
    // No thrown error by this point means the clamp worked.
  })
})
