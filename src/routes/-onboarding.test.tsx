import { describe, it, expect, vi, beforeEach, beforeAll, afterAll } from 'vitest'
import React from 'react'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { PROVIDERS_CATALOG, CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import { catalogEndpointHint, catalogSubtitle } from '@/lib/catalogDisplay'

// Wave 5b spec tests — OnboardingWizard frontend tests
// Traces to: wave5b-system-agent-spec.md — Onboarding Flow BDD scenarios
// Re-based on ADR-068 T068-24: step 3 is the SHARED ProviderPicker +
// ProviderDetailPanel (FR-021), the model field starts empty and carries the
// FR-029 label, and *Finish* is gated on a probe of the CHOSEN auth method for
// the CHOSEN model.
//
// FR-12.3 flow (3 numbered steps + unnumbered completion screen):
//   Step 1 — "What should I call you?" (name/username)
//   Step 2 — "Set your password" (password + confirm)
//   Step 3 — "Add a model key" (provider + auth method + model)
//   Completion — "Meet your Assistant" (Mia intro, Start chatting)
// The step indicator tracks the 3 numbered steps only; the completion screen
// is not a numbered step, so aria-valuemax is 3 — FR-028's "onboarding stays
// three steps" is asserted against that same indicator.

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

// ModelSelector renders its list inside a Radix Popover portal. Render it
// inline instead, so the model rows are reachable without a real portal — the
// same seam model-selector.test.tsx uses.
vi.mock('@/components/ui/popover', () => {
  return {
    Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
    PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) => {
      if (asChild && React.isValidElement(children)) return children
      return React.createElement('div', null, children)
    },
    PopoverContent: ({ children }: { children: React.ReactNode }) =>
      React.createElement('div', { 'data-testid': 'popover-content' }, children),
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
    fetchProviders: vi.fn().mockResolvedValue([]),
    // The registry-fed catalog (ADR-068 FR-037) — the picker's only source.
    fetchProvidersCatalog: vi.fn(),
  }
})

// Mock SVG import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

import { probeProvider, completeOnboardingTransaction, fetchProvidersCatalog } from '@/lib/api'
import { evaluatePasswordStrength, friendlyProbeError, PROVIDERS_REQUIRING_ENDPOINT, ONBOARDING_MODEL_LABEL } from './onboarding'
import { LOCAL_PROVIDER_CREDENTIAL } from '@/components/providers/ProviderDetailPanel'
import type { ProbeProviderRequest } from '@/lib/api/generated/openapi-types'
import { readFileSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname_onboarding = dirname(fileURLToPath(import.meta.url))

// ── jsdom seams the picker and the model list both need ────────────────────
// cmdk needs ResizeObserver + scrollIntoView; @tanstack/react-virtual sizes its
// window from offsetHeight, which jsdom hard-codes to 0 — a zero-height window
// renders zero rows and every row assertion would pass vacuously.
let restoreOffsetHeight: (() => void) | undefined

beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
  const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      // The picker's virtual viewport is the 480 px window SC-005 is stated
      // against; cmdk's own list (the model selector) caps at 300 px.
      if (this.getAttribute('data-testid') === 'picker-virtual-viewport') return 480
      if (this.hasAttribute('cmdk-list')) return 300
      return 0
    },
  })
  restoreOffsetHeight = () => {
    if (original) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', original)
    else delete (HTMLElement.prototype as unknown as Record<string, unknown>).offsetHeight
  }
})

afterAll(() => {
  restoreOffsetHeight?.()
})

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

async function goToStep3() {
  await renderWizard()
  await advanceNameToPassword()
  await advancePasswordToModelKey()
  await waitFor(() => screen.getByTestId('onboarding-provider-picker'))
}

/** Open the second-level panel for a Popular tile (no virtual list involved). */
async function openPanelForTile(providerId: string) {
  fireEvent.click(screen.getByTestId(`picker-popular-${providerId}`))
  await waitFor(() => screen.getByTestId('provider-detail-panel'))
}

/** Open the second-level panel for a company reachable only through search. */
async function openPanelForCompany(company: string, query: string) {
  fireEvent.change(screen.getByTestId('picker-search'), { target: { value: query } })
  await waitFor(() => screen.getByTestId(`picker-row-${company}`))
  fireEvent.click(screen.getByTestId(`picker-row-${company}`))
  await waitFor(() => screen.getByTestId('provider-detail-panel'))
}

/** Type an API key into the panel's own key field and confirm the panel. */
async function confirmPanelWithKey(apiKey: string) {
  fireEvent.change(screen.getByTestId('provider-detail-panel-api-key-input'), {
    target: { value: apiKey },
  })
  fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
  await waitFor(() => screen.getByTestId('onboarding-provider-summary'))
}

/** The last ProbeProviderRequest the wizard sent. */
function lastProbeRequest(): ProbeProviderRequest {
  const calls = vi.mocked(probeProvider).mock.calls
  return calls[calls.length - 1]![0] as ProbeProviderRequest
}

/** Pick a model row from the (inline-rendered) model selector. */
async function pickModel(modelId: string) {
  fireEvent.click(screen.getByTestId('onboarding-model-select'))
  const option = await waitFor(() => screen.getByTestId(`onboarding-model-${modelId}`))
  fireEvent.click(option)
}

const finishButton = () => screen.getByRole('button', { name: /finish|retry setup/i })

// Helper: from step 3, connect Anthropic with a key and a probed model —
// leaves the wizard on step 3 with Finish enabled.
const ANTHROPIC_MODEL = 'claude-sonnet-4-5'

async function connectProviderOnStep3() {
  vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: ANTHROPIC_MODEL })
  await openPanelForTile('anthropic')
  await confirmPanelWithKey('sk-ant-api03-test')
  await pickModel(ANTHROPIC_MODEL)
  await waitFor(() => expect(finishButton()).not.toBeDisabled())
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
// =====================================================================
// ADR-068 T068-24 — step 3 is the SHARED picker (FR-021), the auth-method
// control lives in its second-level panel (FR-028), and the model field is
// empty, labelled, and probe-gated (FR-029).
//
// Coverage note (replacement, not deletion): the "company grid (grouped
// picker)" and "azure endpoint field" describes this file used to carry
// exercised the bespoke L1/L2 grid that FR-021 deletes. Their still-valid
// invariants moved with the UI they belong to — company grouping, plan and
// region resolution, alias search and the unsupported-row behaviour are
// asserted in ProviderPicker.test.tsx / ProviderDetailPanel.test.tsx /
// provider-picker-search.test.ts against the same 190-entry fixture — and the
// Azure "needs an endpoint" invariant is preserved below (the constant's unit
// test, plus the onboarding hint that routes the operator to Custom endpoint).
// =====================================================================

describe('OnboardingWizard — step 3 renders the shared provider picker (FR-021)', () => {
  it('the source no longer carries its own company grid (no PRIORITY_COMPANIES)', () => {
    const src = readFileSync(join(__dirname_onboarding, 'onboarding.tsx'), 'utf-8')
    expect(src).not.toContain('PRIORITY_COMPANIES')
    expect(src).toContain("from '@/components/providers/ProviderPicker'")
  })

  it('step 3 mounts ProviderPicker with the fetched catalog (8 Popular tiles)', async () => {
    await goToStep3()
    const popular = screen.getByTestId('picker-popular')
    expect(within(popular).getAllByRole('button')).toHaveLength(8)
    expect(screen.getByTestId('picker-popular-anthropic')).toBeInTheDocument()
  })

  it('choosing a company opens the second-level panel without leaving step 3 (FR-028)', async () => {
    await goToStep3()
    await openPanelForTile('anthropic')

    // The step tracker still shows exactly 3 steps, on step 3.
    const progressbar = screen.getByRole('progressbar')
    expect(progressbar).toHaveAttribute('aria-valuemax', '3')
    expect(progressbar).toHaveAttribute('aria-valuenow', '3')
    expect(screen.getAllByText('Step 3 of 3').length).toBeGreaterThan(0)
  })

  it('switching the auth segment to API key reveals the key field and hides the sign-in radios', async () => {
    await goToStep3()
    // GitHub is the fixture's company that offers BOTH methods (github-copilot
    // sign-in beside the github-models key row), so it is where the segmented
    // control exists at all.
    await openPanelForCompany('GitHub', 'github')

    // Sign-in is pre-selected where offered (FR-005).
    expect(screen.getByTestId('provider-detail-panel-auth-segment-sign_in')).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(screen.getByTestId('provider-detail-panel-auth-signin')).toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-api-key-input')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('provider-detail-panel-auth-segment-api_key'))

    expect(screen.getByTestId('provider-detail-panel-api-key-input')).toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-auth-signin')).not.toBeInTheDocument()
    // Still three steps — the whole point of FR-028.
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuemax', '3')
  })

  it('the confirmed row renders the subtitle and endpoint derived from the fetched document (US-7 parity)', async () => {
    await goToStep3()
    await openPanelForCompany('Zhipu AI', 'glm')
    await confirmPanelWithKey('sk-zai-test')

    const entry = CATALOG_PROVIDERS.find((e) => e.id === 'zai')!
    const summary = screen.getByTestId('onboarding-provider-summary')
    expect(within(summary).getByText(catalogSubtitle(entry))).toBeInTheDocument()
    // Pinned verbatim — the same strings onboarding-settings-parity.test.tsx pins.
    expect(catalogSubtitle(entry)).toBe('Pay-as-you-go, per token · api.z.ai/api/paas/v4')
    expect(within(summary).getByText(`→ ${catalogEndpointHint(entry)}`)).toBeInTheDocument()
  })

  it('Change reopens the picker and drops the confirmed row', async () => {
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')

    fireEvent.click(within(screen.getByTestId('onboarding-provider-summary')).getByRole('button', { name: /change/i }))

    await waitFor(() => screen.getByTestId('onboarding-provider-picker'))
    expect(screen.queryByTestId('onboarding-provider-summary')).not.toBeInTheDocument()
  })

  it('never probes the key path with an empty key (error prevention)', async () => {
    await goToStep3()
    await openPanelForTile('anthropic')
    // Confirm with NO key typed.
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    await waitFor(() => screen.getByTestId('onboarding-provider-summary'))

    expect(screen.getByTestId('onboarding-probe-button')).toBeDisabled()
    expect(screen.getByTestId('onboarding-key-missing')).toBeInTheDocument()

    await pickModel(ANTHROPIC_MODEL)
    expect(probeProvider).not.toHaveBeenCalled()
    expect(finishButton()).toBeDisabled()
  })

  it('a provider with no fixed default base points the operator at Custom endpoint', async () => {
    await goToStep3()
    await openPanelForCompany('Azure OpenAI', 'azure')
    await confirmPanelWithKey('azure-key-123')

    expect(screen.getByTestId('onboarding-needs-endpoint')).toBeInTheDocument()
    expect(screen.getByTestId('onboarding-probe-button')).toBeDisabled()
  })
})

// =====================================================================
// Scenario: a `locality: local` provider (Ollama) needs no credential
// (ADR-067 FR-039). UAT-confirmed blocker: Ollama's catalog row declares
// `auth_methods: ["api_key"]` (the enum has no "none"), and before this fix
// onboarding therefore forced the operator to invent a fake key to clear the
// Finish gate — or blocked them outright. The fixture's `ollama` row carries
// `locality: "local"` (src/test/fixtures/providers-catalog.json).
// =====================================================================

describe('OnboardingWizard — local provider needs no credential (FR-039)', () => {
  it('Ollama shows no API-key field and no "Add an API key" gate', async () => {
    await goToStep3()
    await openPanelForCompany('Ollama', 'ollama')

    // No key input anywhere in the panel — a local endpoint has nothing to type.
    expect(screen.queryByTestId('provider-detail-panel-api-key-input')).not.toBeInTheDocument()
    expect(screen.getByTestId('provider-detail-panel-no-key-needed')).toBeInTheDocument()

    // Confirm with nothing typed — the exact interaction the UAT screenshot
    // shows as broken (Finish stuck disabled behind a fabricated-key demand).
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    await waitFor(() => screen.getByTestId('onboarding-provider-summary'))

    expect(screen.queryByTestId('onboarding-key-missing')).not.toBeInTheDocument()
    expect(screen.getByTestId('onboarding-probe-button')).not.toBeDisabled()

    // Subtitle is corrected too — a local endpoint is never "Pay-as-you-go".
    const entry = CATALOG_PROVIDERS.find((e) => e.id === 'ollama')!
    expect(entry.locality).toBe('local')
    const summary = screen.getByTestId('onboarding-provider-summary')
    expect(within(summary).queryByText(/Pay-as-you-go/)).not.toBeInTheDocument()
    expect(within(summary).getByText(catalogSubtitle(entry))).toBeInTheDocument()
    expect(catalogSubtitle(entry)).toMatch(/^Local —/)
  })

  it('completes the probe and enables Finish with no operator-supplied key', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: 'ollama-default' })
    await goToStep3()
    await openPanelForCompany('Ollama', 'ollama')
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    await waitFor(() => screen.getByTestId('onboarding-provider-summary'))

    await pickModel('ollama-default')
    await waitFor(() => expect(probeProvider).toHaveBeenCalled())

    // The gateway's api_key contract still requires a non-empty string
    // (pkg/gateway/rest_onboarding.go: "api_key is required") — the SPA
    // supplies the fixed, non-secret placeholder rather than making the
    // operator invent one.
    const req = lastProbeRequest()
    expect(req.auth).toBe('api_key')
    expect(req.api_key).toBe(LOCAL_PROVIDER_CREDENTIAL)

    await waitFor(() => expect(finishButton()).not.toBeDisabled())
  })
})

// =====================================================================
// Scenario: Onboarding model field is empty and labelled (FR-029)
// Scenario: Fresh install seeds no default model (step-3 half)
// =====================================================================

describe('OnboardingWizard — model field (FR-029)', () => {
  it('renders the model field with the verbatim label, no value, and Finish disabled', async () => {
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')

    expect(ONBOARDING_MODEL_LABEL).toBe('Model for your first agent')
    const trigger = screen.getByTestId('onboarding-model-select')
    // With no value the accessible name is the label verbatim.
    expect(trigger).toHaveAttribute('aria-label', ONBOARDING_MODEL_LABEL)
    // No model is chosen — no row carries the operator's pick.
    expect(document.querySelector('[data-chosen]')).toBeNull()
    expect(finishButton()).toBeDisabled()
    // Nothing was probed before a model existed.
    expect(probeProvider).not.toHaveBeenCalled()
  })

  it('choosing a model probes the api_key method with that exact model, then enables Finish', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: ANTHROPIC_MODEL })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')

    await pickModel(ANTHROPIC_MODEL)

    await waitFor(() => expect(probeProvider).toHaveBeenCalled())
    expect(lastProbeRequest()).toEqual({
      id: 'anthropic',
      auth: 'api_key',
      api_key: 'sk-ant-api03-test',
      model: ANTHROPIC_MODEL,
    })
    await waitFor(() => expect(finishButton()).not.toBeDisabled())
  })

  it('changing the model re-probes and disables Finish until the new probe passes', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: ANTHROPIC_MODEL })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')
    await pickModel(ANTHROPIC_MODEL)
    await waitFor(() => expect(finishButton()).not.toBeDisabled())

    // Second model: the probe is deliberately held open, so the assertion is
    // about the state BETWEEN the change and the new result — the window in
    // which a stale pass would otherwise still be enabling Finish.
    let resolveSecond: ((value: { success: boolean; probed_model?: string }) => void) | undefined
    vi.mocked(probeProvider).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSecond = resolve as never
        }) as never,
    )

    const secondModel = 'claude-opus-4-1'
    await pickModel(secondModel)

    await waitFor(() => expect(vi.mocked(probeProvider).mock.calls.length).toBe(2))
    expect(lastProbeRequest().model).toBe(secondModel)
    expect(finishButton()).toBeDisabled()

    resolveSecond!({ success: true, probed_model: secondModel })
    await waitFor(() => expect(finishButton()).not.toBeDisabled())
  })

  it('a probe that passed for a DIFFERENT model never enables Finish', async () => {
    // The gateway falls through to another model on model_not_found (FR-036)
    // and reports what it actually exercised. That is not a pass for the pick
    // on screen, so Finish must stay disabled.
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: 'claude-opus-4-1' })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')

    await pickModel(ANTHROPIC_MODEL)

    await waitFor(() => expect(probeProvider).toHaveBeenCalled())
    expect(finishButton()).toBeDisabled()
  })

  it('a failed probe surfaces the friendly error and leaves Finish disabled', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'upstream models: status 401' })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')

    await pickModel(ANTHROPIC_MODEL)

    await waitFor(() => screen.getByTestId('onboarding-error'))
    expect(screen.getByText(/rejected by Anthropic/i)).toBeInTheDocument()
    expect(finishButton()).toBeDisabled()
  })
})

// =====================================================================
// Scenario: Onboarding complete with sign-in (client body) — FR-036 / CRIT-002
// =====================================================================

describe('OnboardingWizard — sign-in path', () => {
  const CODEX_MODEL = CATALOG_PROVIDERS.find((e) => e.id === 'codex-cli')!.models![0]!.id

  async function chooseCodexCli() {
    await goToStep3()
    await openPanelForCompany('Codex CLI', 'codex')
    // Sign-in only: no segment, no key field, sign-in pre-selected (FR-005).
    expect(screen.queryByTestId('provider-detail-panel-auth-segment')).not.toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-api-key-input')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    await waitFor(() => screen.getByTestId('onboarding-provider-summary'))
  }

  it('Check sign-in probes auth: sign_in with the chosen model and NO api_key', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: CODEX_MODEL })
    await chooseCodexCli()
    await pickModel(CODEX_MODEL)
    await waitFor(() => expect(probeProvider).toHaveBeenCalled())

    const req = lastProbeRequest()
    expect(req.id).toBe('codex-cli')
    expect(req.auth).toBe('sign_in')
    expect(req.model).toBe(CODEX_MODEL)
    expect(Object.prototype.hasOwnProperty.call(req, 'api_key')).toBe(false)
  })

  it('the check button is labelled Check sign-in on the sign-in path', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'not signed in' })
    await chooseCodexCli()
    expect(screen.getByTestId('onboarding-probe-button')).toHaveTextContent('Check sign-in')

    fireEvent.click(screen.getByTestId('onboarding-probe-button'))
    await waitFor(() => expect(probeProvider).toHaveBeenCalled())
    expect(lastProbeRequest().auth).toBe('sign_in')
  })

  it('a missing CLI binary reads as "codex not found on this machine", not as a key error', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: false,
      error: 'exec: "codex": executable file not found in $PATH',
    })
    await chooseCodexCli()
    fireEvent.click(screen.getByTestId('onboarding-probe-button'))

    await waitFor(() => screen.getByTestId('onboarding-cli-missing'))
    expect(screen.getByTestId('onboarding-cli-missing').textContent).toContain(
      'codex not found on this machine',
    )
  })

  it('completion sends the OnboardingProviderSignIn variant — auth_method sign_in, no api_key', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: CODEX_MODEL })
    await chooseCodexCli()
    await pickModel(CODEX_MODEL)
    await waitFor(() => expect(finishButton()).not.toBeDisabled())

    fireEvent.click(finishButton())
    await waitFor(() => expect(completeOnboardingTransaction).toHaveBeenCalledOnce())

    const body = vi.mocked(completeOnboardingTransaction).mock.calls[0]![0]
    expect(body.provider).toEqual({
      auth_method: 'sign_in',
      id: 'codex-cli',
      model: CODEX_MODEL,
    })
    expect(Object.prototype.hasOwnProperty.call(body.provider, 'api_key')).toBe(false)
  })

  it('completion sends the OnboardingProviderApiKey variant on the key path', async () => {
    await goToStep3()
    await connectProviderOnStep3()

    fireEvent.click(finishButton())
    await waitFor(() => expect(completeOnboardingTransaction).toHaveBeenCalledOnce())

    const body = vi.mocked(completeOnboardingTransaction).mock.calls[0]![0]
    expect(body.provider).toEqual({
      auth_method: 'api_key',
      id: 'anthropic',
      api_key: 'sk-ant-api03-test',
      model: ANTHROPIC_MODEL,
    })
  })
})

// =====================================================================
// Scenario: Catalog unavailable in the picker (FR-037) — onboarding still
// proceeds through Custom endpoint.
// =====================================================================

describe('OnboardingWizard — catalog unavailable', () => {
  it('shows the picker error state with Retry, and Custom endpoint stays selectable', async () => {
    vi.mocked(fetchProvidersCatalog).mockRejectedValueOnce(new Error('503'))
    await goToStep3()

    await waitFor(() => screen.getByTestId('picker-catalog-error'))
    expect(screen.queryByTestId('picker-popular-openai')).not.toBeInTheDocument()
    expect(screen.getByTestId('picker-custom-endpoint')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('picker-catalog-retry'))
    await waitFor(() => expect(screen.getByTestId('picker-popular-openai')).toBeInTheDocument())
    expect(screen.queryByTestId('picker-catalog-error')).not.toBeInTheDocument()
  })

  it('onboarding completes through Custom endpoint while the catalog is down', async () => {
    vi.mocked(fetchProvidersCatalog).mockRejectedValue(new Error('503'))
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: 'my-model' })
    await goToStep3()
    await waitFor(() => screen.getByTestId('picker-catalog-error'))

    fireEvent.click(screen.getByTestId('picker-custom-endpoint'))
    await waitFor(() => screen.getByTestId('custom-endpoint-id'))
    fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: 'my-proxy' } })
    fireEvent.change(screen.getByTestId('custom-endpoint-api-base'), {
      target: { value: 'https://proxy.example.com/v1' },
    })
    fireEvent.change(screen.getByTestId('custom-endpoint-api-key'), { target: { value: 'sk-proxy' } })
    fireEvent.click(screen.getByTestId('custom-endpoint-submit'))

    await waitFor(() => screen.getByTestId('onboarding-provider-summary'))

    // No catalog listing for a custom row — the slug is typed, then checked.
    fireEvent.change(screen.getByTestId('onboarding-model-select'), { target: { value: 'my-model' } })
    expect(probeProvider).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('onboarding-probe-button'))

    await waitFor(() => expect(probeProvider).toHaveBeenCalled())
    expect(lastProbeRequest()).toEqual({
      id: 'my-proxy',
      auth: 'api_key',
      api_key: 'sk-proxy',
      model: 'my-model',
      api_base: 'https://proxy.example.com/v1',
      protocol: 'openai-compatible',
    })

    await waitFor(() => expect(finishButton()).not.toBeDisabled())
    fireEvent.click(finishButton())
    await waitFor(() => expect(completeOnboardingTransaction).toHaveBeenCalledOnce())
    expect(vi.mocked(completeOnboardingTransaction).mock.calls[0]![0].provider).toEqual({
      auth_method: 'api_key',
      id: 'my-proxy',
      api_key: 'sk-proxy',
      model: 'my-model',
      endpoint: 'https://proxy.example.com/v1',
    })
  })
})

// =====================================================================
// Scenario: probe validation banner (Flow-A / MAJOR-4) — non-blocking
// warnings still let the user finish.
// =====================================================================

describe('OnboardingWizard — probe banner (Flow-A / MAJOR-4)', () => {
  async function probeWith(outcome: 'no_credit' | 'unreachable' | 'restricted', message: string) {
    vi.mocked(probeProvider).mockResolvedValue({
      success: true,
      probed_model: ANTHROPIC_MODEL,
      validation: { outcome, message },
    })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')
    await pickModel(ANTHROPIC_MODEL)
    await waitFor(() => screen.getByTestId('onboarding-probe-validation-banner'))
  }

  it('no_credit outcome → banner appears; the user can still finish', async () => {
    await probeWith('no_credit', 'Your key works, but the account has no credit.')
    expect(screen.getByTestId('onboarding-probe-validation-banner').textContent).toMatch(/no credit/i)
    expect(finishButton()).not.toBeDisabled()
  })

  it('unreachable outcome → banner appears; the user can still finish', async () => {
    await probeWith('unreachable', 'Could not reach the provider to validate.')
    expect(screen.getByTestId('onboarding-probe-validation-banner')).toBeInTheDocument()
    expect(finishButton()).not.toBeDisabled()
  })

  it('restricted outcome → banner appears; the user can still finish', async () => {
    await probeWith('restricted', 'This key is restricted.')
    expect(screen.getByTestId('onboarding-probe-validation-banner')).toBeInTheDocument()
    expect(finishButton()).not.toBeDisabled()
  })

  it('clean success (no validation) → no banner appears', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: ANTHROPIC_MODEL })
    await goToStep3()
    await connectProviderOnStep3()
    expect(screen.queryByTestId('onboarding-probe-validation-banner')).not.toBeInTheDocument()
  })

  it('the banner is absent while the probe is failing', async () => {
    vi.mocked(probeProvider).mockResolvedValue({
      success: false,
      error: 'status 401',
      validation: { outcome: 'no_credit', message: 'no credit' },
    })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-api03-test')
    await pickModel(ANTHROPIC_MODEL)
    await waitFor(() => screen.getByTestId('onboarding-error'))
    expect(screen.queryByTestId('onboarding-probe-validation-banner')).not.toBeInTheDocument()
  })
})

// =====================================================================
// Scenario: friendly probe error display (the error the probe surfaces)
// =====================================================================

describe('OnboardingWizard — friendly probe error display', () => {
  async function failProbe(rawError: string) {
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: rawError })
    await goToStep3()
    await openPanelForTile('anthropic')
    await confirmPanelWithKey('sk-ant-test')
    await pickModel(ANTHROPIC_MODEL)
    await waitFor(() => screen.getByTestId('onboarding-error'))
  }

  it('renders a friendly message (not the raw upstream string) as the primary error', async () => {
    await failProbe('upstream models: status 401')
    expect(screen.getByText(/rejected by Anthropic/i)).toBeInTheDocument()
  })

  it('keeps the raw upstream string available behind a Technical details disclosure', async () => {
    await failProbe('upstream models: status 401')
    expect(screen.getByText(/technical details/i)).toBeInTheDocument()
    expect(screen.getByText('upstream models: status 401')).toBeInTheDocument()
  })

  it('the probe error container is a live region announced to screen readers', async () => {
    await failProbe('status 429')
    const alert = screen.getByTestId('onboarding-error')
    expect(alert).toHaveAttribute('role', 'alert')
    expect(alert).toHaveAttribute('aria-live', 'assertive')
    expect(alert.textContent).toMatch(/error:/i)
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
// Scenario: Finish onboarding — Finish → Meet your Assistant
// =====================================================================

describe('OnboardingWizard — finish', () => {
  // Helper: walk through the 3 numbered steps to leave the wizard on step 3
  // with Finish enabled (provider confirmed, model chosen, probe passed).
  async function goToCompleteReady() {
    await goToStep3()
    await connectProviderOnStep3()
  }

  it('completing calls completeOnboardingTransaction and reveals Meet your Assistant', async () => {

    await goToCompleteReady()

    fireEvent.click(screen.getByRole('button', { name: /finish/i }))

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
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))

    await waitFor(() => {
      expect(completeOnboardingTransaction).toHaveBeenCalledOnce()
    })

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['commands'] }),
    )
  })

  it('does NOT invalidate the commands cache when completeOnboardingTransaction fails', async () => {
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))

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
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))

    await waitFor(() => {
      expect(completeOnboardingTransaction).toHaveBeenCalledOnce()
    })

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['workspaces'] }),
    )
  })

  it('does NOT invalidate the workspaces cache when completeOnboardingTransaction fails', async () => {
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))

    await waitFor(() => {
      expect(screen.getByTestId('onboarding-error')).toBeInTheDocument()
    })

    expect(invalidateSpy).not.toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['workspaces'] }),
    )
  })

  it('Start chatting on Meet your Assistant navigates to root', async () => {

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))
    await waitFor(() => screen.getByRole('button', { name: /start chatting/i }))

    fireEvent.click(screen.getByRole('button', { name: /start chatting/i }))

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    })
  })

  it('Meet your Assistant shows Mia with the default-star badge and her role', async () => {

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))
    await waitFor(() => screen.getByText(/Mia — Assistant/i))

    // Default-agent star badge is present.
    expect(screen.getByLabelText(/default agent/i)).toBeInTheDocument()
    // Role line.
    expect(screen.getByText(/memory-rich, cross-workspace recall/i)).toBeInTheDocument()
    // Intro line referencing My Workspace + default agent.
    expect(screen.getByText(/bound to my workspace/i)).toBeInTheDocument()
  })

  it('surfaces an inline error and stays on step 3 with Retry setup when finish fails', async () => {
    vi.mocked(completeOnboardingTransaction).mockRejectedValueOnce(new Error('server exploded'))

    await goToCompleteReady()
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))

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
    vi.mocked(completeOnboardingTransaction)
      .mockRejectedValueOnce(new Error('transient outage'))
      .mockResolvedValueOnce({ token: 't', role: 'admin', username: 'admin' } as never)

    await goToCompleteReady()

    // First attempt fails → inline error + Retry Setup CTA.
    fireEvent.click(screen.getByRole('button', { name: /finish/i }))
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
// =====================================================================
// Catalog source (ADR-068 FR-037 / T068-05) — the SPA reads the catalog
// from GET /providers/catalog, not a bundle. Grep half of the BDD
// scenario "SPA reads the catalog from the GET, not a bundle" (the network
// half lands with T068-31's Playwright spec). The old SC-009 "static-only"
// guard asserted the opposite invariant and is retired with the bundle.
// =====================================================================

describe('Providers catalog — onboarding reads GET /providers/catalog, never a bundle (FR-037)', () => {
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

  // The catalog-derived subtitle/endpoint DOM assertion (US-7 parity) and the
  // catalog-failure Retry assertion moved up into the picker describes above,
  // where the surface that renders them now lives — same fixture, same
  // catalogDisplay derivation, same pinned strings.
})
