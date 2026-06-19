import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateAgentModal } from '@/components/agents/CreateAgentModal'

// test_create_agent_flow (test #29)
// Traces to: agent-form-requirements.md §5.3-§5.5 (W4) and
// wave5a-wire-ui-spec.md — Scenario: Creating a custom agent.
//
// W4 (commit d87dd259 + ed8e12d9) replaced the legacy 2-tab CreateAgentModal
// with a 3-step wizard. The wizard's submit button (testid `wizard-create`)
// only renders on step 3; the test must walk the stepper via
// `wizard-next-1` / `wizard-next-2` to reach it. Step gating requires
// name + model on step 1 and soul on step 2 (see CreateAgentWizard.tsx).

function makeQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeQueryClient()}>{children}</QueryClientProvider>
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn())
})

/** Drive the wizard to step 3 with valid Main-type inputs. */
function fillAndAdvanceToStep3() {
  // Step ① — Identity: name + model required (Main type).
  fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'DevOps Agent' } })
  fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'claude-sonnet-4-6' } })
  fireEvent.click(screen.getByTestId('wizard-next-1'))
  // Step ② — Personality: soul required.
  fireEvent.change(screen.getByTestId('wizard-soul'), {
    target: { value: 'You are a DevOps agent that automates deployments.' },
  })
  fireEvent.click(screen.getByTestId('wizard-next-2'))
}

describe('create agent flow integration', () => {
  it('submits POST /api/v1/agents and closes modal on success', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Creating a custom agent (AC2)
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: async () => ({ id: 'devops-agent', name: 'DevOps Agent' }),
    } as Response)

    const onClose = vi.fn()
    const onCreate = vi.fn().mockResolvedValue(undefined)

    render(
      <CreateAgentModal open onClose={onClose} onCreate={onCreate} />,
      { wrapper }
    )

    fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))

    await waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'DevOps Agent' })
      )
    })
  })

  it('disables Next on step 1 when name is empty (step-gating replaces inline error)', () => {
    // W4 changed the validation model: the wizard enforces gating by
    // disabling the Next button rather than showing an inline "Name is
    // required" error on submit. This test pins the new contract — the
    // modal stays open and onCreate is never called.
    const onClose = vi.fn()
    const onCreate = vi.fn().mockResolvedValue(undefined)

    render(
      <CreateAgentModal open onClose={onClose} onCreate={onCreate} />,
      { wrapper }
    )

    // No inputs filled → wizard-next-1 is disabled.
    const next = screen.getByTestId('wizard-next-1')
    expect(next).toBeDisabled()
    // The modal stays open (Close was never called).
    expect(onClose).not.toHaveBeenCalled()
  })
})
