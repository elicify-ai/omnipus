// U6-4: the Remove provider confirm for the only provider says its sentence
// once (it was both the description and the warning).
import { beforeAll, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RemoveProviderDialog, ONLY_PROVIDER_COPY } from './RemoveProviderDialog'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import type { Provider } from '@/lib/api/generated/openapi-types'

beforeAll(() => {
  if (!window.ResizeObserver) {
    window.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
  }
})

const ONLY = {
  id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter', status: 'connected',
  auth_method: 'api_key', dependents: [], backs_default: true, models: [],
} as unknown as Provider

it('U6-4 — the only-provider confirm shows its sentence once', () => {
  render(<RemoveProviderDialog open onOpenChange={vi.fn()} provider={ONLY} displayName="OpenRouter"
    otherProviders={[]} catalog={PROVIDERS_CATALOG} onConfirm={vi.fn()} />)
  const dialog = screen.getByTestId('remove-provider-dialog')
  expect(dialog.textContent?.split(ONLY_PROVIDER_COPY).length, 'the sentence appears exactly once').toBe(2)
  expect(screen.getByTestId('remove-provider-only-copy')).toHaveTextContent(ONLY_PROVIDER_COPY)
  expect(screen.getByTestId('remove-provider-description')).not.toHaveTextContent(ONLY_PROVIDER_COPY)
  expect(screen.getByTestId('remove-provider-confirm')).toBeDisabled()
})
