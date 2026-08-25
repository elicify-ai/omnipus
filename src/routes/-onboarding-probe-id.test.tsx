/**
 * -onboarding-probe-id.test.tsx — ADR-067 spec test T46 (task T067-13).
 *
 * US-10 / FR-023: `ProbeProviderRequest.id` is a FREE STRING (1..64, no enum,
 * no pattern), validated at runtime against the served catalog. The SPA half of
 * that contract is two obligations, and this file is where they are pinned:
 *
 *   1. Whatever the operator names their endpoint is what goes on the wire —
 *      verbatim, un-normalised, un-canonicalised. The old bundled catalog +
 *      alias resolver could have rewritten `z-ai` to `zai` before the POST and
 *      produced a probe against a provider the operator never chose; both are
 *      deleted (FR-025, US-11.AC2) and this asserts the absence.
 *
 *   2. When the gateway answers 400 `unknown provider "<id>"` (US-10.AC2), the
 *      wizard RENDERS that refusal, keeps *Finish* disabled, and — the point of
 *      the greenfield rule — never suggests a canonical alternative. FR-015:
 *      the message names the id and nothing else.
 *
 * The custom-endpoint row is the surface where a free-string id is typed, so it
 * is the surface under test. It is also the only path that stays open when the
 * catalog GET fails, which is why the catalog is left healthy here: the id must
 * ride through unchanged even when a catalog IS loaded and could be consulted.
 */

import { describe, it, expect, vi, beforeEach, beforeAll, afterAll } from 'vitest'
import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'

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
        React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: unknown) =>
          React.createElement(prop as string, { ...props, ref }, children as React.ReactNode),
        ),
    },
  ),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
  PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) => {
    if (asChild && React.isValidElement(children)) return children
    return React.createElement('div', null, children)
  },
  PopoverContent: ({ children }: { children: React.ReactNode }) =>
    React.createElement('div', { 'data-testid': 'popover-content' }, children),
}))

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

import { probeProvider, fetchProvidersCatalog } from '@/lib/api'
import type { ProbeProviderRequest } from '@/lib/api/generated/openapi-types'

// jsdom seams the picker needs: cmdk wants ResizeObserver + scrollIntoView, and
// @tanstack/react-virtual sizes its window from offsetHeight, which jsdom
// hard-codes to 0 (a zero-height window renders zero rows).
let restoreOffsetHeight: (() => void) | undefined

beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class {
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
  WizardComponent = (mod.Route as unknown as { component: React.ComponentType }).component
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
  mockNavigate.mockResolvedValue(undefined)
})

async function goToStep3() {
  if (!WizardComponent) throw new Error('WizardComponent not loaded — beforeAll did not run')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <WizardComponent />
    </QueryClientProvider>,
  )
  fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/set your password/i))
  fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'password123' } })
  fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: 'password123' } })
  fireEvent.click(screen.getByRole('button', { name: /continue/i }))
  await waitFor(() => screen.getByText(/add a model key/i))
  await waitFor(() => screen.getByTestId('onboarding-provider-picker'))
}

/** Fill in the custom-endpoint row with a free-string id and confirm it. */
async function submitCustomEndpoint(id: string) {
  fireEvent.click(screen.getByTestId('picker-custom-endpoint'))
  await waitFor(() => screen.getByTestId('custom-endpoint-id'))
  fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: id } })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-base'), {
    target: { value: 'https://proxy.example.com/v1' },
  })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-key'), { target: { value: 'sk-test' } })
  fireEvent.click(screen.getByTestId('custom-endpoint-submit'))
  await waitFor(() => screen.getByTestId('onboarding-provider-summary'))
}

function lastProbeRequest(): ProbeProviderRequest {
  const calls = vi.mocked(probeProvider).mock.calls
  return calls[calls.length - 1]![0] as ProbeProviderRequest
}

const finishButton = () => screen.getByRole('button', { name: /finish|retry setup/i })

describe('onboarding probe — free-string provider id (US-10, FR-023)', () => {
  it('sends a retired id verbatim and renders the gateway’s 400 without a hint', async () => {
    // `z-ai` is carried in the served catalog as an ALIAS of `zai`, so a SPA
    // that still resolved aliases would silently POST `zai` instead. It must
    // not: the gateway owns identity, and its answer for this id is 400.
    const zai = PROVIDERS_CATALOG.providers.find((p) => p.id === 'zai')
    expect(zai?.aliases).toContain('z-ai')

    vi.mocked(probeProvider).mockResolvedValue({
      success: false,
      error: 'unknown provider "z-ai"',
    })

    await goToStep3()
    await submitCustomEndpoint('z-ai')

    fireEvent.change(screen.getByTestId('onboarding-model-select'), { target: { value: 'some-model' } })
    fireEvent.click(screen.getByTestId('onboarding-probe-button'))
    await waitFor(() => expect(probeProvider).toHaveBeenCalled())

    // 1. the id crossed the wire exactly as typed
    const req = lastProbeRequest()
    expect(req.id).toBe('z-ai')
    expect(req.api_base).toBe('https://proxy.example.com/v1')
    expect(req.protocol).toBe('openai-compatible')

    // 2. the refusal is rendered, names the id, and suggests nothing
    const refusal = await waitFor(() => screen.getByText(/unknown provider "z-ai"/i))
    expect(refusal).toBeInTheDocument()
    const step = refusal.closest('form') ?? document.body
    // Positive control on the container: if this ever stopped holding the
    // refusal, the two negative assertions below would pass vacuously.
    expect(step.textContent).toMatch(/unknown provider/i)
    expect(step.textContent).not.toMatch(/did you mean/i)
    // The canonical id must not be offered anywhere on the step. `zai` is a
    // substring of `z-ai`… in the other direction only, so a bare search is
    // safe: any standalone "zai" would be a suggestion.
    expect(step.textContent?.match(/(^|[^-\w])zai([^-\w]|$)/)).toBeNull()

    // 3. a refused probe never enables Finish
    expect(finishButton()).toBeDisabled()
  })

  it('sends an operator-named id verbatim — no normalisation, no case folding', async () => {
    vi.mocked(probeProvider).mockResolvedValue({ success: false, error: 'unknown provider "My-Proxy"' })

    await goToStep3()
    await submitCustomEndpoint('My-Proxy')

    fireEvent.change(screen.getByTestId('onboarding-model-select'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('onboarding-probe-button'))
    await waitFor(() => expect(probeProvider).toHaveBeenCalled())

    expect(lastProbeRequest().id).toBe('My-Proxy')
    expect(finishButton()).toBeDisabled()
  })
})
