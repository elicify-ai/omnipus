import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

// Wave 5b spec tests — OnboardingWizard frontend tests
// Traces to: wave5b-system-agent-spec.md — Onboarding Flow BDD scenarios

// Wave B migration: component evolved from 3-step to 4-step wizard.
// Step 1 — Welcome, Step 2 — Provider Setup, Step 3 — Admin Credentials, Step 4 — Done.
// All aria-valuemax assertions updated 3 → 4.
// "Test Connection" button was renamed to "Connect & Load Models" in the component.
// finish flow: component calls completeOnboardingTransaction (not completeOnboarding).
// Skip button removed: the Welcome step no longer offers a skip path.

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
// which are called during the 4-step flow.
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
import { evaluatePasswordStrength } from './onboarding'

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

// =====================================================================
// Scenario: Step navigation (US-7 AC1, AC2, AC3)
// Traces to: wave5b-system-agent-spec.md — Scenario: Onboarding step navigation
// =====================================================================

describe('OnboardingWizard — step navigation', () => {
  it('renders step 1 (Welcome) by default with Get Started button and no Skip button', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: First-launch shows welcome screen
    // BDD: Given fresh install, When onboarding route loads, Then step 1 Welcome is shown.
    // Skip button was removed to prevent users landing in an unconfigured app.
    await renderWizard()

    expect(screen.getByText('Welcome to Omnipus')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /get started/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /skip/i })).not.toBeInTheDocument()
  })

  it('advances to step 2 (Provider) when Get Started is clicked', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Get Started advances to provider step
    // BDD: Given step 1, When Get Started clicked, Then step 2 shown with provider selection
    await renderWizard()

    fireEvent.click(screen.getByRole('button', { name: /get started/i }))

    await waitFor(() => {
      expect(screen.getByText(/connect a provider/i)).toBeInTheDocument()
    })
  })

  it('shows step progress indicator with 4 dots', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Step indicator shows progress
    // BDD: Given wizard at any step, Then progressbar with 4 steps is visible
    await renderWizard()

    const progressbar = screen.getByRole('progressbar')
    expect(progressbar).toBeInTheDocument()
    expect(progressbar).toHaveAttribute('aria-valuenow', '1')
    expect(progressbar).toHaveAttribute('aria-valuemin', '1')
    expect(progressbar).toHaveAttribute('aria-valuemax', '4')
  })

  it('step 2 Back button returns to step 1', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Back navigation in wizard
    await renderWizard()

    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))

    fireEvent.click(screen.getByRole('button', { name: /back/i }))

    await waitFor(() => {
      expect(screen.getByText('Welcome to Omnipus')).toBeInTheDocument()
    })
  })
})

// =====================================================================
// Scenario: Provider selection (US-8 AC1, AC3)
// Traces to: wave5b-system-agent-spec.md — Scenario: Provider setup in onboarding
// =====================================================================

describe('OnboardingWizard — provider selection', () => {
  it('shows all 5 key providers in step 2', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Provider selection UI
    // BDD: Given step 2, Then Anthropic, OpenRouter, OpenAI, Google Gemini, Groq are shown.
    // Note: Use exact name strings to avoid false matches (e.g. /openai/i matches "Azure OpenAI").
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))

    expect(screen.getByRole('button', { name: 'Anthropic' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'OpenRouter' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'OpenAI' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Google Gemini' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Groq' })).toBeInTheDocument()
  })

  it('shows API key input when a provider is selected', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: API key input appears after provider selection
    // BDD: Given step 2, When Anthropic selected, Then API key input is shown
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))

    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))

    await waitFor(() => {
      expect(screen.getByLabelText('API Key')).toBeInTheDocument()
    })
  })

  it('shows placeholder hint for the selected provider', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Provider-specific placeholder hints
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))

    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))

    await waitFor(() => {
      const input = screen.getByLabelText('API Key')
      expect(input).toHaveAttribute('placeholder', 'Starts with sk-ant-...')
    })
  })

  it('API key input defaults to password type (hidden)', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: API key is masked by default (US-8 AC3)
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))

    await waitFor(() => {
      const input = screen.getByLabelText('API Key')
      expect(input).toHaveAttribute('type', 'password')
    })
  })

  it('show/hide toggle reveals the API key', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Toggle API key visibility
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))

    await waitFor(() => screen.getByLabelText('API Key'))
    const toggleBtn = screen.getByRole('button', { name: /show api key/i })
    fireEvent.click(toggleBtn)

    expect(screen.getByLabelText('API Key')).toHaveAttribute('type', 'text')
  })
})

// =====================================================================
// Scenario: Connect & Load Models (US-8 AC4, AC5)
// Traces to: wave5b-system-agent-spec.md — Scenario: Test connection success/failure
// Note: The button was renamed from "Test Connection" to "Connect & Load Models".
// =====================================================================

describe('OnboardingWizard — test connection', () => {
  async function goToProviderStep() {
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), {
      target: { value: 'sk-ant-api03-test' },
    })
  }

  it('Connect & Load Models button is disabled when API key is empty', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Test Connection disabled without key
    await renderWizard()
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))

    const connectBtn = screen.getByRole('button', { name: /connect & load models/i })
    expect(connectBtn).toBeDisabled()
  })

  it('shows success feedback on successful connection', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Test connection success (US-8 AC4)
    // BDD: Given API key entered, When test succeeds, Then "Connected successfully" shown
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToProviderStep()

    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    await waitFor(() => {
      expect(screen.getByText(/connected successfully/i)).toBeInTheDocument()
    })
  })

  it('shows error feedback on failed connection', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Test connection failure (US-8 AC5)
    // BDD: Given API key entered, When test fails, Then error message shown
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'Invalid API key' })

    await goToProviderStep()

    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    await waitFor(() => {
      expect(screen.getByText(/invalid api key/i)).toBeInTheDocument()
    })
  })

  it('Continue button is disabled until connection is successful and model is selected', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Continue gated on successful test
    // BDD: Given step 2, Until connection succeeds AND model is chosen, Continue is disabled.
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'Bad key' })

    await goToProviderStep()

    // Before connection attempt: Continue is disabled
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByText(/bad key/i))

    // After failed connection: Continue still disabled
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
  })

  it('Continue button enabled after successful connection and model selection', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Continue enabled after success
    // Continue requires both testStatus === 'success' AND selectedModel.trim() non-empty.
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToProviderStep()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByText(/connected successfully/i))

    // fetchProviders returns [] so ModelSelector renders as a free-text Input.
    // Enter a model slug to satisfy the selectedModel.trim() requirement.
    const modelInput = await waitFor(() => screen.getByPlaceholderText(/enter model slug/i))
    fireEvent.change(modelInput, { target: { value: 'claude-3-haiku' } })

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /continue/i })).not.toBeDisabled()
    })
  })
})

// =====================================================================
// Scenario: Skip onboarding — button removed
// The Welcome step no longer exposes a skip path.
// Users must complete provider + admin setup to reach the app.
// =====================================================================

describe('OnboardingWizard — no skip button', () => {
  it('Welcome step has no Skip button', async () => {
    // BDD: Given step 1, Then no "Skip" or "skip" button exists in the DOM.
    // The skip path was removed to prevent users landing in an unconfigured app.
    await renderWizard()

    expect(screen.queryByRole('button', { name: /skip/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/skip/i)).not.toBeInTheDocument()
  })
})

// =====================================================================
// evaluatePasswordStrength — pure heuristic, table-driven boundary coverage.
// Empty → null; <8 chars → "Too short" (score 1) regardless of class mix;
// >=8 scores 1–4 by length + character-class diversity. Score is never > 4.
// =====================================================================

describe('evaluatePasswordStrength', () => {
  it('returns null for empty input', () => {
    expect(evaluatePasswordStrength('')).toBeNull()
  })

  it('treats <8 chars as "Too short" (score 1) even with mixed classes', () => {
    // "Aa1!" has all four classes but is only 4 chars → still gated to score 1.
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
// Scenario: Finish onboarding (US-7 AC9)
// Traces to: wave5b-system-agent-spec.md — Scenario: Onboarding finish persists state
// =====================================================================

describe('OnboardingWizard — finish', () => {
  // Helper: complete the 4-step flow up to and including Step 4 (Done).
  // Step 2 → successful provider connection → Step 3 → admin credentials → Step 4.
  async function goToFinishStep() {
    await renderWizard()

    // Step 1 → 2
    fireEvent.click(screen.getByRole('button', { name: /get started/i }))
    await waitFor(() => screen.getByText(/connect a provider/i))

    // Select provider, enter key, connect
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), {
      target: { value: 'sk-ant-api03-test' },
    })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByText(/connected successfully/i))

    // Select a model so Continue is enabled
    const modelInput = await waitFor(() => screen.getByPlaceholderText(/enter model slug/i))
    fireEvent.change(modelInput, { target: { value: 'claude-3-haiku' } })
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /continue/i })).not.toBeDisabled()
    )

    // Step 2 → 3
    fireEvent.click(screen.getByRole('button', { name: /continue/i }))
    await waitFor(() => screen.getByLabelText(/username/i))

    // Fill admin credentials
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'password123' } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'password123' } })
    fireEvent.click(screen.getByRole('button', { name: /create account/i }))

    // Step 3 → 4
    await waitFor(() => screen.getByText(/you.re all set/i))
  }

  it('finishing calls completeOnboardingTransaction and navigates to root', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Onboarding completion (US-7 AC9)
    // BDD: Given step 4, When Start Exploring clicked, Then completeOnboardingTransaction
    //      called with provider + admin data, then navigate to /.
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToFinishStep()

    // Finish
    fireEvent.click(screen.getByRole('button', { name: /start exploring/i }))

    await waitFor(() => {
      expect(completeOnboardingTransaction).toHaveBeenCalledOnce()
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    })
  })

  it('displays provider name on completion step', async () => {
    // Traces to: wave5b-system-agent-spec.md — Scenario: Step 4 shows provider name
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })

    await goToFinishStep()

    await waitFor(() => {
      expect(screen.getByText(/anthropic connected/i)).toBeInTheDocument()
    })
  })

  it('surfaces an inline error and stays on step 4 with Retry Setup when finish fails', async () => {
    // BDD: Given step 4, When completeOnboardingTransaction rejects, Then an inline
    //      error (data-testid="onboarding-error") is shown, the user stays on step 4,
    //      navigation does NOT fire, and the CTA becomes "Retry Setup".
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))

    await goToFinishStep()
    fireEvent.click(screen.getByRole('button', { name: /start exploring/i }))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-error')).toBeInTheDocument()
    })
    expect(screen.getByText(/server exploded/i)).toBeInTheDocument()
    // Stayed on step 4 (still showing the Done heading) and did not navigate.
    expect(screen.getByText(/you.re all set/i)).toBeInTheDocument()
    expect(mockNavigate).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: /retry setup/i })).toBeInTheDocument()
  })

  it('retry after a failure clears the error and navigates on success', async () => {
    // BDD: Given a finish failure on step 4, When the user clicks Retry Setup and the
    //      retry succeeds, Then the inline error clears and navigation to / fires.
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    vi.mocked(completeOnboardingTransaction)
      .mockRejectedValueOnce(new Error('transient outage'))
      .mockResolvedValueOnce({ token: 't', role: 'admin', username: 'admin' } as never)

    await goToFinishStep()

    // First attempt fails → inline error + Retry Setup CTA.
    fireEvent.click(screen.getByRole('button', { name: /start exploring/i }))
    await waitFor(() => expect(screen.getByTestId('onboarding-error')).toBeInTheDocument())

    // Retry succeeds → error clears, navigates to root.
    fireEvent.click(screen.getByRole('button', { name: /retry setup/i }))
    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    })
    expect(screen.queryByTestId('onboarding-error')).not.toBeInTheDocument()
  })
})
