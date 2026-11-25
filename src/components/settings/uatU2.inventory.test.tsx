// U2-6 oracle: D-9 inventory id2 says "Set it in Limits"; US-1 removes Models.
// Real ProvidersSection / DefaultModelCard and generated HTTP response validation.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen } from '@testing-library/react'
import type { DefaultModel } from '@/lib/api/generated/openapi-types'
import { jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('U2-6 — No context length directs the person to Set it in Limits while keeping the same model prefill', async () => {
  const t = transportG1()
  // US-1 No context length scenario supplies this provider/model pair.
  const unknownWindow: DefaultModel = {
    provider: 'openrouter', model: 'acme/m1', context_window: 0, window_unknown: true,
  }
  t.on('GET', '/providers/default-model', () => jsonG1(unknownWindow))
  sectionG1('providers')
  expect(await screen.findByTestId('default-model-window')).toHaveTextContent(/^No context length$/)
  const link = screen.getByTestId('default-model-window-unknown-link')
  expect(link.tagName, 'the guidance remains an actionable link').toBe('A')
  const target = new URL(link.getAttribute('href')!, window.location.href)
  const route = new URL(target.hash.slice(1), window.location.origin)
  expect(route.pathname).toBe('/settings')
  expect(Object.fromEntries(route.searchParams), 'FR-004 / D-3: preserve the existing model-prefill address').toEqual({
    tab: 'models', provider: 'openrouter', model: 'acme/m1',
  })
  expect(link, 'U2-6/D-9 id2: name the actual Limits destination, not the removed Models section').toHaveTextContent(/^Set it in Limits$/)
  expect(t.writes()).toEqual([])
})
