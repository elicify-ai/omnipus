// Advanced.toolIterationLimit.test.tsx — #904 RED, test plan row 16.
//
// Create wizard → Advanced: the per-agent limit starts EMPTY (never
// pre-filled 200), its placeholder names the global the server returns
// ("Global limit (N)" from GET /performance → PerformanceSettings
// .max_tool_iterations), falls back to "Global limit" with no number when
// that request fails, and appears for BOTH the native and the external
// (subagent_3p) variants (D14). An untouched field is absent from the
// submitted payload (Scenario "Create without a value rides the global").
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md — User Story 3 AS-2,
// US-4 AS-3, "UI Screens and States" (Create wizard row), "Where the create
// wizard learns the global", FR-004 (no SPA literal), FR-013.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchPerformanceSettings: vi.fn() }
})

vi.mock('@/components/ui/model-selector', () => ({
  ModelSelector: ({ value, onChange, triggerTestId }: { value: string; onChange: (v: string) => void; triggerTestId?: string }) => (
    <input data-testid={triggerTestId ?? 'model-selector'} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}))

import { ApiError, fetchPerformanceSettings } from '@/lib/api'
import { Advanced } from './Advanced'
import { CreateAgentWizard } from '../CreateAgentWizard'

const LABEL = /^Max tool calls per turn/

function perf(global: number) {
  return {
    max_parallel_agents: 4,
    effective_max_parallel_agents: 4,
    max_parallel_agents_configured: true,
    tools_on_demand: true,
    goal_max_rounds: 20,
    max_tool_iterations: global,
    max_tool_iterations_saved_state: 'ok',
  }
}

function withQuery(ui: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {ui}
    </QueryClientProvider>
  )
}

async function openDisclosure() {
  fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))
  return (await screen.findByLabelText(LABEL)) as HTMLInputElement
}

beforeEach(() => {
  vi.mocked(fetchPerformanceSettings).mockReset().mockResolvedValue(perf(350) as never)
})

describe('wizard Advanced — Max tool calls per turn (#904 US-3, D14)', () => {
  it.each(['Main', 'Subagent', 'subagent_3p'] as const)(
    '%s: empty field with placeholder "Global limit (350)" from the server, no literal 200 anywhere',
    async (type) => {
      render(withQuery(<Advanced payload={{} as never} setField={vi.fn()} initialType={type} />))
      const input = await openDisclosure()
      expect(input.value).toBe('')
      await waitFor(() => expect(input).toHaveAttribute('placeholder', 'Global limit (350)'))
      expect(document.body.textContent ?? '').not.toMatch(/\b200\b/)
    },
  )

  it('when GET /performance fails the placeholder is "Global limit" with no number', async () => {
    vi.mocked(fetchPerformanceSettings).mockReset().mockRejectedValue(new ApiError(503, 'bypass'))
    render(withQuery(<Advanced payload={{} as never} setField={vi.fn()} initialType="Main" />))
    const input = await openDisclosure()
    await waitFor(() => expect(input).toHaveAttribute('placeholder', 'Global limit'))
  })

  it('Scenario "Create without a value rides the global": an untouched field is absent from the submitted payload', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(withQuery(<CreateAgentWizard initialType="Main" onSubmit={onSubmit} onClose={vi.fn()} />))
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const payload = onSubmit.mock.calls[0][0] as Record<string, unknown>
    expect('max_tool_iterations' in payload).toBe(false)
  })
})
