import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import React from 'react'
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
  return {
    motion: new Proxy(
      {},
      {
        get: (_target: object, prop: string) => {
          return React.forwardRef(
            ({ children, ...props }: Record<string, unknown>, ref: unknown) =>
              React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)
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
import { evaluatePasswordStrength, friendlyProbeError, PROVIDERS_REQUIRING_ENDPOINT, PLAN_LABELS, REGION_LABELS } from './onboarding'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'
import { readFileSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname_onboarding = dirname(fileURLToPath(import.meta.url))

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
// Scenario: Onboarding probe → ProviderValidationBanner (Flow-A / MAJOR-4)
// Spec: provider-validation-centralization-spec.md, US8 / R-B / R-H/m2.
// =====================================================================
//
// When the probe returns success=true with a non-blocking validation outcome
// (no_credit / unreachable / restricted), the ProviderValidationBanner MUST
// render with the correct data-outcome and message, and the user MUST be able
// to proceed (Complete Setup not blocked).

describe('OnboardingWizard — probe banner (Flow-A / MAJOR-4)', () => {
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

  it('no_credit outcome → amber banner with wallet icon appears; user can still proceed', async () => {
    // Use empty models list so the free-text slug input is shown (allowFreeTextWhenEmpty
    // path in ModelSelector) — this lets us verify the user can proceed by entering a slug.
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      models: [],
      validation: {
        outcome: 'no_credit',
        message: 'Your Anthropic key works, but the account has no credit.',
      },
    })

    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    // Connected status appears (non-blocking — the probe succeeded).
    await waitFor(() => screen.getByText(/connected successfully/i))

    // The probe-validation banner must render.
    await waitFor(() => {
      expect(screen.getByTestId('onboarding-probe-validation-banner')).toBeInTheDocument()
    })
    expect(screen.getByTestId('onboarding-probe-validation-banner')).toHaveAttribute(
      'data-outcome',
      'no_credit',
    )
    // Server-provided copy must be shown.
    expect(
      screen.getByText('Your Anthropic key works, but the account has no credit.'),
    ).toBeInTheDocument()

    // User can still proceed — Complete Setup is not blocked (after entering a model slug).
    // With empty models the ModelSelector renders a free-text input.
    const modelInput = await waitFor(() => screen.getByPlaceholderText(/enter model slug/i))
    fireEvent.change(modelInput, { target: { value: 'claude-3-haiku' } })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /complete setup/i })).not.toBeDisabled()
    })
  })

  it('unreachable outcome → banner with unreachable outcome; user can proceed', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      models: [],
      validation: {
        outcome: 'unreachable',
        message: "Couldn't reach Anthropic to check the key.",
      },
    })

    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    await waitFor(() => screen.getByText(/connected successfully/i))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-probe-validation-banner')).toBeInTheDocument()
    })
    expect(screen.getByTestId('onboarding-probe-validation-banner')).toHaveAttribute(
      'data-outcome',
      'unreachable',
    )
  })

  it('restricted outcome → banner with restricted outcome; user can proceed', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      models: [],
      validation: {
        outcome: 'restricted',
        message: 'The request was blocked in your region.',
      },
    })

    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    await waitFor(() => screen.getByText(/connected successfully/i))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-probe-validation-banner')).toBeInTheDocument()
    })
    expect(screen.getByTestId('onboarding-probe-validation-banner')).toHaveAttribute(
      'data-outcome',
      'restricted',
    )
  })

  it('clean success (no validation) → no banner appears', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      models: ['claude-3-haiku'],
    })

    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    await waitFor(() => screen.getByText(/connected successfully/i))

    // No banner when the probe returned no validation (or outcome=valid).
    expect(screen.queryByTestId('onboarding-probe-validation-banner')).not.toBeInTheDocument()
  })

  it('banner is hidden when testStatus is not success (error path)', async () => {
    // Probe returns success=false (InvalidKey): error is shown, NOT the banner.
    vi.mocked(probeProvider).mockResolvedValue({
      success: false,
      error: 'The API key was rejected.',
    })

    await goToProviderAndSelect()
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))

    await waitFor(() => screen.getByTestId('onboarding-error'))

    // No probe-validation banner on error paths (testStatus=error, not success).
    expect(screen.queryByTestId('onboarding-probe-validation-banner')).not.toBeInTheDocument()
  })
})

// =====================================================================
// friendlyProbeError — pure unit tests
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

  it('maps "unknown provider" / "requires endpoint" to the needs-endpoint message', () => {
    for (const raw of [
      'unknown provider "azure"',
      'unknown provider azure',
      'requires an endpoint',
      'requires endpoint',
      'no endpoint configured',
    ]) {
      const msg = friendlyProbeError(raw, 'Azure OpenAI')
      expect(msg).toMatch(/Azure OpenAI needs a custom API endpoint/i)
      expect(msg).toMatch(/enter it below/i)
    }
  })

  it('maps upstream 400 / 404 to a region/endpoint check message', () => {
    for (const raw of [
      'upstream models: status 400',
      'status 404',
      'not found',
      'bad request',
    ]) {
      const msg = friendlyProbeError(raw, 'Google Gemini')
      expect(msg).toMatch(/rejected the request/i)
      expect(msg).toMatch(/check the endpoint/i)
    }
  })

  it('maps upstream 5xx to a server-issues message', () => {
    for (const raw of ['status 500', 'status 503', 'upstream models: status 502']) {
      const msg = friendlyProbeError(raw, 'Mistral')
      expect(msg).toMatch(/having server issues/i)
      expect(msg).toMatch(/try again shortly/i)
    }
  })

  it('does not misclassify 404/5xx as an auth error', () => {
    expect(friendlyProbeError('status 404', 'OpenAI')).not.toMatch(/rejected by OpenAI/i)
    expect(friendlyProbeError('status 500', 'OpenAI')).not.toMatch(/rejected by OpenAI/i)
  })

  it('needs-endpoint branch takes priority over auth branch (unknown-provider 401 edge)', () => {
    // A raw string with both "unknown provider" and "401" must hit the endpoint branch first.
    const msg = friendlyProbeError('unknown provider: status 401', 'Azure OpenAI')
    expect(msg).toMatch(/needs a custom API endpoint/i)
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
// PROVIDERS_REQUIRING_ENDPOINT — exported set
// =====================================================================

describe('PROVIDERS_REQUIRING_ENDPOINT', () => {
  it('includes azure and azure-openai', () => {
    expect(PROVIDERS_REQUIRING_ENDPOINT.has('azure')).toBe(true)
    expect(PROVIDERS_REQUIRING_ENDPOINT.has('azure-openai')).toBe(true)
  })

  it('does not include openai, anthropic, or other standard providers', () => {
    expect(PROVIDERS_REQUIRING_ENDPOINT.has('openai')).toBe(false)
    expect(PROVIDERS_REQUIRING_ENDPOINT.has('anthropic')).toBe(false)
    expect(PROVIDERS_REQUIRING_ENDPOINT.has('moonshot')).toBe(false)
  })
})

// =====================================================================
// Provider list — China/intl variants present in the UI
// =====================================================================

// =====================================================================
// Scenario: Grouped picker — company grid (L1)
// =====================================================================
//
// The new UI shows ONE tile per company (not per variant). Multi-variant
// companies (Moonshot/Kimi, MiniMax, Qwen, Zhipu/GLM, DeepSeek) each get
// one tile with a ▾ affordance. The old flat-variant button names are gone.

describe('OnboardingWizard — company grid (grouped picker)', () => {
  async function goToStep3() {
    await renderWizard()
    const username = screen.getByLabelText(/username/i)
    fireEvent.change(username, { target: { value: 'admin' } })
    fireEvent.click(screen.getByRole('button', { name: /continue/i }))
    await waitFor(() => screen.getByText(/set your password/i))
    const pw = 'password123'
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: pw } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: pw } })
    fireEvent.click(screen.getByRole('button', { name: /continue/i }))
    await waitFor(() => screen.getByText(/add a model key/i))
  }

  it('renders one tile per company (multi-variant companies collapsed into one)', async () => {
    await goToStep3()
    // Multi-variant companies show one tile each.
    expect(screen.getByRole('button', { name: /Moonshot \/ Kimi/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^MiniMax$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Zhipu \/ GLM/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Qwen \/ Alibaba/i })).toBeInTheDocument()
    // Old flat variant names should NOT appear as separate tiles.
    expect(screen.queryByRole('button', { name: /Moonshot \/ Kimi \(International\)/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /MiniMax \(International\)/i })).not.toBeInTheDocument()
  })

  it('shows Plan controls (standard-api/coding-plan) when Zhipu/GLM tile is clicked; no wire control at all', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /Zhipu \/ GLM/i }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: PLAN_LABELS['standard-api'] })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] })).toBeInTheDocument()
    })
    // Wire (OpenAI- vs Anthropic-compatible) is an internal config detail, not
    // shown in onboarding at all (provider-ux-fixes-plan FIX-5) — no
    // "Anthropic-compatible" text anywhere on the L2 panel.
    expect(screen.queryByText(/Anthropic-compatible/i)).not.toBeInTheDocument()
  })

  it('shows Region controls (intl/china) for Zhipu/GLM on api plan', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /Zhipu \/ GLM/i }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: REGION_LABELS.intl })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: REGION_LABELS.china })).toBeInTheDocument()
    })
  })

  it('resolves the correct id for Zhipu/GLM standard-api+intl: z-ai', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /Zhipu \/ GLM/i }))
    await waitFor(() => screen.getByRole('button', { name: PLAN_LABELS['standard-api'] }))
    // Plan defaults to standard-api, region defaults to intl → id should be z-ai
    // (the only catalog entry for this company+plan+region since wire merged).
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('z-ai', 'test-key', undefined)
    })
  })

  it('resolves the correct id for Zhipu/GLM coding-plan+china: zhipu-coding', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /Zhipu \/ GLM/i }))
    await waitFor(() => screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    fireEvent.click(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    await waitFor(() => screen.getByRole('button', { name: REGION_LABELS.china }))
    fireEvent.click(screen.getByRole('button', { name: REGION_LABELS.china }))
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('zhipu-coding', 'test-key', undefined)
    })
  })

  it('resolves the correct id for Qwen/Alibaba Coding Plan (single entry, no region split): coding-plan', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /Qwen \/ Alibaba/i }))
    await waitFor(() => screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    fireEvent.click(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    // The Coding Plan variant has no regional split — no Region control renders.
    expect(screen.queryByRole('group', { name: /select region/i })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('coding-plan', 'test-key', undefined)
    })
  })

  it('shows no Plan/Region for single-option OpenAI (one click → API key)', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^OpenAI$/i }))
    await waitFor(() => screen.getByLabelText('API Key'))
    // No plan selector should appear for single-option companies.
    expect(screen.queryByRole('button', { name: PLAN_LABELS['standard-api'] })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: PLAN_LABELS['coding-plan'] })).not.toBeInTheDocument()
  })

  it('search filters by alias: "kimi" shows Moonshot/Kimi tile', async () => {
    await goToStep3()
    fireEvent.change(screen.getByLabelText(/search providers/i), { target: { value: 'kimi' } })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Moonshot \/ Kimi/i })).toBeInTheDocument()
    })
    // Non-matching companies should be filtered out.
    expect(screen.queryByRole('button', { name: /^OpenAI$/i })).not.toBeInTheDocument()
  })

  it('search filters by alias: "glm" shows Zhipu/GLM tile', async () => {
    await goToStep3()
    fireEvent.change(screen.getByLabelText(/search providers/i), { target: { value: 'glm' } })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Zhipu \/ GLM/i })).toBeInTheDocument()
    })
    expect(screen.queryByRole('button', { name: /^Groq$/i })).not.toBeInTheDocument()
  })

  it('changing plan resets the loaded model list', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      models: ['model-a', 'model-b'],
    })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /Zhipu \/ GLM/i }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => screen.getByText(/connected successfully/i))
    // Now switch plan — model list should reset.
    fireEvent.click(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    await waitFor(() => {
      // The "connected successfully" message disappears when model list is reset.
      expect(screen.queryByText(/connected successfully/i)).not.toBeInTheDocument()
    })
  })

  it('DeepSeek is single-variant: no Plan/Region controls at all (one click straight to API key)', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /DeepSeek/i }))
    await waitFor(() => screen.getByLabelText('API Key'))
    // DeepSeek has exactly one catalog entry post wire-merge (standard-api, no
    // region) — it is NOT multi-variant, so no Plan/Region L2 panel renders.
    expect(screen.queryByRole('button', { name: PLAN_LABELS['standard-api'] })).not.toBeInTheDocument()
    expect(screen.queryByText(/Anthropic-compatible/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: REGION_LABELS.intl })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: REGION_LABELS.china })).not.toBeInTheDocument()
  })
})

// =====================================================================
// Azure endpoint field — required before Connect
// =====================================================================

describe('OnboardingWizard — azure endpoint field', () => {
  async function goToStep3() {
    await renderWizard()
    const username = screen.getByLabelText(/username/i)
    fireEvent.change(username, { target: { value: 'admin' } })
    fireEvent.click(screen.getByRole('button', { name: /continue/i }))
    await waitFor(() => screen.getByText(/set your password/i))
    const pw = 'password123'
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: pw } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: pw } })
    fireEvent.click(screen.getByRole('button', { name: /continue/i }))
    await waitFor(() => screen.getByText(/add a model key/i))
  }

  it('shows the endpoint field when Azure is selected', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /azure openai/i }))
    await waitFor(() => expect(screen.getByLabelText(/api endpoint/i)).toBeInTheDocument())
  })

  it('does not show the endpoint field for non-azure providers', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: 'Anthropic' }))
    await waitFor(() => screen.getByLabelText('API Key'))
    expect(screen.queryByLabelText(/api endpoint/i)).not.toBeInTheDocument()
  })

  it('Connect is disabled for Azure when endpoint is empty', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /azure openai/i }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), {
      target: { value: 'some-azure-key' },
    })
    // Endpoint is still empty — Connect must be disabled.
    const connectBtn = screen.getByRole('button', { name: /connect & load models/i })
    expect(connectBtn).toBeDisabled()
  })

  it('Connect is enabled for Azure when both key and endpoint are filled', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /azure openai/i }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), {
      target: { value: 'some-azure-key' },
    })
    fireEvent.change(screen.getByLabelText(/api endpoint/i), {
      target: { value: 'https://my-resource.openai.azure.com/openai/deployments/gpt4' },
    })
    const connectBtn = screen.getByRole('button', { name: /connect & load models/i })
    expect(connectBtn).not.toBeDisabled()
  })

  it('probe is called with the endpoint when Azure Connect is clicked', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /azure openai/i }))
    await waitFor(() => screen.getByLabelText('API Key'))
    fireEvent.change(screen.getByLabelText('API Key'), {
      target: { value: 'azure-key-123' },
    })
    const azureEndpoint = 'https://my-resource.openai.azure.com/openai/deployments/gpt4'
    fireEvent.change(screen.getByLabelText(/api endpoint/i), {
      target: { value: azureEndpoint },
    })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('azure', 'azure-key-123', azureEndpoint)
    })
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

// =====================================================================
// PROVIDER_CATALOG consistency (US-7 / FR-019) — onboarding consumes
// the shared catalog verbatim (labels, subtitles, logoSlug).
// The AVAILABLE_PROVIDERS ⊆ ProbeProviderRequest invariant has been
// re-homed to Go: TestCatalog_DriftGuard_IdInProbeEnum (already green).
// =====================================================================

describe('PROVIDER_CATALOG — onboarding sources catalog verbatim (US-7 / FR-019, FIX-4/5)', () => {
  it('PROVIDER_CATALOG is non-empty and has 23 entries (post FIX-5 wire-merge: 30 → 23)', () => {
    expect(PROVIDER_CATALOG.length).toBe(23)
  })

  it('every entry has required fields: id, company, label, subtitle, logoSlug, plan, wire', () => {
    for (const entry of PROVIDER_CATALOG) {
      expect(entry.id, `missing id on ${JSON.stringify(entry)}`).toBeTruthy()
      expect(entry.company, `missing company on ${entry.id}`).toBeTruthy()
      expect(entry.label, `missing label on ${entry.id}`).toBeTruthy()
      expect(entry.subtitle, `missing subtitle on ${entry.id}`).toBeTruthy()
      expect(entry.logoSlug, `missing logoSlug on ${entry.id}`).toBeTruthy()
      expect(['standard-api', 'coding-plan']).toContain(entry.plan)
      expect(['openai-compatible', 'anthropic']).toContain(entry.wire)
    }
  })

  it('catalog has entries for Zhipu/GLM: 2 plans × 2 regions = 4 entries (wire merged into anthropic_id)', () => {
    const entries = PROVIDER_CATALOG.filter((e) => e.company === 'Zhipu / GLM')
    expect(entries).toHaveLength(4)
  })

  it('catalog has entries for Moonshot/Kimi: 1 plan × 2 regions = 2 entries (wire merged into anthropic_id)', () => {
    const entries = PROVIDER_CATALOG.filter((e) => e.company === 'Moonshot / Kimi')
    expect(entries).toHaveLength(2)
  })

  it('catalog has entries for MiniMax: 1 plan × 2 regions = 2 entries (wire merged into anthropic_id)', () => {
    const entries = PROVIDER_CATALOG.filter((e) => e.company === 'MiniMax')
    expect(entries).toHaveLength(2)
  })

  it('catalog has entries for DeepSeek: 1 plan, no region = 1 entry (wire merged into anthropic_id)', () => {
    const entries = PROVIDER_CATALOG.filter((e) => e.company === 'DeepSeek')
    expect(entries).toHaveLength(1)
  })

  it('catalog has entries for Qwen/Alibaba: 3 api regions + 1 coding-plan = 4 entries', () => {
    const entries = PROVIDER_CATALOG.filter((e) => e.company === 'Qwen / Alibaba')
    expect(entries).toHaveLength(4)
  })

  it('the z-ai entry label and subtitle match catalog verbatim (US-7 consistency, FIX-4)', () => {
    const entry = PROVIDER_CATALOG.find((e) => e.id === 'z-ai')
    expect(entry).toBeDefined()
    expect(entry!.label).toBe('Zhipu / GLM (International)')
    expect(entry!.subtitle).toBe('Pay-as-you-go, per token · api.z.ai/api/paas/v4')
    expect(entry!.logoSlug).toBe('zhipu')
    expect(entry!.anthropic_id).toBe('z-ai-anthropic')
  })

  it('openai entry label and subtitle match catalog verbatim (FIX-4: no plan suffix for single-plan companies)', () => {
    const entry = PROVIDER_CATALOG.find((e) => e.id === 'openai')
    expect(entry).toBeDefined()
    expect(entry!.label).toBe('OpenAI')
    expect(entry!.logoSlug).toBe('openai')
  })

  // [SC-009] The SPA MUST consume the provider catalog via a static import, NOT
  // a live /providers/catalog HTTP fetch.  ADR-031 explicitly rejected a live
  // catalog endpoint (G-2=B: build-time embed, not a live endpoint).
  //
  // We verify this by:
  // (a) Confirming PROVIDER_CATALOG is a non-empty array (static import works).
  // (b) Reading the source files of onboarding.tsx and ProvidersSection.tsx and
  //     asserting they contain NO fetch('/providers/catalog') call.
  //
  // This is a source-scan, not a mock-level test — it verifies the property at
  // the code level, where a mock might hide a real fetch call.
  //
  // Traces to: connectors-providers-redesign-spec.md §7 SC-009; ADR-031 §"Out of scope".
  it('[SC-009] PROVIDER_CATALOG is a non-empty array consumed via static import', () => {
    // Proves the static import itself works at runtime.
    expect(Array.isArray(PROVIDER_CATALOG)).toBe(true)
    expect(PROVIDER_CATALOG.length).toBeGreaterThan(0)
  })

  it('[SC-009] onboarding.tsx source does not fetch /providers/catalog (static-only)', () => {
    // Read the source file and assert no live catalog fetch exists.
    const src = readFileSync(
      join(__dirname_onboarding, 'onboarding.tsx'),
      'utf-8',
    )
    expect(src, 'onboarding.tsx must not call a /providers/catalog endpoint').not.toContain(
      '/providers/catalog',
    )
  })

  it('[SC-009] ProvidersSection.tsx source does not fetch /providers/catalog (static-only)', () => {
    const src = readFileSync(
      join(__dirname_onboarding, '../components/settings/ProvidersSection.tsx'),
      'utf-8',
    )
    expect(src, 'ProvidersSection.tsx must not call a /providers/catalog endpoint').not.toContain(
      '/providers/catalog',
    )
  })
})
