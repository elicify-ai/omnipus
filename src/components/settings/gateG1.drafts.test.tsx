// Settings G1 F1/F2/F4/F5. Spec FR-010/011/014/015/020: drafts belong to
// one section Save, and completion of an older request cannot clear a newer edit.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { updateConfig } from '@/lib/api'
import { catalogG1, heldG1, integrationsG1, jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
const save = () => screen.getByRole('button', { name: 'Save changes' })
function expectLeaveGuard(item: string) {
  const event = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(event)
  expect(event.defaultPrevented, `${item}: a live unsaved draft must keep browser leave protection`).toBe(true)
}
async function chooseDefault(model: string) {
  const row = await screen.findByText('Default model', { selector: 'p' })
  const card = row.closest('[data-settings-row]')!
  await userEvent.click(within(card as HTMLElement).getByRole('button', { name: 'Change' }))
  await userEvent.click(screen.getByRole('combobox', { name: /^Default model, currently / }))
  await userEvent.click(await screen.findByRole('option', { name: new RegExp(model) }))
}

describe('F1 FR-010/011/014 — Memory model slots join the shared Save bar', () => {
  it.each([
    ['primary', 'g1-model-a'], ['fallback', 'g1-model-b'],
  ])('%s model choice stays a protected draft until Save, then sends only that slot', async (slot, previous) => {
    const t = transportG1()
    const held = heldG1()
    t.on('PUT', '/settings/memory', () => held.promise)
    // These slots keep their explicit Off setting on a model that supports
    // an on/off switch, so selecting C completes the model choice rather
    // than stopping in the separate effort-remapping confirmation flow.
    t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: catalogG1.providers.map((p) => ({ ...p, models: p.models.map((m) => ({ ...m, reasoning: { levels: ['low', 'high'], switch: true } })) })) }))
    sectionG1('memory')
    const trigger = await screen.findByRole('combobox', { name: `Model selector, currently ${previous}` })
    await userEvent.click(trigger)
    await userEvent.click(await screen.findByRole('option', { name: /g1-model-c/ }))
    expect(t.writes('/settings/memory'), `F1: choosing the ${slot} model may not auto-save`).toEqual([])
    expect(screen.getByText(/1 unsaved change/)).toBeVisible()
    expect(screen.getByRole('button', { name: 'Discard' })).toBeEnabled()
    expectLeaveGuard('F1')
    await userEvent.click(save())
    await waitFor(() => expect(t.writes('/settings/memory')).toHaveLength(1))
    const expected = slot === 'primary'
      ? { recap_model: 'g1-model-c' }
      : { recap_fallback_models: [{ model: 'g1-model-c', provider: 'openrouter', reasoning_effort: 'off' }] }
    expect(t.writes('/settings/memory')[0].body, 'F1/FR-015: only the chosen slot, no unrelated recap fields').toEqual(expected)
    await act(async () => { held.resolve(jsonG1({ ...(t.defaults['/settings/memory'] as object), ...expected })) })
    expect(await screen.findByText(/Saved at/)).toBeVisible()
  })

  it('ordinary Auto recap edit uses the same Save/Discard and sends no untouched fields', async () => {
    const t = transportG1()
    t.on('PUT', '/settings/memory', ({ body }) => jsonG1({ ...(t.defaults['/settings/memory'] as object), ...(body as object) }))
    sectionG1('memory')
    await userEvent.click(await screen.findByRole('switch', { name: 'Auto recap' }))
    expect(t.writes('/settings/memory')).toEqual([])
    expectLeaveGuard('F1')
    expect(screen.queryByRole('button', { name: 'Discard' }), 'F1: Memory must expose the same Discard as every Settings section').not.toBeNull()
    await userEvent.click(save())
    await waitFor(() => expect(t.writes('/settings/memory').map((c) => c.body)).toEqual([{ auto_recap_enabled: false }]))
  })
})

it('F2 FR-010/C-4 — proxy joins Sandbox drafts and its actual config request carries enable_proxy', async () => {
  const t = transportG1()
  t.on('PUT', '/config', ({ body }) => jsonG1({ ...(t.defaults['/config'] as object), ...(body as object) }))
  sectionG1('sandbox')
  const group = await screen.findByRole('region', { name: 'Network proxy for shell commands' })
  await waitFor(() => expect(within(group).getByRole('switch')).toBeEnabled())
  await userEvent.click(within(group).getByRole('switch'))
  expect(screen.queryByRole('dialog'), 'F2: a proxy draft must not open its old independent restart/write confirmation').not.toBeInTheDocument()
  expect(t.writes('/config')).toEqual([])
  expect(screen.getByText(/1 unsaved change/)).toBeVisible()
  await userEvent.click(save())
  await waitFor(() => expect(t.writes('/config').map((c) => c.body), 'F2: the real frontendToRawConfig must not drop tools.exec.enable_proxy').toEqual([{ tools: { exec: { enable_proxy: true } } }]))
})

it('F2 C-4 — the real proxy config adapter serializes tools.exec.enable_proxy instead of an empty update', async () => {
  const t = transportG1()
  t.on('PUT', '/config', ({ body }) => jsonG1({ ...(t.defaults['/config'] as object), ...(body as object) }))
  await updateConfig({ tools: { exec: { enable_proxy: true } } })
  expect(t.writes('/config').map((call) => call.body), 'F2: the proxy field must survive the actual updateConfig → frontendToRawConfig → fetch path').toEqual([{ tools: { exec: { enable_proxy: true } } }])
})

describe('F4 FR-010/011 — older Providers completion keeps a newer draft', () => {
  it('default model B completion leaves the later model C dirty and selected', async () => {
    const t = transportG1()
    const held = heldG1()
    const savedB = { provider: 'openrouter', model: 'g1-model-b', reasoning_effort: 'low' }
    t.on('PUT', '/providers/default-model', () => held.promise)
    const { client } = sectionG1('providers')
    await chooseDefault('g1-model-b')
    await userEvent.click(save())
    await waitFor(() => expect(t.writes('/providers/default-model')).toHaveLength(1))
    await chooseDefault('g1-model-c')
    t.on('GET', '/providers/default-model', () => jsonG1(savedB))
    await act(async () => { held.resolve(jsonG1(savedB)) })
    await waitFor(() => expect(client.getQueryData(['default-model'])).toMatchObject(savedB))
    expect(screen.queryByText(/1 unsaved change/), 'F4: B acknowledged only its own submitted draft, not the later C edit').not.toBeNull()
    expect(screen.getByText('g1-model-c', { selector: 'span.font-mono' })).toBeVisible()
    expectLeaveGuard('F4 model')
  })

  it('transcriber B completion leaves later transcriber C dirty and selected', async () => {
    const t = transportG1()
    const held = heldG1()
    t.on('PUT', '/integrations/providers/groq', () => held.promise)
    const { client } = sectionG1('providers')
    const input = await screen.findByRole('combobox', { name: 'Active transcriber' })
    await userEvent.selectOptions(input, 'groq')
    await userEvent.click(save())
    await waitFor(() => expect(t.writes('/integrations/providers/groq')).toHaveLength(1))
    await userEvent.selectOptions(input, 'audio-model')
    const savedB = { ...integrationsG1, active_voice: 'groq', voice: integrationsG1.voice.map((p) => ({ ...p, active: p.id === 'groq' })) }
    t.on('GET', '/integrations/providers', () => jsonG1(savedB))
    await act(async () => { held.resolve(jsonG1(savedB)) })
    await waitFor(() => expect(t.calls.filter((c) => c.path === '/integrations/providers' && c.method === 'GET').length).toBeGreaterThanOrEqual(2))
    await waitFor(() => expect(client.getQueryData(['integrations'])).toEqual(savedB))
    expect(screen.getByRole('combobox', { name: 'Active transcriber' }), 'F4: the user chose C while the B write was pending').toHaveValue('audio-model')
    expect(screen.getByText(/1 unsaved change/)).toBeVisible()
    expectLeaveGuard('F4 transcriber')
  })
})

it('F5 FR-020 — failed effort-only default Save cannot be reconciled as saved from an unchanged model pair', async () => {
  const t = transportG1()
  t.on('PUT', '/providers/default-model', () => jsonG1({ error: 'G1 effort write failed before persistence' }, 500))
  sectionG1('providers')
  await userEvent.click(await screen.findByRole('button', { name: /Effort for the default model, g1-model-a: Low/ }))
  await userEvent.click(await screen.findByRole('radio', { name: 'High' }))
  await userEvent.click(save())
  await waitFor(() => expect(t.calls.filter((c) => c.path === '/providers/default-model' && c.method === 'GET').length).toBeGreaterThanOrEqual(2))
  await screen.findByText(/Saved at|may not have been saved|could not be saved|changed somewhere else/)
  expect(t.writes('/providers/default-model').map((c) => c.body)).toEqual([{ provider: 'openrouter', model: 'g1-model-a', reasoning_effort: 'high' }])
  expect(screen.queryByText(/Saved at/), 'F5: the unchanged provider/model does not prove the new effort persisted').not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Effort for the default model, g1-model-a: High/ })).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Discard' }), 'F5: the unsaved effort draft must remain').not.toBeNull()
})
