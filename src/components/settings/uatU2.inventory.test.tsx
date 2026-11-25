// U2-6 oracle: D-9 inventory id2 says "Set it in Limits"; US-1 removes Models.
// Real ProvidersSection / DefaultModelCard and generated HTTP response validation.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
const { load } = createRequire(import.meta.url)('js-yaml') as { load(source: string): unknown }
import type { DefaultModel } from '@/lib/api/generated/openapi-types'
import { llmErrorUserMessages } from '@/lib/api/generated/llm-error-messages'
import { catalogG1, jsonG1, providersG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

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

it('T4-7 — the per-model ProviderRow link also says Set it in Limits and retains its exact prefill', async () => {
  const t = transportG1()
  // CatalogModel.window_unknown is a LOCAL-provider projection, not a cloud flag.
  // Keep the existing default provider intact; add a contract-shaped local endpoint.
  t.on('GET', '/providers', () => jsonG1([...providersG1, { ...providersG1[0],
    id: 'u4-local', name: 'U4 local', display_name: 'U4 local', backs_default: false,
  }]))
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: [...catalogG1.providers, {
    ...catalogG1.providers[0], id: 'u4-local', name: 'U4 local', company: 'U4 local', locality: 'local',
    api: 'http://127.0.0.1:3210/v1',
    models: catalogG1.providers[0].models.map((model) => ({ ...model, context_window: 0, window_unknown: true })),
  }] }))
  sectionG1('providers')
  const row = await screen.findByTestId('provider-row-u4-local')
  const expand = row.querySelector<HTMLElement>('[aria-expanded="false"]')
  expect(expand, 'reachability: the real provider model-list disclosure exists').not.toBeNull()
  await userEvent.click(expand!)
  const link = await screen.findByTestId('model-limit-window-unknown-link-u4-local-g1-model-a')
  expect(screen.getByTestId('model-limit-window-u4-local-g1-model-a')).toHaveTextContent(/^No context length$/)
  expect(link).toHaveAccessibleName('Set it in Limits')
  expect(link).toHaveAttribute('href', '/#/settings?tab=models&provider=u4-local&model=g1-model-a')
  expect(t.writes()).toEqual([])
})

it('U4-4 / T4-7 — the context-window error directs a person to Limits, not the removed Models section', () => {
  // US-1 / D-9 id2 / docs/settings.md: destination is independent of the catalogue text.
  expect(llmErrorUserMessages.context_window_unknown).toContain('Settings → Limits → Model overrides → Context length.')
  expect(llmErrorUserMessages.context_window_unknown).not.toContain('Settings → Models')
})

it('U4-4 / T4-7 — the DefaultModel schema describes the same Limits destination', () => {
  // This is a documentation assertion, not a source-text substitute for behaviour.
  const schema = load(readFileSync('contracts/components/schemas/DefaultModel.yaml', 'utf8')) as {
    properties: { window_unknown: { description: string } }
  }
  const description = schema.properties.window_unknown.description.replace(/\s+/g, ' ')
  expect(description).toContain('Settings → Limits → Model overrides')
  expect(description).not.toContain('Settings → Models')
})
