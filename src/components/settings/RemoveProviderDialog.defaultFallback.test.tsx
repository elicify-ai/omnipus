/**
 * Issue #1620 T3 — founder's 2026-11-05 rule, not the old ADR-068 copy:
 * clearing a primary with an available installation default means inheritance,
 * not an agent left without a model. Assert facts, not one exact sentence.
 *
 * Missing production input: ProvidersSection already has `defaultModel`, but
 * RemoveProviderDialogProps and its caller do not pass it. The props spread
 * supplies that generated pair without making this RED a TypeScript error;
 * the real dialog currently ignores it. No component or resolver is mocked.
 */
import { expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { DefaultModel, Provider } from '@/lib/api/generated/openapi-types'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { RemoveProviderDialog, type RemoveProviderDialogProps } from './RemoveProviderDialog'

it('warns that all affected primary agents fall back to the named default provider and selected model', () => {
  const defaultModel: DefaultModel = { provider: 'openrouter', model: 'openai/gpt-5.4' }
  const removed: Provider = {
    id: 'anthropic',
    name: 'anthropic',
    display_name: 'Anthropic',
    status: 'connected',
    auth_method: 'api_key',
    backs_default: false,
    custom: false,
    locality: 'cloud',
    models: ['claude-sonnet-4.6'],
    dependents: [
      { id: 'scout', name: 'Scout', role: 'primary' },
      { id: 'writer', name: 'Writer', role: 'primary' },
      { id: 'archivist', name: 'Archivist', role: 'fallback' },
    ],
  }
  const defaultProvider: Provider = {
    id: 'openrouter',
    name: 'openrouter',
    display_name: 'OpenRouter',
    status: 'connected',
    auth_method: 'api_key',
    backs_default: true,
    dependents: [],
    // The chosen default is NOT the first available model. A provider's
    // model list cannot tell the dialog which installation model was picked.
    models: ['anthropic/claude-3.5-haiku', 'openai/gpt-5.4'],
  }
  const props = {
    open: true,
    onOpenChange: vi.fn(),
    onConfirm: vi.fn(),
    provider: removed,
    displayName: 'Anthropic',
    otherProviders: [defaultProvider],
    catalog: PROVIDERS_CATALOG,
    defaultModel,
  } satisfies RemoveProviderDialogProps & { defaultModel: DefaultModel }

  render(<RemoveProviderDialog {...props} />)

  const dialog = screen.getByRole('alertdialog')
  const primaries = within(dialog).getByTestId('dependent-group-primary')
  expect(within(primaries).getAllByRole('listitem').map((item) => item.textContent).sort()).toEqual([
    'Scout', 'Writer',
  ])
  expect(within(dialog).getByTestId('dependent-group-fallback')).toHaveTextContent('Archivist')
  expect.soft(dialog, 'an available default means agents are not left without a model')
    .not.toHaveTextContent(/left without a model/i)
  expect.soft(dialog, 'the dialog must explain fallback, not just key deletion')
    .toHaveTextContent(/\bfall(?:s|ing)?\s*back\b/i)
  expect.soft(dialog, 'the fallback must be identified as the installation default')
    .toHaveTextContent(/\bdefault\b/i)
  expect.soft(dialog, 'name the destination provider, not the removed provider')
    .toHaveTextContent('OpenRouter')
  expect.soft(dialog, 'name the selected default model, not the first available model')
    .toHaveTextContent('openai/gpt-5.4')
})
