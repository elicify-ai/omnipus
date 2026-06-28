/**
 * MessageInput.slash.test.tsx — O11 slash-command autocomplete.
 *
 * US-4 / FR-008: built-in suggestions now come from fetchCommands (API-driven),
 * not a hardcoded list.  Dynamic /skill <id> entries still come from fetchSkills.
 *
 * Covers:
 *  - Typing /cl shows the API-driven "Start a new conversation" (clear) suggestion.
 *  - Typing /re does NOT show Recall or Remember (excluded from the web surface).
 *  - Trailing space after command text closes the menu.
 *  - ArrowDown clamps at length-1 (no overflow past last item).
 *  - Enter applies the /clear suggestion and for trigger=clear calls startNewSession().
 *  - Skills filtered to status === 'active' only (still from fetchSkills).
 *  - Escape closes the suggestion list.
 *  - Stale-index shrink case (MAJOR 3): ArrowDown then backspace does not crash.
 *  - Agent-delivery command (/skill) is inserted as text, not executed locally.
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
  // The 5 web-surface commands from the API (US-3/AC-1).
  // Inlined inside the factory: vi.mock is hoisted, so top-level constants are
  // not yet initialized when the factory executes.
  const mockCommands = [
    { name: 'clear',  label: '/clear',  description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
    { name: 'help',   label: '/help',   description: 'Show available commands',   delivery: 'client', available_while_streaming: false },
    { name: 'model',  label: '/model',  description: 'Change the chat model',     delivery: 'client', available_while_streaming: false },
    { name: 'skill',  label: '/skill',  description: 'Run an installed skill',    delivery: 'agent',  available_while_streaming: false },
    { name: 'cancel', label: '/cancel', description: 'Cancel the current turn',   delivery: 'client', available_while_streaming: true  },
  ]
  return {
    ...actual,
    transcribeAudio: vi.fn(),
    isApiError: actual.isApiError,
    fetchSkills: vi.fn().mockResolvedValue([
      { id: 'web-research', name: 'Web Research', description: 'Research the web', status: 'active' },
      { id: 'code-review', name: 'Code Review', description: 'Review code', status: 'inactive' },
    ]),
    fetchCommands: vi.fn().mockResolvedValue(mockCommands),
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

describe('MessageInput slash-command autocomplete — API-driven (US-4 / FR-008)', () => {
  it('typing /cl shows the "Start a new conversation" (clear) suggestion from the API', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeInTheDocument()
      // description becomes the human-readable label in the suggestion entry
      expect(screen.getByText('Start a new conversation')).toBeInTheDocument()
    })
  })

  it('typing /re does NOT show Recall or Remember suggestions (excluded from web surface)', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/re' } })
    // Wait for a tick so the debounce/memo settles.
    await act(async () => { await Promise.resolve() })
    // The listbox might still appear (if skills match /re), but Recall and
    // Remember must never be present since they are excluded from the web surface.
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

  it('Enter on /clear suggestion calls startNewSession (client delivery)', async () => {
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/cl' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    // The first item should be /clear from the API. Press Enter immediately.
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
    // Type "/cancel" — shows 1 item matching "cancel".
    fireEvent.change(getTextarea(), { target: { value: '/cancel' } })
    await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument())
    // Press ArrowDown to move index to 0 (only 1 item).
    fireEvent.keyDown(getTextarea(), { key: 'ArrowDown' })
    // Backspace to "/cance" — list might be same.
    fireEvent.change(getTextarea(), { target: { value: '/cance' } })
    // Backspace again to "/c" — list now includes more items; MAJOR 3 fix clamps index.
    fireEvent.change(getTextarea(), { target: { value: '/c' } })
    await waitFor(() => {
      // No crash — the listbox is still rendered and an option is accessible.
      const options = screen.getAllByRole('option')
      expect(options.length).toBeGreaterThan(0)
    })
    // No thrown error by this point means the clamp worked.
  })

  it('US-4/AC-3: selecting the API /skill (agent-delivery) suggestion inserts text, not executed locally', async () => {
    // Type "/skill" exactly — this matches the API built-in /skill suggestion (trigger='skill')
    // and the active skill /skill web-research (trigger='skill web-research').
    // Select the first option (the built-in /skill from the API, trigger='skill').
    renderInput()
    fireEvent.change(getTextarea(), { target: { value: '/skill' } })
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeInTheDocument()
    })
    // Get all options and find the one whose trigger span text is exactly "/skill"
    const options = screen.getAllByRole('option')
    // The first option whose trigger span ends in just "skill" (not "skill web-research")
    const skillOpt = options.find((o) => {
      const triggerSpan = o.querySelector('[aria-hidden="true"]')
      return triggerSpan?.textContent === '/skill'
    })
    expect(skillOpt).toBeDefined()
    fireEvent.click(skillOpt!)
    // The input should now contain "/skill " (inserted as text, not executed locally)
    expect(getTextarea()).toHaveValue('/skill ')
    // startNewSession should NOT have been called (not a client-delivery /clear)
    expect(mockStartNewSession).not.toHaveBeenCalled()
  })
})
