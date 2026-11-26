/*
 * U6-9 (same cause as U6-6): the web-search service lists take the width of
 * their trigger. Tailwind v4 compiles `w-[--x]` to the invalid `width: --x`,
 * which the browser drops, so the popup fell back to the primitive's narrow
 * 18rem default. jsdom has no layout: this checks the width class only; the
 * physical width stays a browser check.
 */

import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, IntegrationProvidersResponse } from '@/lib/api'

vi.mock('@/store/ui', () => ({ useUiStore: vi.fn(() => ({ addToast: vi.fn() })) }))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchIntegrationProviders: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { IntegrationsSection } from './IntegrationsSection'

const response = {
  search: [
    { id: 'tavily', kind: 'search', display_name: 'Tavily', requires_key: true, configured: true, usable: true },
    { id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo', requires_key: false, configured: true, usable: true },
  ],
  voice: [],
  default_search: 'tavily',
  fallback_search: null,
  native_search_in_effect: false,
} as unknown as IntegrationProvidersResponse

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchAppState).mockResolvedValue({ onboarding_complete: true } as AppState)
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(response)
})

describe('U6-9 web-search lists take the trigger width', () => {
  for (const [role, name] of [['default', 'Default search'], ['fallback', 'Fallback']] as const) {
    it(`${name}: the popup width class is a real CSS width`, async () => {
      render(
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <IntegrationsSection />
        </QueryClientProvider>,
      )
      fireEvent.click(within(await screen.findByTestId(`${role}-search-card`)).getByRole('button', { name: 'Change' }))
      fireEvent.click(screen.getByRole('combobox', { name }))
      const list = document.querySelector('[cmdk-list]') as HTMLElement
      const popup = list.closest('[data-radix-popper-content-wrapper] > *') as HTMLElement
      expect(popup.className.split(/\s+/)).toContain('w-[var(--radix-popover-trigger-width)]')
      expect(popup.className).not.toMatch(/w-\[--/)
    })
  }
})
