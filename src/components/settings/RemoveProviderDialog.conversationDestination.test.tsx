/**
 * Issue #1620 T4 — a custom OR local endpoint needs an explicit warning that
 * affected agents' conversations will go to the default provider instead.
 * `custom` and `locality` are the generated Provider contract's markers;
 * custom/cloud and catalog/local exercise the two conditions independently.
 *
 * Regression coverage after the #1620 implementation: `defaultModel` carries
 * the parent's selected pair into the real dialog. As in T3, no component or
 * resolver is mocked; the original semantic RED receipt is preserved.
 */
import { expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { DefaultModel, Provider } from '@/lib/api/generated/openapi-types'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { RemoveProviderDialog, type RemoveProviderDialogProps } from './RemoveProviderDialog'

it.each([
  { label: 'custom cloud endpoint', id: 'workshop', name: 'Workshop', custom: true, locality: 'cloud' as const },
  { label: 'non-custom local endpoint', id: 'ollama', name: 'Ollama', custom: false, locality: 'local' as const },
])('warns that conversations move to the named default when removing a $label', ({ id, name, custom, locality }) => {
  const defaultModel: DefaultModel = { provider: 'openrouter', model: 'openai/gpt-5.4' }
  const removed: Provider = {
    id,
    name,
    display_name: name,
    status: 'connected',
    auth_method: 'api_key',
    backs_default: false,
    custom,
    locality,
    models: ['workspace-model'],
    dependents: [
      { id: 'scout', name: 'Scout', role: 'primary' },
      { id: 'writer', name: 'Writer', role: 'primary' },
    ],
  }
  const defaultProvider: Provider = {
    id: 'openrouter',
    name: 'openrouter',
    display_name: 'OpenRouter',
    status: 'connected',
    auth_method: 'api_key',
    backs_default: true,
    locality: 'cloud',
    dependents: [],
    models: ['anthropic/claude-3.5-haiku', 'openai/gpt-5.4'],
  }
  const props = {
    open: true,
    onOpenChange: vi.fn(),
    onConfirm: vi.fn(),
    provider: removed,
    displayName: name,
    otherProviders: [defaultProvider],
    catalog: PROVIDERS_CATALOG,
    defaultModel,
  } satisfies RemoveProviderDialogProps & { defaultModel: DefaultModel }

  render(<RemoveProviderDialog {...props} />)

  const dialog = screen.getByRole('alertdialog')
  expect(within(dialog).getAllByRole('listitem').map((item) => item.textContent).sort()).toEqual([
    'Scout', 'Writer',
  ])
  expect.soft(dialog, 'custom/local removal must explicitly warn about conversation destination')
    .toHaveTextContent(/\bconversations?\b/i)
  expect.soft(dialog, 'conversations must be described as going to the default provider instead')
    .toHaveTextContent(/\bconversations?\b[\s\S]*(?:\bgo\b|\bsent\b|\brout(?:e|ed)\b|\bswitch(?:ed)?\b)[\s\S]*(?:\bdefault\b|OpenRouter)/i)
  expect.soft(dialog, 'identify the new destination as the default')
    .toHaveTextContent(/\bdefault\b/i)
  expect.soft(dialog, 'identify which provider will receive the conversations')
    .toHaveTextContent('OpenRouter')
  expect.soft(dialog, 'identify the actual selected default model')
    .toHaveTextContent('openai/gpt-5.4')
  expect.soft(dialog, 'do not imply conversations stop when a working default exists')
    .not.toHaveTextContent(/left without a model/i)
})
