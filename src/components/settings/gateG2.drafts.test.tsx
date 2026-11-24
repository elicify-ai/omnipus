// G2-F1, Settings #1519. Oracle: spec FR-010/011/014 and docs/settings.md,
// "If you edit a field again while a Save is still on its way...".
// Real shell, sections, save scope, QueryClient, adapters and validators;
// only HTTP completion is controlled. Existing G1 B→C proofs stay unchanged.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
import { heldG1, integrationsG1, jsonG1, renderG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

// jsdom has no viewport scrolling; this is a browser capability shim, not
// a spy on the Settings shell, draft count or either leave guard.
beforeEach(() => { vi.stubGlobal('scrollTo', () => {}) })
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function chooseDefault(model: string) {
  const row = await screen.findByText('Default model', { selector: 'p' })
  const card = row.closest('[data-settings-row]') as HTMLElement
  await userEvent.click(within(card).getByRole('button', { name: 'Change' }))
  await userEvent.click(screen.getByRole('combobox', { name: /^Default model, currently / }))
  await userEvent.click(await screen.findByRole('option', { name: new RegExp(model) }))
}

async function saveWithRealConsent(passwordProtected = false) {
  const button = screen.queryByRole('button', { name: 'Save changes' })
  expect.soft(button, 'G2-F1: another Save must be available without a successful follow-up GET').not.toBeNull()
  // A missing button is already a failed assertion; avoid a cascading click
  // exception so the actual second serialized request is still asserted.
  if (button) {
    await userEvent.click(button)
    if (passwordProtected) {
      const password = await screen.findByLabelText('Password', { exact: true })
      // Synthetic transport-only password, never a real credential.
      await userEvent.type(password, 'g2-fixture-password')
      await userEvent.click(screen.getByRole('button', { name: /^Confirm$/ }))
    }
  }
}

async function expectProtectedDraft() {
  expect.soft(screen.queryByText(/^1 unsaved change/), 'G2-F1: acknowledged B, not stale A, is the saved baseline').not.toBeNull()
  const unload = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(unload)
  expect.soft(unload.defaultPrevented, 'G2-F1: newer A must keep browser leave protection').toBe(true)
  // The real phone Back action also uses the shell's summed draft count.
  await userEvent.click(screen.getByRole('button', { name: /^Settings$/ }))
  const question = screen.queryByRole('alertdialog', { name: 'Leave without saving 1 change?' })
  expect.soft(question, 'G2-F1: the real shell must receive a positive draft count and block leaving').not.toBeNull()
  if (question) await userEvent.click(within(question).getByRole('button', { name: 'Stay' }))
}

describe('G2-F1 FR-010/011/014 — A → B → A retains a protected draft after acknowledging B', () => {
  it.each(['held', 'failed'] as const)('default-model follow-up GET %s: A remains dirty and can be saved immediately', async (read) => {
    const t = transportG1()
    const putB = heldG1()
    const getB = heldG1()
    const putA = heldG1()
    t.on('PUT', '/providers/default-model', () => t.writes('/providers/default-model').length === 1 ? putB.promise : putA.promise)
    const { client } = renderG1(<SettingsScreen section="providers" requestedTab="providers" />)
    await waitFor(() => expect(client.getQueryData(['default-model'])).toMatchObject({ model: 'g1-model-a' }))
    await chooseDefault('g1-model-b')
    await saveWithRealConsent()
    await waitFor(() => expect(t.writes('/providers/default-model')).toHaveLength(1))
    await chooseDefault('g1-model-a')
    t.on('GET', '/providers/default-model', () => read === 'held' ? getB.promise : jsonG1({ error: 'G2 acknowledged-model read unavailable' }, 500))
    await act(async () => { putB.resolve(jsonG1({ provider: 'openrouter', model: 'g1-model-b', reasoning_effort: 'low' })) })
    await waitFor(() => expect(t.calls.filter((c) => c.method === 'GET' && c.path === '/providers/default-model')).toHaveLength(2))
    if (read === 'failed') await waitFor(() => expect(client.getQueryState(['default-model'])?.fetchStatus).toBe('idle'))
    expect.soft(screen.queryByText('g1-model-a', { selector: 'span.font-mono' }), 'G2-F1: newer A must remain selected').not.toBeNull()
    await expectProtectedDraft()
    await saveWithRealConsent()
    expect.soft(t.writes('/providers/default-model').map((c) => c.body), 'G2-F1: a second Save sends newer A while B GET has not succeeded').toEqual([
      { provider: 'openrouter', model: 'g1-model-b', reasoning_effort: 'low' },
      { provider: 'openrouter', model: 'g1-model-a', reasoning_effort: 'low' },
    ])
  })

  it.each(['held', 'failed'] as const)('transcriber follow-up GET %s: A remains dirty and can be saved immediately', async (read) => {
    const t = transportG1()
    const putB = heldG1()
    const getB = heldG1()
    const putA = heldG1()
    t.on('PUT', '/integrations/providers/groq', () => putB.promise)
    t.on('PUT', '/integrations/providers/elevenlabs', () => putA.promise)
    const { client } = renderG1(<SettingsScreen section="providers" requestedTab="providers" />)
    const input = await screen.findByRole('combobox', { name: 'Active transcriber' })
    expect(input, 'instrument: saved transcriber A is selected before any edit').toHaveValue('elevenlabs')
    await userEvent.selectOptions(input, 'groq')
    await saveWithRealConsent(true)
    await waitFor(() => expect(t.writes('/integrations/providers/groq')).toHaveLength(1))
    await userEvent.selectOptions(input, 'elevenlabs')
    t.on('GET', '/integrations/providers', () => read === 'held' ? getB.promise : jsonG1({ error: 'G2 acknowledged-transcriber read unavailable' }, 500))
    const savedB = { ...integrationsG1, active_voice: 'groq', voice: integrationsG1.voice.map((p) => ({ ...p, active: p.id === 'groq' })) }
    await act(async () => { putB.resolve(jsonG1(savedB)) })
    await waitFor(() => expect(t.calls.filter((c) => c.method === 'GET' && c.path === '/integrations/providers')).toHaveLength(2))
    if (read === 'failed') await waitFor(() => expect(client.getQueryState(['integrations'])?.fetchStatus).toBe('idle'))
    expect.soft(screen.queryByRole('combobox', { name: 'Active transcriber' }), 'G2-F1: newer transcriber A stays selected').toHaveValue('elevenlabs')
    await expectProtectedDraft()
    await saveWithRealConsent(true)
    expect.soft(t.writes().filter((c) => c.path.startsWith('/integrations/providers/')).map((c) => ({ path: c.path, body: c.body })), 'G2-F1: second Save targets A before a successful GET').toEqual([
      { path: '/integrations/providers/groq', body: { kind: 'voice', active: true } },
      { path: '/integrations/providers/elevenlabs', body: { kind: 'voice', active: true } },
    ])
  })
})
