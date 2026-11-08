/**
 * ProviderPicker.takenIds.test.tsx — WP-1 (#1700) design R23 (picker half),
 * extra cases added during the mutation round, alongside (never editing) the
 * test author's ProviderPicker.test.tsx.
 *
 * Design §3: the picker passes takenIds built from catalog.providers[].id
 * "when the catalog is loaded; pass nothing when it is loading or failed (the
 * server's M1 is then the backstop)". The pre-written picker test only covers
 * the loaded case; these pin the other two states and that the set really is
 * built from the catalog document handed in, not from a fixed list.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import type { ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import { ProviderPicker } from './ProviderPicker'

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

async function typeCustomId(id: string) {
  fireEvent.click(screen.getByTestId('picker-custom-endpoint'))
  await screen.findByTestId('custom-endpoint-id')
  fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: id } })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-base'), {
    target: { value: 'https://llm.example.com/v1' },
  })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-key'), { target: { value: 'sk-x' } })
}

describe('ProviderPicker — takenIds by catalog state (design R23 / §3)', () => {
  it('passes no takenIds while the catalog is loading', async () => {
    render(
      <ProviderPicker catalog={PROVIDERS_CATALOG} status="loading" onSelect={vi.fn()} autoFocus={false} />,
    )
    await typeCustomId('azure')

    expect(screen.queryByTestId('custom-endpoint-id-taken')).not.toBeInTheDocument()
    expect(screen.getByTestId('custom-endpoint-submit')).not.toBeDisabled()
  })

  it('passes no takenIds when the catalog GET failed, even if a stale document is still held', async () => {
    render(
      <ProviderPicker catalog={PROVIDERS_CATALOG} status="error" onSelect={vi.fn()} autoFocus={false} />,
    )
    await typeCustomId('azure')

    expect(screen.queryByTestId('custom-endpoint-id-taken')).not.toBeInTheDocument()
    expect(screen.getByTestId('custom-endpoint-submit')).not.toBeDisabled()
  })

  it('builds takenIds from the catalog document it is given', async () => {
    const base = PROVIDERS_CATALOG.providers[0]!
    const catalog: ProvidersCatalog = {
      ...PROVIDERS_CATALOG,
      providers: [{ ...base, id: 'only-listed-id' }],
    }
    render(<ProviderPicker catalog={catalog} status="ready" onSelect={vi.fn()} autoFocus={false} />)
    await typeCustomId('only-listed-id')

    expect(screen.getByTestId('custom-endpoint-id-taken')).toHaveTextContent(
      '"only-listed-id" is the name of a listed provider. Choose a different name for your custom endpoint, for example "my-only-listed-id".',
    )
    expect(screen.getByTestId('custom-endpoint-submit')).toBeDisabled()

    // An id the fixture lists but this document does not is free here.
    fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: 'azure' } })
    expect(screen.queryByTestId('custom-endpoint-id-taken')).not.toBeInTheDocument()
  })
})
