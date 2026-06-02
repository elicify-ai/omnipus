import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ScheduleFormSheet } from './ScheduleFormSheet'
import type { Agent } from '@/lib/api'

// #264 US6 AS3 — Create via the Sheet form: fill fields + submit calls
// createSchedule + shows a success toast.

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    createSchedule: vi.fn().mockResolvedValue({ id: 's-new' }),
    updateSchedule: vi.fn().mockResolvedValue({ id: 's1' }),
    isApiError: vi.fn().mockReturnValue(false),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

import { fetchAgents, createSchedule } from '@/lib/api'

const mockAgents = [
  { id: 'mia', name: 'Mia', type: 'core' },
  { id: 'max', name: 'Max', type: 'custom' },
] as unknown as Agent[]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderForm() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ScheduleFormSheet open={true} onOpenChange={() => {}} defaultOwnerAgentId="mia" />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
})

describe('ScheduleFormSheet — create (#264 US6 AS3)', () => {
  it('renders the create form with no table', async () => {
    const { container } = renderForm()
    await screen.findByText('New schedule')
    expect(container.querySelector('table')).toBeNull()
  })

  it('fills fields and submitting calls createSchedule + toasts', async () => {
    renderForm()
    await screen.findByText('New schedule')

    // Name + message are required; owner pre-filled from defaultOwnerAgentId.
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), {
      target: { value: 'My schedule' },
    })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), {
      target: { value: 'Do the thing' },
    })

    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))

    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    expect(body.name).toBe('My schedule')
    expect(body.message).toBe('Do the thing')
    expect(body.owner_agent_id).toBe('mia')
    // default trigger is "every"
    expect(body.trigger.kind).toBe('every')

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success' }),
      ),
    )
  })

  it('disables submit until required fields are filled', async () => {
    renderForm()
    await screen.findByText('New schedule')
    // owner is pre-filled but name + message empty → disabled
    expect(screen.getByRole('button', { name: /create schedule/i })).toBeDisabled()
  })
})
