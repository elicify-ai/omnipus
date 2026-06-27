// CreateAgentWizard — focused unit tests for the 3-step wizard itself.
//
// These tests cover the wizard's internal behaviour independent of the
// CreateAgentModal wrapper: step navigation, the submit error inline
// alert, the type / CLI chip, the stepper a11y semantics, and the
// prop-driven onClose / onSubmit contracts.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { CreateAgentWizard } from './CreateAgentWizard'
import { useUiStore } from '@/store/ui'

// The real ModelSelector is now a CONSTRAINED dropdown (UAT model-catalog fix):
// with no providers it renders a disabled "no models" state (no <input>). These
// wizard tests exercise STEP GATING, not picker mechanics, so we stub the
// selector with a plain forwarding <input> keyed by the trigger test id. The
// selector's own behaviour is covered by model-selector.test.tsx, and the
// inherit-toggle rendering is driven by Step1Identity (not the selector), so
// this stub does not weaken the new 4a coverage.
vi.mock('@/components/ui/model-selector', () => ({
  ModelSelector: ({
    value,
    onChange,
    triggerTestId,
  }: {
    value: string
    onChange: (v: string) => void
    triggerTestId?: string
  }) => (
    <input
      data-testid={triggerTestId ?? 'model-selector'}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}))

beforeEach(() => {
  act(() => {
    useUiStore.setState({
      createAgentModalOpen: false,
      sessionPanelOpen: false,
      toasts: [],
    })
  })
})

function renderWizard(props: Partial<Parameters<typeof CreateAgentWizard>[0]> = {}) {
  const onSubmit = props.onSubmit ?? vi.fn().mockResolvedValue(undefined)
  const onClose = props.onClose ?? vi.fn()
  return render(
    <CreateAgentWizard
      initialType={props.initialType ?? 'Main'}
      {...(props.initialCli ? { initialCli: props.initialCli } : {})}
      onSubmit={onSubmit}
      onClose={onClose}
    />,
  )
}

describe('CreateAgentWizard — type / CLI chips (spec §5.2, §11 #3)', () => {
  it('renders the type chip with "Main" for the Main type', () => {
    renderWizard({ initialType: 'Main' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Main$/)
  })

  it('renders the type chip with "Subagent" for the Subagent type', () => {
    renderWizard({ initialType: 'Subagent' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Subagent$/)
  })

  it('renders "Subagent (External)" for the subagent_3p type', () => {
    renderWizard({ initialType: 'subagent_3p' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/Subagent \(External\)/)
  })

  it('renders the locked CLI chip for External wizards', () => {
    renderWizard({ initialType: 'subagent_3p', initialCli: 'claude-code' })
    expect(screen.getByTestId('cli-chip')).toHaveTextContent(/claude-code/)
  })

  it('does NOT render a CLI chip for Main / Subagent (CLI is external-only)', () => {
    renderWizard({ initialType: 'Main' })
    expect(screen.queryByTestId('cli-chip')).toBeNull()
    renderWizard({ initialType: 'Subagent' })
    expect(screen.queryByTestId('cli-chip')).toBeNull()
  })

  it('the [×] on the type chip fires onClose (no confirmation, per spec §11 #3)', () => {
    const onClose = vi.fn()
    renderWizard({ onClose })
    fireEvent.click(screen.getByRole('button', { name: /cancel wizard/i }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})

describe('CreateAgentWizard — stepper a11y (spec §9.3, WAI-ARIA APG)', () => {
  it('renders a list (ol) with aria-label "Wizard progress: step N of 3"', () => {
    renderWizard()
    const stepper = screen.getByTestId('wizard-stepper')
    expect(stepper.tagName).toBe('OL')
    expect(stepper).toHaveAttribute('aria-label', 'Wizard progress: step 1 of 3')
  })

  it('marks the current step with aria-current="step"', () => {
    renderWizard()
    const stepper = screen.getByTestId('wizard-stepper')
    const active = stepper.querySelector('[aria-current="step"]')
    expect(active).not.toBeNull()
    expect(active).toHaveTextContent(/Step 1: Identity/i)
  })
})

describe('CreateAgentWizard — step gating (spec §9.2)', () => {
  it('Next is disabled on step 1 with all fields empty', () => {
    renderWizard()
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
  })

  it('Next is disabled on step 1 when only name is filled (model also required)', () => {
    renderWizard()
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
  })

  it('Next is enabled on step 1 when name + model are filled (Main type)', async () => {
    renderWizard()
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    await waitFor(() => expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled())
  })

  it('Next on step 1 stays disabled for Subagent until name + model + description are filled', () => {
    // Inherit toggles default OFF (UAT e2e fix), so the model picker renders
    // and a model is required alongside name + description before step 1 can
    // advance for a native Subagent.
    renderWizard({ initialType: 'Subagent' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'W' } })
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'handles X' } })
    expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled()
  })

  it('Next on step 2 stays disabled until soul is non-empty', async () => {
    renderWizard()
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    const next = await screen.findByTestId('wizard-next-2')
    expect(next).toBeDisabled()
    fireEvent.change(screen.getByTestId('wizard-soul'), { target: { value: 'hi' } })
    await waitFor(() => expect(next).not.toBeDisabled())
  })
})

describe('CreateAgentWizard — step navigation', () => {
  it('Back button is hidden on step 1', () => {
    renderWizard()
    expect(screen.queryByTestId('wizard-back')).toBeNull()
  })

  it('Back appears on step 2 and returns to step 1', async () => {
    renderWizard()
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    expect(await screen.findByTestId('wizard-back')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('wizard-back'))
    // Back on step 1 hides the Back button again.
    expect(screen.queryByTestId('wizard-back')).toBeNull()
  })

  it('Create button appears on step 3 (after step 1 + 2)', async () => {
    renderWizard()
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    expect(await screen.findByTestId('wizard-create')).toBeInTheDocument()
  })
})

describe('CreateAgentWizard — submit success', () => {
  it('calls onSubmit with the current payload on Create click', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    renderWizard({ onSubmit })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'Main',
          name: 'X',
          model: 'm',
          soul: 'hi',
        }),
      )
    })
  })

  it('disables the Create button while submitting', async () => {
    let resolveSubmit!: () => void
    const onSubmit = vi.fn(() => new Promise<void>((r) => { resolveSubmit = r }))
    renderWizard({ onSubmit })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    // While the submit promise is pending, the button is disabled and
    // the label switches to "Creating…".
    await waitFor(() => {
      const btn = screen.getByTestId('wizard-create')
      expect(btn).toBeDisabled()
      expect(btn).toHaveTextContent(/creating/i)
    })
    resolveSubmit()
  })
})

describe('CreateAgentWizard — submit error (silent-failure-hunter fix)', () => {
  it('surfaces the error in an inline role=alert and keeps the wizard open', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('Server rejected agent create'))
    renderWizard({ onSubmit })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    const err = await screen.findByTestId('wizard-submit-error')
    expect(err).toHaveTextContent(/server rejected/i)
    // Wizard stays open.
    expect(screen.getByTestId('wizard-stepper')).toBeInTheDocument()
  })

  it('clears the error banner when the user edits a field', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('Boom'))
    renderWizard({ onSubmit })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await screen.findByTestId('wizard-submit-error')
    // Editing any field clears the stale error.
    fireEvent.click(screen.getByTestId('wizard-back'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'rewritten' } })
    expect(screen.queryByTestId('wizard-submit-error')).toBeNull()
  })

  it('does NOT fire onClose when submit fails (per silent-failure-hunter fix)', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('Boom'))
    const onClose = vi.fn()
    renderWizard({ onSubmit, onClose })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await screen.findByTestId('wizard-submit-error')
    expect(onClose).not.toHaveBeenCalled()
  })
})

describe('CreateAgentWizard — Cancel button', () => {
  it('fires onClose when the footer Cancel button is clicked', () => {
    const onClose = vi.fn()
    renderWizard({ onClose })
    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})

describe('CreateAgentWizard — missing-field hint (B3 fix)', () => {
  it('(a) Main with empty Name + Model: hint names both missing fields and Next is disabled', () => {
    renderWizard({ initialType: 'Main' })
    const hint = screen.getByTestId('wizard-advance-hint')
    expect(hint).toHaveTextContent(/Name/)
    expect(hint).toHaveTextContent(/Model/)
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
  })

  it('(b) filling Name + Model removes the hint and enables Next', async () => {
    renderWizard({ initialType: 'Main' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'My Agent' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'gpt-4' } })
    await waitFor(() => {
      expect(screen.queryByTestId('wizard-advance-hint')).toBeNull()
      expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled()
    })
  })

  it('(c) Subagent with inherit_model ON: Model is NOT listed as missing', () => {
    renderWizard({ initialType: 'Subagent' })
    // Turn inherit_model ON so model is not required.
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    const hint = screen.getByTestId('wizard-advance-hint')
    // Name and Description are still missing, but Model must NOT appear.
    expect(hint).not.toHaveTextContent(/Model/)
    expect(hint).toHaveTextContent(/Name/)
  })

  it('(d) step 2 with empty Soul: hint names Soul', async () => {
    renderWizard({ initialType: 'Main' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    // On step 2 the soul field is empty — hint must name it.
    const hint = await screen.findByTestId('wizard-advance-hint')
    expect(hint).toHaveTextContent(/Soul/)
    expect(screen.getByTestId('wizard-next-2')).toBeDisabled()
  })
})

describe('CreateAgentWizard — inherit-from-caller toggles (UAT 4a)', () => {
  it('renders the Model inherit toggle on step 1 for a native Subagent (OFF by default)', () => {
    renderWizard({ initialType: 'Subagent' })
    const toggle = screen.getByTestId('wizard-inherit-model') as HTMLInputElement
    expect(toggle).toBeInTheDocument()
    // Default OFF → explicit override → the model picker is shown so the
    // operator picks a model (UAT e2e fix). Inheritance stays an opt-in.
    expect(toggle.checked).toBe(false)
    expect(screen.getByTestId('wizard-model')).toBeInTheDocument()
  })

  it('hides the model picker when the Model inherit toggle is turned ON', () => {
    renderWizard({ initialType: 'Subagent' })
    // Picker visible by default; turning inherit ON hides it.
    expect(screen.getByTestId('wizard-model')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    expect(screen.queryByTestId('wizard-model')).toBeNull()
  })

  it('does NOT render inherit toggles for a Main agent', () => {
    renderWizard({ initialType: 'Main' })
    expect(screen.queryByTestId('wizard-inherit-model')).toBeNull()
  })

  it('lets a native Subagent advance past step 1 with NO model once inherit is turned ON', () => {
    renderWizard({ initialType: 'Subagent' })
    // inherit_model defaults OFF, so name + description + model are required.
    // Turning inherit ON drops the model requirement.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Researcher' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'does research' } })
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    const next = screen.getByTestId('wizard-next-1') as HTMLButtonElement
    expect(next).not.toBeDisabled()
  })

  it('renders the Tools and Skills inherit toggles on step 3 for a native Subagent', async () => {
    renderWizard({ initialType: 'Subagent' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Researcher' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'does research' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'soul' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    expect(await screen.findByTestId('wizard-inherit-tools')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-inherit-skills')).toBeInTheDocument()
    // Tools editor visible while NOT inherited (default OFF).
    expect(screen.getByTestId('wizard-tools-cfg')).toBeInTheDocument()
  })
})

describe('CreateAgentWizard — missing-field hint (B3 fix)', () => {
  it('names the missing required fields when Next is disabled (Main: Name + Model)', () => {
    renderWizard({ initialType: 'Main' })
    const hint = screen.getByTestId('wizard-advance-hint')
    expect(hint).toHaveTextContent(/Name/)
    expect(hint).toHaveTextContent(/Model/)
    expect(screen.getByTestId('wizard-next-1')).toBeDisabled()
  })

  it('hides the hint and enables Next once required fields are filled', async () => {
    renderWizard({ initialType: 'Main' })
    expect(screen.getByTestId('wizard-advance-hint')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    await waitFor(() => {
      expect(screen.queryByTestId('wizard-advance-hint')).toBeNull()
      expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled()
    })
  })

  it('does not list Model as missing when a native Subagent inherits the model', () => {
    renderWizard({ initialType: 'Subagent' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'W' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'handles X' } })
    // Model still required (inherit OFF) → listed.
    expect(screen.getByTestId('wizard-advance-hint')).toHaveTextContent(/Model/)
    // Turn inherit ON → Model no longer required, Next enabled, hint gone.
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    expect(screen.queryByTestId('wizard-advance-hint')).toBeNull()
    expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled()
  })

  it('names Soul as missing on step 2', async () => {
    renderWizard({ initialType: 'Main' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    expect(await screen.findByTestId('wizard-advance-hint')).toHaveTextContent(/Soul/)
  })
})
