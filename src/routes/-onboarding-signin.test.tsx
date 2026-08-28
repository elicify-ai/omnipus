/**
 * -onboarding-signin.test.tsx — ADR-068 §8b sign-in wiring in onboarding
 * step 3 (T068-33; FR-005, FR-045, FR-049, FR-050).
 *
 * Kept out of -onboarding.test.tsx because it needs its own SignInDialog stub:
 * the dialog is fully unit-tested in SignInDialog.test.tsx, and mocking it
 * there would weaken that file's api_key coverage. Here the stub exists only
 * so the WIRING is provable — that the picker's *Sign in* button opens the
 * shared dialog for the right provider, that a completed sign-in re-probes,
 * and that Finish stays gated on a passing `auth: sign_in` probe.
 *
 * The catalog fixture's `openai-chatgpt` row is a real `auth_methods:
 * ["sign_in"]` row (company "ChatGPT"), so nothing synthetic is layered on.
 */

import { describe, it, expect, vi, beforeAll, afterAll, beforeEach } from 'vitest'
import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import type { ProbeProviderRequest } from '@/lib/api/generated/openapi-types'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (opts: { component: React.ComponentType }) => opts,
  useNavigate: () => mockNavigate,
  redirect: (opts: unknown) => opts,
  useRouteContext: () => ({ appStateBannerMessage: null }),
}))

vi.mock('framer-motion', () => ({
  motion: new Proxy(
    {},
    {
      get: (_target: object, prop: string) =>
        React.forwardRef(
          ({ children, ...props }: Record<string, unknown>, ref: unknown) =>
            React.createElement(prop as string, { ...props, ref }, children as React.ReactNode),
        ),
    },
  ),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    probeProvider: vi.fn(),
    completeOnboardingTransaction: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchProvidersCatalog: vi.fn(),
  }
})

// The dialog stub records what it was opened with and can report a completed
// sign-in on demand — everything else about it is SignInDialog.test.tsx's job.
vi.mock('@/components/providers/SignInDialog', () => ({
  SignInDialog: ({
    open,
    providerId,
    providerLabel,
    onSignedIn,
  }: {
    open: boolean
    providerId: string
    providerLabel: string
    onSignedIn?: (status: { state: string; account_label?: string }) => void
  }) =>
    open ? (
      <div data-testid="sign-in-dialog-stub" data-provider={providerId}>
        <span>
          {providerLabel} ({providerId})
        </span>
        <button
          type="button"
          data-testid="stub-complete-sign-in"
          onClick={() => onSignedIn?.({ state: 'signed_in', account_label: 'user@example.com' })}
        >
          simulate signed in
        </button>
      </div>
    ) : null,
}))

import { probeProvider, completeOnboardingTransaction, fetchProvidersCatalog } from '@/lib/api'

const CHATGPT_MODEL = 'gpt-5-codex'

// ── jsdom seams (identical to -onboarding.test.tsx: the virtualiser sizes its
// window from offsetHeight, which jsdom hard-codes to 0). ──────────────────
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

let WizardComponent: React.ComponentType | null = null

beforeAll(async () => {
  const mod = await import('./onboarding')
  WizardComponent = ((mod.Route as unknown) as { component: React.ComponentType }).component
})

async function renderWizard() {
  if (!WizardComponent) throw new Error('WizardComponent not loaded — beforeAll did not run')
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

async function goToStep3() {
  await renderWizard()
  fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/set your password/i))
  fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'password123' } })
  fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'password123' } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/add a model key/i))
  await waitFor(() => screen.getByTestId('onboarding-provider-picker'))
}

/** Open the second-level panel for a company reachable only through search. */
async function openPanelForCompany(company: string, query: string) {
  fireEvent.change(screen.getByTestId('picker-search'), { target: { value: query } })
  await waitFor(() => screen.getByTestId(`picker-row-${company}`))
  fireEvent.click(screen.getByTestId(`picker-row-${company}`))
  await waitFor(() => screen.getByTestId('provider-detail-panel'))
}

async function openPanelForTile(providerId: string) {
  fireEvent.click(screen.getByTestId(`picker-popular-${providerId}`))
  await waitFor(() => screen.getByTestId('provider-detail-panel'))
}

async function pickModel(modelId: string) {
  fireEvent.click(screen.getByTestId('onboarding-model-select'))
  const option = await waitFor(() => screen.getByTestId(`onboarding-model-${modelId}`))
  fireEvent.click(option)
}

function lastProbeRequest(): ProbeProviderRequest {
  const calls = vi.mocked(probeProvider).mock.calls
  return calls[calls.length - 1]![0] as ProbeProviderRequest
}

const finishButton = () => screen.getByRole('button', { name: /finish|retry setup/i })

/** Reach step 3 with the ChatGPT sign-in row confirmed. */
async function confirmChatGptSignInRow() {
  await goToStep3()
  await openPanelForCompany('ChatGPT', 'chatgpt')
  fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
  await waitFor(() => screen.getByTestId('onboarding-provider-summary'))
}

describe('OnboardingWizard — ADR-068 §8b sign-in on step 3', () => {
  it('a sign_in-only company offers Sign in and no API-key field (FR-005)', async () => {
    await goToStep3()
    await openPanelForCompany('ChatGPT', 'chatgpt')

    expect(screen.getByTestId('provider-detail-panel-auth-signin-start')).toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-api-key-input')).not.toBeInTheDocument()
    // FR-006: the helper line, verbatim.
    expect(screen.getByText("Uses your ChatGPT plan's included usage")).toBeInTheDocument()
  })

  it('Sign in in the picker panel opens the shared dialog for that provider (FR-045)', async () => {
    await goToStep3()
    await openPanelForCompany('ChatGPT', 'chatgpt')

    expect(screen.queryByTestId('sign-in-dialog-stub')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('provider-detail-panel-auth-signin-start'))

    const dialog = await waitFor(() => screen.getByTestId('sign-in-dialog-stub'))
    expect(dialog).toHaveAttribute('data-provider', 'openai-chatgpt')
  })

  it('the confirmed sign-in row can reopen the dialog from the summary', async () => {
    await confirmChatGptSignInRow()

    fireEvent.click(screen.getByTestId('onboarding-sign-in-btn'))
    const dialog = await waitFor(() => screen.getByTestId('sign-in-dialog-stub'))
    expect(dialog).toHaveAttribute('data-provider', 'openai-chatgpt')
  })

  it('Finish stays disabled while the gateway reports the session is not signed in', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'not signed in' })
    await confirmChatGptSignInRow()
    await pickModel(CHATGPT_MODEL)

    await waitFor(() => expect(probeProvider).toHaveBeenCalled())
    // The probe is the sign-in gate: no key is ever sent on this path.
    expect(lastProbeRequest()).toEqual({
      id: 'openai-chatgpt',
      auth: 'sign_in',
      model: CHATGPT_MODEL,
    })
    expect(finishButton()).toBeDisabled()
  })

  it('completing the sign-in re-probes the chosen model and then enables Finish', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'not signed in' })
    await confirmChatGptSignInRow()
    await pickModel(CHATGPT_MODEL)
    await waitFor(() => expect(probeProvider).toHaveBeenCalledTimes(1))
    expect(finishButton()).toBeDisabled()

    // The operator signs in; the gateway now accepts the sign_in probe.
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: CHATGPT_MODEL })
    fireEvent.click(screen.getByTestId('onboarding-sign-in-btn'))
    await waitFor(() => screen.getByTestId('sign-in-dialog-stub'))
    fireEvent.click(screen.getByTestId('stub-complete-sign-in'))

    await waitFor(() => expect(probeProvider).toHaveBeenCalledTimes(2))
    expect(lastProbeRequest().auth).toBe('sign_in')
    await waitFor(() => expect(finishButton()).not.toBeDisabled())
  })

  it('Finish submits the sign_in variant with no api_key property at all', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: CHATGPT_MODEL })
    await confirmChatGptSignInRow()
    await pickModel(CHATGPT_MODEL)
    await waitFor(() => expect(finishButton()).not.toBeDisabled())

    fireEvent.click(finishButton())

    await waitFor(() => expect(completeOnboardingTransaction).toHaveBeenCalled())
    const body = vi.mocked(completeOnboardingTransaction).mock.calls[0]![0]
    expect(body.provider).toMatchObject({
      auth_method: 'sign_in',
      id: 'openai-chatgpt',
      model: CHATGPT_MODEL,
    })
    expect('api_key' in body.provider).toBe(false)
  })

  it('an api_key-only company never renders a sign-in control (FR-049, §8b decision 4)', async () => {
    await goToStep3()
    await openPanelForTile('anthropic')

    expect(screen.queryByTestId('provider-detail-panel-auth-signin-start')).not.toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-auth-segment')).not.toBeInTheDocument()
    expect(screen.getByTestId('provider-detail-panel-api-key-input')).toBeInTheDocument()
  })

  it('xAI stays key-only with no sign-in control and no forward-looking copy (FR-049)', async () => {
    await goToStep3()
    await openPanelForTile('xai')

    expect(screen.queryByTestId('provider-detail-panel-auth-signin-start')).not.toBeInTheDocument()
    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument()
    expect(screen.getByTestId('provider-detail-panel-api-key-input')).toBeInTheDocument()
  })
})
