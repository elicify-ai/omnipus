import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { PROVIDERS_CATALOG, CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import { catalogEndpointHint, catalogSubtitle } from '@/lib/catalogDisplay'

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
  createFileRoute: () => (opts: { component: React.ComponentType }) => opts,
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
    // The registry-fed catalog (ADR-068 FR-037) — the picker's only source.
    fetchProvidersCatalog: vi.fn(),
  }
})

// Mock SVG import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

import { configureProvider, probeProvider, completeOnboardingTransaction, fetchProvidersCatalog } from '@/lib/api'
import { evaluatePasswordStrength, friendlyProbeError, PROVIDERS_REQUIRING_ENDPOINT, PLAN_LABELS, REGION_LABELS } from './onboarding'
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
  // A fresh QueryClient per render so the catalog query never leaks a cached
  // document (or error) between tests.
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <WizardComponent />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
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
// companies (Moonshot AI, MiniMax, Alibaba Cloud, Zhipu AI) each get
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
    expect(screen.getByRole('button', { name: /^Moonshot AI$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^MiniMax$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Zhipu AI$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Alibaba Cloud$/i })).toBeInTheDocument()
    // Old flat variant names should NOT appear as separate tiles.
    expect(screen.queryByRole('button', { name: /Moonshot AI \(International\)/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /MiniMax \(International\)/i })).not.toBeInTheDocument()
  })

  it('shows Plan controls (standard-api/coding-plan) when Zhipu AI tile is clicked; no wire control at all', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Zhipu AI$/i }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: PLAN_LABELS['standard-api'] })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] })).toBeInTheDocument()
    })
    // Wire (OpenAI- vs Anthropic-compatible) is an internal config detail, not
    // shown in onboarding at all (provider-ux-fixes-plan FIX-5) — no
    // "Anthropic-compatible" text anywhere on the L2 panel.
    expect(screen.queryByText(/Anthropic-compatible/i)).not.toBeInTheDocument()
  })

  it('shows Region controls (intl/china) for Zhipu AI on api plan', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Zhipu AI$/i }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: REGION_LABELS.intl })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: REGION_LABELS.china })).toBeInTheDocument()
    })
  })

  it('resolves the correct id for Zhipu AI standard-api+intl: zai', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Zhipu AI$/i }))
    await waitFor(() => screen.getByRole('button', { name: PLAN_LABELS['standard-api'] }))
    // Plan defaults to standard-api, region defaults to intl → id should be zai
    // (the only catalog entry for this company+plan+region since wire merged).
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('zai', 'test-key', undefined)
    })
  })

  it('resolves the correct id for Zhipu AI coding-plan+china: zhipuai-coding-plan', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Zhipu AI$/i }))
    await waitFor(() => screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    fireEvent.click(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    await waitFor(() => screen.getByRole('button', { name: REGION_LABELS.china }))
    fireEvent.click(screen.getByRole('button', { name: REGION_LABELS.china }))
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('zhipuai-coding-plan', 'test-key', undefined)
    })
  })

  it('resolves the correct id for Alibaba Cloud Coding Plan (single entry, no region split): alibaba-coding-plan', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Alibaba Cloud$/i }))
    await waitFor(() => screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    fireEvent.click(screen.getByRole('button', { name: PLAN_LABELS['coding-plan'] }))
    // The Coding Plan variant has no regional split — no Region control renders.
    expect(screen.queryByRole('group', { name: /select region/i })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'test-key' } })
    fireEvent.click(screen.getByRole('button', { name: /connect & load models/i }))
    await waitFor(() => {
      expect(probeProvider).toHaveBeenCalledWith('alibaba-coding-plan', 'test-key', undefined)
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

  it('search filters by alias: "kimi" shows Moonshot AI tile', async () => {
    await goToStep3()
    fireEvent.change(screen.getByLabelText(/search providers/i), { target: { value: 'kimi' } })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^Moonshot AI$/i })).toBeInTheDocument()
    })
    // Non-matching companies should be filtered out.
    expect(screen.queryByRole('button', { name: /^OpenAI$/i })).not.toBeInTheDocument()
  })

  it('search filters by alias: "glm" shows Zhipu AI tile', async () => {
    await goToStep3()
    fireEvent.change(screen.getByLabelText(/search providers/i), { target: { value: 'glm' } })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^Zhipu AI$/i })).toBeInTheDocument()
    })
    expect(screen.queryByRole('button', { name: /^Groq$/i })).not.toBeInTheDocument()
  })

  it('changing plan resets the loaded model list', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      models: ['model-a', 'model-b'],
    })
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Zhipu AI$/i }))
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

  // Slash-palette silent-empty bugfix: completeOnboardingTransaction is the
  // FIRST point the omnipus-session cookie exists on a fresh install. The
  // ['commands'] query (useSlashMenu.ts / ChatScreen.tsx) is behind withAuth
  // and may have already 401'd — going permanently errored — from a
  // composer that mounted before this cookie was set. Nothing else ever
  // refetches it, so this transaction succeeding is the one point we KNOW
  // the session just became valid.
  it('invalidates the commands cache after completeOnboardingTransaction succeeds', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))

    await waitFor(() => {
      expect(completeOnboardingTransaction).toHaveBeenCalledOnce()
    })

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['commands'] }),
    )
  })

  it('does NOT invalidate the commands cache when completeOnboardingTransaction fails', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-error')).toBeInTheDocument()
    })

    expect(invalidateSpy).not.toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['commands'] }),
    )
  })

  // Same failure, same moment, the cache entry that actually decides where the
  // user lands. ['workspaces'] is read by DefaultWorkspaceRedirect to pick the
  // landing workspace and is shared with Sidebar's 30s poll, so an observer
  // that mounted before the cookie existed holds a stale or errored entry and
  // the landing decision is made from it. The ['commands'] invalidation above
  // was added for exactly this class and was never generalised to this key.
  it('invalidates the workspaces cache after completeOnboardingTransaction succeeds', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))

    await waitFor(() => {
      expect(completeOnboardingTransaction).toHaveBeenCalledOnce()
    })

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['workspaces'] }),
    )
  })

  it('does NOT invalidate the workspaces cache when completeOnboardingTransaction fails', async () => {
    vi.mocked(configureProvider).mockResolvedValue({} as never)
    vi.mocked(probeProvider).mockResolvedValue({ success: true })
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /complete setup/i }))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-error')).toBeInTheDocument()
    })

    expect(invalidateSpy).not.toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['workspaces'] }),
    )
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
// Catalog source (ADR-068 FR-037 / T068-05) — the SPA reads the catalog
// from GET /providers/catalog, not a bundle. Grep half of the BDD
// scenario "SPA reads the catalog from the GET, not a bundle" (the network
// half lands with T068-31's Playwright spec). The old SC-009 "static-only"
// guard asserted the opposite invariant and is retired with the bundle.
// =====================================================================

describe('Providers catalog — onboarding reads GET /providers/catalog, never a bundle (FR-037)', () => {
  async function goToStep3() {
    await renderWizard()
    await advanceNameToPassword()
    await advancePasswordToModelKey()
  }

  // T068-18: both screens now reach the catalog through the shared
  // ETag-re-validating query policy (providersCatalogQuery.ts →
  // fetchProvidersCatalog) instead of naming the fetcher inline. The
  // still-valid half of the original assertion — no bundled catalog import —
  // is preserved verbatim; the sourcing half is asserted on the import that
  // actually carries the call, which a stray comment cannot satisfy.
  it('[SC-010] onboarding.tsx sources the catalog through the providers-catalog query policy and imports no bundle', () => {
    const src = readFileSync(join(__dirname_onboarding, 'onboarding.tsx'), 'utf-8')
    expect(src).toContain("from '@/lib/providersCatalogQuery'")
    expect(src).toContain('providersCatalogQueryOptions()')
    expect(src).not.toContain("from '@/lib/generated/")
  })

  it('[SC-010] ProvidersSection.tsx sources the catalog through the providers-catalog query policy and imports no bundle', () => {
    const src = readFileSync(join(__dirname_onboarding, '../components/settings/ProvidersSection.tsx'), 'utf-8')
    expect(src).toContain("from '@/lib/providersCatalogQuery'")
    expect(src).toContain('providersCatalogQueryOptions()')
    expect(src).not.toContain("from '@/lib/generated/")
  })

  it('the wizard requests the catalog from the API on step 3', async () => {
    await goToStep3()
    expect(fetchProvidersCatalog).toHaveBeenCalled()
  })

  it('the selected entry renders the subtitle and endpoint derived from the fetched document (US-7 parity)', async () => {
    await goToStep3()
    fireEvent.click(screen.getByRole('button', { name: /^Zhipu AI$/i }))
    const entry = CATALOG_PROVIDERS.find((e) => e.id === 'zai')!
    await waitFor(() => {
      expect(screen.getByText(catalogSubtitle(entry))).toBeInTheDocument()
    })
    // Pinned verbatim — the same strings onboarding-settings-parity.test.tsx pins.
    expect(catalogSubtitle(entry)).toBe('Pay-as-you-go, per token · api.z.ai/api/paas/v4')
    expect(screen.getByText(`→ ${catalogEndpointHint(entry)}`)).toBeInTheDocument()
  })

  it('a catalog fetch failure shows "Provider catalog unavailable" with a Retry that refetches', async () => {
    vi.mocked(fetchProvidersCatalog).mockRejectedValueOnce(new Error('503'))
    await goToStep3()
    await waitFor(() => screen.getByTestId('catalog-error'))
    expect(screen.queryByRole('button', { name: /^OpenAI$/i })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^OpenAI$/i })).toBeInTheDocument()
    })
    expect(screen.queryByTestId('catalog-error')).not.toBeInTheDocument()
  })
})
