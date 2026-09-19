import { describe, it, expect, vi, beforeEach, beforeAll, afterAll } from 'vitest'
import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'

// WP5 (ADR-0010) — LOCAL MODE onboarding: the admin-account steps restored
// from upstream (merge base 184d724773789513a4a7fd404596115ea4ec55cf),
// gated on the app state's `identity.mode` (read defensively — see
// readOnboardingAuthMode in ./onboarding — since that field is landed by a
// parallel lane and is not yet in the generated OpenAPI types). This file
// pins the mode this route means by mocking useRouteContext to return
// onboardingAuthMode: 'local'; -onboarding.test.tsx pins the platform-mode
// flow the same way, explicitly, rather than relying on onboardingAuthMode
// being absent — see that file's own note on its identical mock for why an
// absent mode is no longer platform's default.

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (opts: { component: React.ComponentType }) => opts,
  useNavigate: () => mockNavigate,
  redirect: (opts: unknown) => opts,
  useRouteContext: () => ({ appStateBannerMessage: null, onboardingAuthMode: 'local' }),
}))

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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    configureProvider: vi.fn(),
    probeProvider: vi.fn(),
    completeOnboardingTransaction: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchProvidersCatalog: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

import { probeProvider, completeOnboardingTransaction, fetchProvidersCatalog } from '@/lib/api'
import type { OnboardingCompleteRequest } from '@/lib/api/generated/openapi-types'

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
  localStorage.clear()
  vi.mocked(fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
  vi.mocked(completeOnboardingTransaction).mockResolvedValue({
    username: 'admin',
    token: 'omnipus_0123abcd_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
  } as never)
})

/** Advance from step 1 (username) to step 2 (password). */
async function advanceUsernameToPassword(username = 'admin') {
  fireEvent.change(screen.getByLabelText(/^username$/i), { target: { value: username } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/set your password/i))
}

/** Advance from step 2 (password) to step 3 (personal preferences). */
async function advancePasswordToPersonal(password = 's3cr3tpassword') {
  fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: password } })
  fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: password } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/what should i call you\?/i))
}

/** Advance from step 3 (personal) to step 4 (provider). */
async function advancePersonalToProvider(name = 'Daniel') {
  fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: name } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/select your model provider and default model/i))
}

/** The step-count text appears twice (a visible span and an sr-only twin —
 * see the step indicator in onboarding.tsx), so assert on the progressbar's
 * ARIA attributes instead of screen.getByText. */
function expectProgress(step: number, total: number) {
  const progressbar = screen.getByRole('progressbar')
  expect(progressbar).toHaveAttribute('aria-valuenow', String(step))
  expect(progressbar).toHaveAttribute('aria-valuemax', String(total))
}

describe('OnboardingWizard — local mode: admin-account steps (WP5, ADR-0010)', () => {
  it('renders the username step first, as step 1 of 4', async () => {
    await renderWizard()
    await waitFor(() => screen.getByText(/what should i call you\?/i))
    expect(screen.getByText(/choose a username/i)).toBeInTheDocument()
    expectProgress(1, 4)
  })

  it('Continue is disabled until a username is entered', async () => {
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/^username$/i), { target: { value: 'admin' } })
    expect(screen.getByRole('button', { name: /continue/i })).not.toBeDisabled()
  })

  it('advances to the password step (step 2 of 4) on Continue', async () => {
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    await advanceUsernameToPassword()
    expectProgress(2, 4)
  })

  // Continue is disabled client-side until isValid (length>=8 AND matching —
  // see PasswordStep), so a click on a disabled submit button never fires
  // the form's onSubmit in jsdom (matching real browser behaviour). These
  // two tests pin the disabled gate itself; handleAdminPasswordContinue's
  // own "must be at least 8 characters" / "do not match" checks are the
  // defence-in-depth the doc comment on PasswordStep's onSubmit describes,
  // exercised at the unit level in onboarding-and-profile validation tests
  // rather than through a disabled button here.
  it('rejects a password under 8 characters (Continue stays disabled)', async () => {
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    await advanceUsernameToPassword()
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'short' } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'short' } })
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
    // Still on the password step — no navigation happened.
    expect(screen.getByText(/set your password/i)).toBeInTheDocument()
  })

  it('rejects a mismatched password confirmation (Continue stays disabled)', async () => {
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    await advanceUsernameToPassword()
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 's3cr3tpassword' } })
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'different1' } })
    expect(screen.getByRole('button', { name: /continue/i })).toBeDisabled()
  })

  it('Back on the password step returns to the username step', async () => {
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    await advanceUsernameToPassword()
    fireEvent.click(screen.getByRole('button', { name: /back/i }))
    await waitFor(() => screen.getByText(/what should i call you\?/i))
    expectProgress(1, 4)
  })

  it('advances password → personal preferences (step 3 of 4) on a valid password', async () => {
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    await advanceUsernameToPassword()
    await advancePasswordToPersonal()
    expectProgress(3, 4)
  })

  it('completion sends the admin block (username + password) alongside the provider and preferences', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: true, probed_model: 'claude-sonnet-4-5' })
    await renderWizard()
    await waitFor(() => screen.getByLabelText(/^username$/i))
    await advanceUsernameToPassword('admin')
    await advancePasswordToPersonal('s3cr3tpassword')
    await advancePersonalToProvider('Daniel')

    await waitFor(() => screen.getByTestId('onboarding-provider-picker'))
    fireEvent.click(screen.getByTestId('picker-popular-anthropic'))
    await waitFor(() => screen.getByTestId('provider-detail-panel'))
    fireEvent.change(screen.getByTestId('provider-detail-panel-api-key-input'), {
      target: { value: 'sk-ant-api03-test' },
    })
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    await waitFor(() => screen.getByTestId('onboarding-provider-summary'))

    fireEvent.click(screen.getByTestId('onboarding-model-select'))
    const option = await waitFor(() => screen.getByTestId('onboarding-model-claude-sonnet-4-5'))
    fireEvent.click(option)

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /finish|retry setup/i })).not.toBeDisabled(),
    )
    fireEvent.click(screen.getByRole('button', { name: /finish|retry setup/i }))

    await waitFor(() => expect(completeOnboardingTransaction).toHaveBeenCalled())
    const req = vi.mocked(completeOnboardingTransaction).mock.calls[0]![0] as OnboardingCompleteRequest
    expect(req.admin).toEqual({ username: 'admin', password: 's3cr3tpassword' })
    expect(req.preferences).toEqual({ name: 'Daniel', tone: 'direct', detail: 'brief' })
  })
})
