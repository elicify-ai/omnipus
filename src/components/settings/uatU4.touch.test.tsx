// T4-5 / U4-2 — FR-070, US-9 AS5, SC-009. Rendered-control inventory, not geometry.
// jsdom cannot prove 14px-root arithmetic, 44x44 hit boxes or 390px clipping; browser UAT must.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SettingsSectionId } from './settingsSections'
import { catalogG1, jsonG1, providersG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { controlsU4, touchSweepU4 } from '../../../tests/helpers/settings-u4-sweep'

const SECTIONS: { section: SettingsSectionId; path: string }[] = [
  { section: 'providers', path: '/providers' }, { section: 'memory', path: '/settings/memory' },
  { section: 'system-agents', path: '/agents' }, { section: 'third-party-agents', path: '/settings/third-party-agents' },
  { section: 'limits', path: '/settings/context' }, { section: 'protection', path: '/security/sandbox-config' },
  { section: 'sandbox', path: '/security/sandbox-config' }, { section: 'keys', path: '/credentials' },
  { section: 'gateway', path: '/config' }, { section: 'data', path: '/settings/retention' }, { section: 'about', path: '/about' },
]
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function assertSweep(state: string, nonempty = true) {
  const rows = await touchSweepU4(state)
  if (nonempty) expect(rows.length, `${state}: a loaded sweep must reach real controls`).toBeGreaterThan(0)
  expect.soft(rows.filter((row) => !row.pass), `${state}/FR-070: EVERY discovered visible control needs its own coarse-pointer touch contract`).toEqual([])
}

it.each(SECTIONS)('T4-5 — every visible control in loaded $section (including expanded groups)', async ({ section, path }) => {
  const t = transportG1()
  const { client } = sectionG1(section)
  await waitFor(() => expect(t.calls.some((call) => call.path === path)).toBe(true))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  // Re-discover after each expansion so newly mounted nested groups are included.
  // Never open dropdowns/comboboxes or click save/destructive actions.
  const opened = new Set<HTMLElement>()
  let disclosure: HTMLElement | undefined
  while ((disclosure = controlsU4().find((control) => !opened.has(control)
    && control.matches('button[aria-expanded="false"]')
    && control.getAttribute('role') !== 'combobox' && !control.hasAttribute('aria-haspopup')))) {
    opened.add(disclosure)
    await userEvent.click(disclosure)
    await waitFor(() => expect(client.isFetching()).toBe(0))
    await assertSweep(`expanded:${section}:${disclosure.textContent?.trim()}`)
  }
  await waitFor(() => expect(client.isFetching()).toBe(0))
  if (section === 'providers') expect(screen.getByRole('combobox', { name: 'Active transcriber' })).toBeVisible()
  await assertSweep(`loaded:${section}`)
  expect(t.writes()).toEqual([])
})

it('T4-5 — all controls in the configured Providers AWS region sheet (Q4)', async () => {
  const t = transportG1()
  // Synthetic generated-contract-shaped Bedrock fixture. Region strings are inputs, not sizing oracles.
  t.on('GET', '/providers', () => jsonG1([{ ...providersG1[0], id: 'amazon-bedrock', name: 'amazon-bedrock', display_name: 'Amazon Bedrock' }]))
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: [{ ...catalogG1.providers[0],
    id: 'amazon-bedrock', name: 'Amazon Bedrock', company: 'Amazon Bedrock', region: 'us-east-1',
    regions: [{ id: 'us-east-1', group: 'us' }, { id: 'eu-central-1', group: 'eu' }],
  }] }))
  sectionG1('providers')
  await userEvent.click(await screen.findByTestId('configure-btn-amazon-bedrock'))
  expect(await screen.findByTestId('aws-region-input-amazon-bedrock')).toBeVisible()
  await assertSweep('sheet:configured-AWS')
  expect(t.writes()).toEqual([])
})

it('U4-2 / T4-5 — all controls in the actual Connect Custom endpoint form carry the coarse touch contract', async () => {
  const t = transportG1()
  sectionG1('providers')
  await userEvent.click(await screen.findByRole('button', { name: 'Connect a provider' }))
  await assertSweep('sheet:Connect-picker')
  await userEvent.click(screen.getByRole('option', { name: 'Custom endpoint' }))
  expect(await screen.findByTestId('custom-endpoint-protocol')).toBeVisible()
  await assertSweep('sheet:Custom-endpoint')
  expect(t.writes()).toEqual([])
})

it.each(['sibling regions', 'AWS select'] as const)('U4-2 / T4-5 — Connect provider-detail auth, plans and %s', async (kind) => {
  const t = transportG1()
  const base = catalogG1.providers[0]
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: [
    { ...base, id: 'u4-base', name: 'U4 cloud', company: 'U4 cloud', plan: '', region: 'US', auth_methods: ['api_key', 'sign_in'],
      ...(kind === 'AWS select' ? { regions: [{ id: 'us-east-1', group: 'us' }, { id: 'eu-central-1', group: 'eu' }] } : {}) },
    { ...base, id: 'u4-pro', name: 'U4 cloud Pro', company: 'U4 cloud', plan: 'pro', region: 'EU', auth_methods: ['api_key', 'sign_in'],
      ...(kind === 'AWS select' ? { regions: [{ id: 'us-east-1', group: 'us' }, { id: 'eu-central-1', group: 'eu' }] } : {}) },
  ] }))
  sectionG1('providers')
  await userEvent.click(await screen.findByRole('button', { name: 'Connect a provider' }))
  await userEvent.click(await screen.findByRole('button', { name: 'U4 cloud' }))
  expect(await screen.findByRole('group', { name: 'Configure U4 cloud' })).toBeVisible()
  // Both auth modes are real; the sweep lists each rendered control, not just the segment.
  await assertSweep(`sheet:Provider-detail:${kind}:sign-in`)
  await userEvent.click(screen.getByRole('button', { name: /^API key$/ }))
  await assertSweep(`sheet:Provider-detail:${kind}:api-key`)
  expect(t.writes()).toEqual([])
})
