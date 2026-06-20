import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

// Wave 5b spec tests — OnboardingWizard frontend tests
// Traces to: wave5b-system-agent-spec.md — Onboarding Flow BDD scenarios
//
// FR-12.3 flow (3 numbered steps + unnumbered completion screen):
//   Step 1 — "What should I call you?" (name/username)
//   Step 2 — "Set your password" (password + confirm)
//   Step 3 — "Add a model key" (provider + API key + model)
//   Completion — "Meet your Assistant" (Mia intro, Start chatting)
// The step indicator tracks the 3 numbered steps only; the completion screen
// is not a numbered step, so aria-valuemax is 3.

// Mock TanStack Router navigate
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => opts,
  useNavigate: () => mockNavigate,
  redirect: (opts: unknown) => opts,
  useRouteContext: () => ({ appStateBannerMessage: null }),
}))

// Mock Framer Motion — strip all animations so AnimatePresence doesn't keep
// exit elements in the DOM during state transitions.
vi.mock('framer-motion', () => {
  const React = require('react')
  return {
    motion: new Proxy(
      {},
      {
        get: (_target: object, prop: string) => {
          return React.forwardRef(
            ({ children, ...props }: Record<string, unknown>, ref: unknown) =>
              React.createElement(prop as string, { ...props, ref }, children)
          )
        },
      }
    ),
    AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
  }
})

// Mock API calls — includes completeOnboardingTransaction and probeProvider
// which are called during the flow.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    configureProvider: vi.fn(),
    probeProvider: vi.fn(),
    completeOnboardingTransaction: vi.fn(),
    // fetchProviders is called after a successful test to populate the model list.
    // Return empty models so ModelSelector renders in free-text (Input) mode.
    fetchProviders: vi.fn().mockResolvedValue([]),
  }
})

// Mock SVG import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

import { configureProvider, probeProvider, completeOnboardingTransaction } from '@/lib/api'
import { evaluatePasswordStrength, friendlyProbeError } from './onboarding'

// Cache the dynamically imported component across all tests so the first import's
// transform cost (~20s) only pays once and doesn't time out individual tests.
let WizardComponent: React.ComponentType | null = null

beforeAll(async () => {
  const mod = await import('./onboarding')
  WizardComponent = ((mod.Route as unknown) as { component: React.ComponentType }).component
})

async function renderWizard() {
  if (!WizardComponent) throw new Error('WizardComponent not loaded — beforeAll did not run')
  return render(<WizardComponent />)
}

beforeEach(() => {
  vi.clearAllMocks()
  mockNavigate.mockResolvedValue(undefined)
  vi.mocked(completeOnboardingTransaction).mockResolvedValue({
    token: 'test-token',
    role: 'admin',
    username: 'admin',
  } as never)
})

// Helper: advance from step 1 (Name) to step 2 (Password) by entering a
// username and clicking Continue.
async function advanceNameToPassword(username = 'admin') {
  fireEvent.change(screen.getByLabelText(/username/i), { target: { value: username } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/set your password/i))
}

// Helper: advance from step 2 (Password) to step 3 (Model key).
async function advancePasswordToModelKey(password = 'password123') {
  fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: password } })
  fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: password } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/add a model key/i))
}

// Helper: from step 3, select a provider, enter a key, connect, and pick a
// model — leaves the wizard on step 3 with Complete Setup enabled.
async function connectProviderOnStep3() {
  vi.mocked(configureProvider).mockResolvedValue({} as never)
  vi.mocked(probeProvider).mockResolvedValue({ success: true })

  fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
  await waitFor(() => screen.getByLabelText('API Key'))
  fireEvent.change(screen.getByLabelText('API Key'), {
    target: { value: 'sk-ant-api03-test' },
  })
  fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
  await waitFor(() => screen.getByText(/connected successfully/i))
  const modelInput = await waitFor(() => screen.getByPlaceholderText(/enter model slug/i))
  fireEvent.change(modelInput, { target: { value: 'claude-3-haiku' } })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /complete setup/i })).not.toBeDisabled()
  )
}

// =====================================================================
// Scenario: Step navigation
// =====================================================================

describe('OnboardingWizard — step navigation', () => {
  it('renders step 1 (Name) by default', async () => {
    await renderWizard()
    expect(screen.getByText(/what should i call you/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /continue/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /skip/i })).not.toBeInTheDocument()
  })

  it('advances to step 2 (Password) when Continue is clicked with a username', async () => {
    await renderWizard()
    await advanceNameToPassword()
    expect(screen.getByText(/set your password/i)).toBeInTheDocument()
  })

  it('shows step progress indicator with 3 dots (aria-valuemax=3)', async () => {
    await renderWizard()
    const progressbar = screen.getByRole('progressbar')
    expect(progressbar).toBeInTheDocument()
    expect(progressbar).toHaveAttribute('aria-valuenow', '1')
    expect(progressbar).toHaveAttribute('aria-valuemin', '1')
    expect(progressbar).toHaveAttribute('aria-valuemax', '3')
  })

  it('step 1 Continue is disabled until a username is entered', async () => {
    await renderWizard()
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
  })

  it('step 2 Back button returns to step 1', async () => {
    await renderWizard()
    await advanceNameToPassword()
    fireEvent.click(screen.getByRole('button', { name: /back/i }))
    await waitFor(() => {
      expect(screen.getByText(/what should i call you/i)).toBeInTheDocument()
    })
  })

  it('step 3 Back button returns to step 2', async () => {
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
    expect(screen.getByText(/add a model key/i)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /back/i }))
    await waitFor(() => {
      expect(screen.getByText(/set your password/i)).toBeInTheDocument()
    })
  })
})

// =====================================================================
// Scenario: Step 2 password validation
// =====================================================================

describe('OnboardingWizard — password step', () => {
  it('Continue is disabled until passwords match and are >= 8 chars', async () => {
    await renderWizard()
    await advanceNameToPassword()
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'short' } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'short' } })
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'password123' } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'different123' } })
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'password123' } })
    expect(screen.getByRole('button', { name: /continue/i })).not.toBeDisabled()
  })
})

// =====================================================================
// Scenario: Provider selection (Step 3)
// =====================================================================

describe('OnboardingWizard — provider selection', () => {
  async function goToStep3() {
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
  }

  it('shows key providers in step 3', async () => {
    await goToStep3()
    expect(screen.getByRole('button', { name: 'Anthropic' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'OpenRouter' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'OpenAI' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Google Gemini' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Groq' })).toBeInTheDocument()
  })

  it('shows API key input when a provider is selected', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => {
      expect(screen.getByLabelText('API Key')).toBeInTheDocument()
    })
  })

  it('shows placeholder hint for the selected provider', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => {
      const input = screen.getByLabelText('API Key')
      expect(input).toHaveAttribute('placeholder', 'Starts with sk-ant-...')
    })
  })

  it('API key input defaults to password type (hidden)', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => {
      const input = screen.getByLabelText('API Key')
      expect(input).toHaveAttribute('type', 'password')
    })
  })

  it('show/hide toggle reveals the API key', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.click(screen.getByRole('button', { name: /show api key/i }))
    expect(screen.getByLabelText('API Key')).toHaveAttribute('type', 'text')
  })
})

// =====================================================================
// Scenario: Connect & Load Models (Step 3)
// =====================================================================

describe('OnboardingWizard — test connection', () => {
  async function goToProviderAndSelect() {
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), {
      target: { value: 'sk-ant-api03-test' },
    })
  }

  it('Connect & Load Models button is disabled when API key is empty', async () => {
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    const connectBtn = screen.getByRole('button', { name: /connect & load models/i })
    expect(connectBtn).toBeDisabled()
  })

  it('shows success feedback on successful connection', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(screen.getByText(/connected successfully/i)).toBeInTheDocument()
    })
  })

  it('shows error feedback on failed connection', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'Invalid API key' })
    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(screen.getByText(/invalid api key/i)).toBeInTheDocument()
    })
  })

  it('Complete Setup is disabled until connection succeeds and a model is chosen', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'Bad key' })
    await goToProviderAndSelect()
    expect(screen.getByRole('button', { name: /complete setup/i })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByText(/bad key/i))
    expect(screen.getByRole('button', { name: /complete setup/i })).toBeDisabled()
  })

  it('Complete Setup enabled after successful connection and model selection', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByText(/connected successfully/i))
    const modelInput = await waitFor(() => screen.getByPlaceholderText(/enter model slug/i))
    fireEvent.change(modelInput, { target: { value: 'claude-3-haiku' } })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /complete setup/i })).not.toBeDisabled()
    })
  })
})

// =====================================================================
// friendlyProbeError — pure unit tests (unchanged behaviour)
// =====================================================================

describe('friendlyProbeError', () => {
  it('maps 401/403 (and auth-ish text) to a "key rejected" message naming the provider', () => {
    for (const raw of [
      'upstream models: status 401',
      'status 403',
      '401 unauthorized',
      'invalid api key',
      'request rejected',
    ]) {
      const msg = friendlyProbeError(raw, 'OpenAI')
      expect(msg).toMatch(/rejected by OpenAI/i)
      expect(msg).toMatch(/double-check/i)
    }
  })

  it('maps 429 / rate-limit text to a "rate limited" message naming the provider', () => {
    expect(friendlyProbeError('upstream models: status 429', 'Groq')).toMatch(
      /rate limited by Groq/i
    )
    expect(friendlyProbeError('rate limit exceeded', 'Groq')).toMatch(/rate limited by Groq/i)
  })

  it('falls back to a generic "couldn\'t reach" message for network/timeout/unknown', () => {
    for (const raw of ['network error', 'request timeout', 'dial tcp: connection refused', '']) {
      const msg = friendlyProbeError(raw, 'Anthropic')
      expect(msg).toMatch(/couldn.t reach Anthropic/i)
    }
  })

  it('does not misclassify a 404/500 status as an auth error', () => {
    expect(friendlyProbeError('status 404', 'OpenAI')).toMatch(/couldn.t reach OpenAI/i)
    expect(friendlyProbeError('status 500', 'OpenAI')).toMatch(/couldn.t reach OpenAI/i)
  })
})

// =====================================================================
// Scenario: friendly probe error in the UI (display-layer mapping + a11y)
// =====================================================================

describe('OnboardingWizard — friendly probe error display', () => {
  async function failConnect(rawError: string) {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: rawError })
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'sk-ant-test' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByTestId('onboarding-error'))
  }

  it('renders a friendly message (not the raw upstream string) as the primary error', async () => {
    await failConnect('upstream models: status 401')
    expect(screen.getByText(/rejected by Anthropic/i)).toBeInTheDocument()
  })

  it('keeps the raw upstream string available behind a Technical details disclosure', async () => {
    await failConnect('upstream models: status 401')
    expect(screen.getByText(/technical details/i)).toBeInTheDocument()
    expect(screen.getByText('upstream models: status 401')).toBeInTheDocument()
  })

  it('the probe error container is a live region announced to screen readers', async () => {
    await failConnect('status 429')
    const alert = screen.getByTestId('onboarding-error')
    expect(alert).toHaveAttribute('role', 'alert')
    expect(alert).toHaveAttribute('aria-live', 'assertive')
    expect(alert.textContent).toMatch(/error:/i)
  })
})

// =====================================================================
// Scenario: visible step counter (sighted users) — 3 steps
// =====================================================================

describe('OnboardingWizard — visible step indicator', () => {
  it('shows a visible "Step 1 of 3" counter alongside the progressbar', async () => {
    await renderWizard()
    const matches = screen.getAllByText(/step 1 of 3/i)
    expect(matches.length).toBeGreaterThanOrEqual(2)
  })
})

// =====================================================================
// Scenario: No skip button
// =====================================================================

describe('OnboardingWizard — no skip button', () => {
  it('Step 1 has no Skip button', async () => {
    await renderWizard()
    expect(screen.queryByRole('button', { name: /skip/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/skip/i)).not.toBeInTheDocument()
  })
})

// =====================================================================
// evaluatePasswordStrength — pure heuristic (unchanged)
// =====================================================================

describe('evaluatePasswordStrength', () => {
  it('returns null for empty input', () => {
    expect(evaluatePasswordStrength('')).toBeNull()
  })

  it('treats <8 chars as "Too short" (score 1) even with mixed classes', () => {
    const r = evaluatePasswordStrength('Aa1!')
    expect(r).toEqual({ score: 1, label: 'Too short', color: 'var(--color-error)' })
  })

  it('scores an 8-char single-class password as "Weak" (1)', () => {
    const r = evaluatePasswordStrength('aaaaaaaa')
    expect(r?.score).toBe(1)
    expect(r?.label).toBe('Weak')
  })

  it('scores an 8-char two-class password as "Fair" (2)', () => {
    const r = evaluatePasswordStrength('aaaaaaaA')
    expect(r?.score).toBe(2)
    expect(r?.label).toBe('Fair')
  })

  it('scores an 8-char three-class password as "Good" (3)', () => {
    const r = evaluatePasswordStrength('aaaaaA12')
    expect(r?.score).toBe(3)
    expect(r?.label).toBe('Good')
  })

  it('scores a 12-char four-class password as "Strong" (4)', () => {
    const r = evaluatePasswordStrength('aaaaaaaA12!@')
    expect(r?.score).toBe(4)
    expect(r?.label).toBe('Strong')
    expect(r?.color).toBe('var(--color-success)')
  })

  it('never exceeds score 4 for very long, very diverse passwords', () => {
    const r = evaluatePasswordStrength('Abcdefgh12345678!@#$%^&*()')
    expect(r?.score).toBe(4)
    expect(r?.score).toBeLessThanOrEqual(4)
  })

  it('uses only the closed set of brand-token colors', () => {
    const allowed = new Set([
      'var(--color-error)',
      'var(--color-warning)',
      'var(--color-accent)',
      'var(--color-success)',
    ])
    for (const pw of ['', 'a', 'aaaaaaaa', 'aaaaaaaA', 'aaaaaA12', 'aaaaaaaA12!@']) {
      const r = evaluatePasswordStrength(pw)
      if (r) expect(allowed.has(r.color)).toBe(true)
    }
  })
})

// =====================================================================
// Scenario: Finish onboarding — Complete Setup → Meet your Assistant
// =====================================================================

describe('OnboardingWizard — finish', () => {
  // Helper: walk through the 3 numbered steps to leave the wizard on step 3
  // with Complete Setup enabled.
  async function goToCompleteReady() {
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
    await connectProviderOnStep3()
  }

  it('completing calls completeOnboardingTransaction and reveals Meet your Assistant', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToCompleteReady()

    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))

    await waitFor(() => {
      expect(completeOnboardingTransaction).toHaveBeenCalledOnce()
    })
    // The Meet your Assistant screen is shown (Mia intro + Start chatting CTA).
    await waitFor(() => {
      expect(screen.getByText(/meet your assistant|Mia — Assistant/i)).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /start chatting/i })).toBeInTheDocument()
  })

  it('Start chatting on Meet your Assistant navigates to root', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))
    await waitFor(() => screen.getByRole('button', { name: /start chatting/i }))

    fireEvent.click(screen.getByRole('button', { name: /start chatting/i }))

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    })
  })

  it('Meet your Assistant shows Mia with the default-star badge and her role', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))
    await waitFor(() => screen.getByText(/Mia — Assistant/i))

    // Default-agent star badge is present.
    expect(screen.getByLabelText(/default agent/i)).toBeInTheDocument()
    // Role line.
    expect(screen.getByText(/memory-rich, cross-workspace recall/i)).toBeInTheDocument()
    // Intro line referencing My Workspace + default agent.
    expect(screen.getByText(/bound to my workspace/i)).toBeInTheDocument()
  })

  it('surfaces an inline error and stays on step 3 with Retry Setup when finish fails', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-error')).toBeInTheDocument()
    })
    expect(screen.getByText(/server exploded/i)).toBeInTheDocument()
    // Stayed on step 3 (still showing the model-key heading) and did not navigate.
    expect(screen.getByText(/add a model key/i)).toBeInTheDocument()
    expect(mockNavigate).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: /retry setup/i })).toBeInTheDocument()
  })

  it('retry after a failure clears the error and reveals Meet your Assistant', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    vi.mocked(completeOnboardingTransaction)
      .mockRejectedValueOnce(new Error('transient outage'))
      .mockResolvedValueOnce({ token: 't', role: 'admin', username: 'admin' } as never)

    await goToCompleteReady()

    // First attempt fails → inline error + Retry Setup CTA.
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))
    await waitFor(() => expect(screen.getByTestId('onboarding-error')).toBeInTheDocument())

    // Retry succeeds → error clears, Meet your Assistant reveals.
    fireEvent.click(screen.getByRole('button', { name: /retry setup/i }))
    await waitFor(() => {
      expect(screen.getByText(/Mia — Assistant/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('onboarding-error')).not.toBeInTheDocument()
  })
})
