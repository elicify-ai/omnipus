/**
 * Issue #1620 T7 / CHECK F3. Oracle: team-lead's Round 3 exact-copy decision
 * and the founder's replacement-before-removal rule. A provider that backs
 * the default cannot leave its primary agents without a model: the operator
 * must pick the replacement first. The dialog and model picker remain real.
 */
import { beforeAll, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { DefaultModel, Provider } from '@/lib/api/generated/openapi-types'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { RemoveProviderDialog } from './RemoveProviderDialog'

// jsdom lacks the measurement/scroll APIs used by the real model picker.
// Do not replace the popover, selector, or their state with test doubles.
beforeAll(() => {
  if (!window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

const CURRENT_DEFAULT: DefaultModel = { provider: 'openrouter', model: 'openai/gpt-5.4' }
const REMOVED: Provider = {
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  status: 'connected',
  auth_method: 'api_key',
  backs_default: true,
  models: ['openai/gpt-5.4'],
  dependents: [
    { id: 'scout', name: 'Scout', role: 'primary' },
    { id: 'writer', name: 'Writer', role: 'primary' },
  ],
}
const GROQ: Provider = {
  id: 'groq',
  name: 'groq',
  display_name: 'Groq',
  status: 'connected',
  auth_method: 'api_key',
  backs_default: false,
  models: ['llama-4-maverick'],
  dependents: [],
}
const ANTHROPIC: Provider = {
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  status: 'connected',
  auth_method: 'api_key',
  backs_default: false,
  models: ['claude-sonnet-4-5'],
  dependents: [],
}

function openDialog() {
  const onConfirm = vi.fn()
  render(<RemoveProviderDialog
    open
    onOpenChange={vi.fn()}
    onConfirm={onConfirm}
    provider={REMOVED}
    displayName="OpenRouter"
    otherProviders={[GROQ, ANTHROPIC]}
    catalog={PROVIDERS_CATALOG}
    defaultModel={CURRENT_DEFAULT}
  />)
  const dialog = screen.getByRole('alertdialog')
  const primary = within(dialog).getByTestId('dependent-group-primary')
  expect(within(primary).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Scout', 'Writer'])
  return { dialog, primary, onConfirm }
}

it('says primary agents will use the new default chosen below before a replacement pair is picked', () => {
  const { dialog, primary, onConfirm } = openDialog()
  expect.soft(primary.querySelector('p')?.textContent,
    'a required replacement is not an agent left without a model')
    .toBe('These agents will use the new default you choose below.')
  expect.soft(primary).not.toHaveTextContent(/left without a model/i)
  expect.soft(primary, 'no replacement pair has been chosen yet').not.toHaveTextContent(/fall back/i)
  expect(within(dialog).getByRole('button', { name: /^Remove$/ })).toBeDisabled()
  expect(onConfirm).not.toHaveBeenCalled()
})

it('names the selected replacement provider and model in the primary heading before allowing removal', async () => {
  const { dialog, primary, onConfirm } = openDialog()
  expect(within(dialog).getByRole('button', { name: /^Remove$/ })).toBeDisabled()

  // Choose a provider other than both the removed provider and the first
  // candidate, then pick its actual model through the real popover.
  fireEvent.click(within(dialog).getByTestId('new-default-provider-anthropic'))
  fireEvent.click(within(dialog).getByTestId('new-default-model-select'))
  fireEvent.click(await screen.findByTestId('new-default-model-claude-sonnet-4-5'))
  await waitFor(() => expect(within(dialog).getByRole('button', { name: /^Remove$/ })).toBeEnabled())

  expect.soft(primary.querySelector('p')?.textContent,
    'the heading must name the operator-selected replacement pair')
    .toBe('These agents will fall back to the default provider Anthropic, using claude-sonnet-4-5.')
  expect.soft(primary).not.toHaveTextContent(/left without a model/i)
  expect.soft(primary, 'the removed provider is not the replacement').not.toHaveTextContent('OpenRouter')
  expect.soft(primary, 'do not guess the first candidate').not.toHaveTextContent('Groq')
  expect.soft(primary).not.toHaveTextContent('openai/gpt-5.4')

  fireEvent.click(within(dialog).getByRole('button', { name: /^Remove$/ }))
  expect(onConfirm).toHaveBeenCalledExactlyOnceWith({ provider: 'anthropic', model: 'claude-sonnet-4-5' })
})
