// T4-5 / U4-2 — FR-070, US-9 AS5, SC-009. Rendered-control inventory, not geometry.
// jsdom cannot prove 14px-root arithmetic, 44x44 hit boxes or 390px clipping; browser UAT must.
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { parse } from 'postcss'
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SettingsSectionId } from './settingsSections'
import { catalogG1, jsonG1, providersG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { controlsU4, touchSweepU4 } from '../../../tests/helpers/settings-u4-sweep'
import { tokenAxisU3 } from '../../../tests/helpers/settings-u3-contracts'

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
  return rows
}

it.each(SECTIONS)('T4-5 — every visible control in loaded $section (including expanded groups)', async ({ section, path }) => {
  const t = transportG1()
  if (section === 'sandbox') {
    t.on('GET', '/security/sandbox-config', () => jsonG1({
      ...t.defaults['/security/sandbox-config'] as object, workspace_path_guard: true,
    }))
  }
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
  const inventory = await assertSweep(`loaded:${section}`)
  if (section === 'sandbox') {
    const ids = ['sandbox-workspace-limit-on', 'sandbox-workspace-limit-off']
    for (const id of ids) {
      const radio = screen.getByTestId(id) as HTMLInputElement
      expect(radio).toBeVisible()
      expect(radio).toHaveAttribute('type', 'radio')
      expect(radio.labels, 'U4-C4: the real radio must have its clickable label').toHaveLength(1)
      expect(radio.labels![0]).toBeVisible()
    }
    expect(inventory.filter((row) => ids.includes(row.id)).map(({ id, labelTarget }) => ({ id, labelPass: labelTarget?.pass })),
      'U4-C4: both Shell workspace limit labels must actually be swept and have their own touch contract').toEqual(ids.map((id) => ({ id, labelPass: true })))
  }
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

// T4-8 / U4-5: five synthetic companies fit the virtual window, including a
// Popular company and a disabled option. Exact inputs prevent an empty/partial
// virtual list or a Custom-endpoint-only sweep from passing as provider coverage.
const PICKER_COMPANIES = ['Other cloud', 'R40 Alpha', 'R40 Beta', 'R40 Blocked', 'R40 Prime']

function touchMinimumU42() {
  const css = parse(readFileSync(resolve('src/styles/tokens.generated.css'), 'utf8'))
  const values: string[] = []
  css.walkDecls('--target-touch-minimum', (declaration) => { values.push(declaration.value.trim()) })
  expect(values, 'FR-070 / US-9 AS5: read the registered spec token, never a picker constant').toHaveLength(1)
  expect(values[0]).toMatch(/^\d+(?:\.\d+)?px$/)
  return Number.parseFloat(values[0])
}

it.each([true, false])('U4-C3 / FR-070 — Connect row slots respect the touch token on coarse=%s, fine is the increasing-origin control', async (coarse) => {
  const minimum = touchMinimumU42()
  const t = transportG1()
  // Install AFTER transportG1, which otherwise overwrites this browser input.
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: media === '(pointer: coarse)' ? coarse : media === '(pointer: fine)' ? !coarse : false,
    media, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent: () => false,
  }))
  expect(window.matchMedia('(pointer: coarse)').matches, 'instrument: coarse pointer answer reaches the page').toBe(coarse)
  expect(window.matchMedia('(pointer: fine)').matches, 'instrument: fine pointer answer reaches the page').toBe(!coarse)
  const nativeHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')?.get
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
    // Virtual viewport input only; NEVER substitute a row's size/origin.
    return this.dataset.testid === 'picker-virtual-viewport' ? 480 : nativeHeight?.call(this) ?? 0
  })
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: PICKER_COMPANIES.map((company) => ({
    ...catalogG1.providers[0], id: company.toLowerCase().replaceAll(' ', '-'), name: company, company,
    tier: company === 'R40 Prime' ? 'popular' : company === 'R40 Blocked' ? 'unsupported' : 'standard',
    aliases: [], ...(company === 'R40 Blocked' ? { unsupported_reason: 'cloud-iam' } : {}),
  })) }))
  const { client } = sectionG1('providers')
  await userEvent.click(await screen.findByRole('button', { name: 'Connect a provider' }))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const sheet = screen.getByRole('dialog', { name: 'Connect a provider' })
  await userEvent.click(within(sheet).getByTestId('picker-all-toggle'))
  const ids = PICKER_COMPANIES.map((name) => `picker-row-${name}`)
  await waitFor(() => expect(within(sheet).getAllByTestId(/^picker-row-/).map((row) => row.dataset.testid)).toEqual(ids))
  const rows = within(sheet).getAllByTestId(/^picker-row-/).map((row) => ({
    id: row.dataset.testid,
    y: Number.parseFloat(/translateY\(([-\d.]+)px\)/.exec(row.style.transform)?.[1] ?? 'NaN'),
    height: Number.parseFloat(row.style.height),
  }))
  const gaps = rows.slice(1).map((row, i) => row.y - rows[i].y)
  console.info('R42_SLOT ' + JSON.stringify({ coarse, minimum, rows, gaps }))
  expect(rows.filter((row) => !Number.isFinite(row.y) || !Number.isFinite(row.height)), 'instrument: real inline origins and heights').toEqual([])
  expect(gaps.filter((gap) => gap <= 0), 'control: every real row origin increases, including the fine arm').toEqual([])
  expect(rows.filter((row) => row.height <= 0), 'control: every real inline row height is positive').toEqual([])
  if (coarse) {
    expect.soft(gaps.filter((gap) => gap < minimum), 'FR-070: coarse row slots, not only painted minimum heights, must stay the touch minimum apart').toEqual([])
    expect.soft(rows.filter((row) => row.height < minimum), 'FR-070: each coarse row height must reach the registered touch minimum').toEqual([])
  }
  expect(t.writes()).toEqual([])
})
it.each(['expanded', 'search'] as const)('T4-8 / T4-5 — every provider row in the %s Connect list has the coarse touch minimum', async (state) => {
  const t = transportG1()
  const nativeHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')?.get
  // Browser observation seam only: 480px is the scroll viewport, NOT a measured
  // row size or touch oracle. No Picker/virtualizer/selection code is mocked.
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.dataset.testid === 'picker-virtual-viewport' ? 480 : nativeHeight?.call(this) ?? 0
  })
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: PICKER_COMPANIES.map((company) => ({
    ...catalogG1.providers[0], id: company.toLowerCase().replaceAll(' ', '-'), name: company, company,
    tier: company === 'R40 Prime' ? 'popular' : company === 'R40 Blocked' ? 'unsupported' : 'standard',
    aliases: [], ...(company === 'R40 Blocked' ? { unsupported_reason: 'cloud-iam' } : {}),
  })) }))
  const { client } = sectionG1('providers')
  await userEvent.click(await screen.findByRole('button', { name: 'Connect a provider' }))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const sheet = screen.getByRole('dialog', { name: 'Connect a provider' })
  const toggle = within(sheet).getByTestId('picker-all-toggle')
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(within(sheet).queryAllByTestId(/^picker-row-/)).toEqual([])
  if (state === 'expanded') await userEvent.click(toggle)
  else await userEvent.type(within(sheet).getByTestId('picker-search'), 'R40')
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const names = state === 'expanded' ? PICKER_COMPANIES : PICKER_COMPANIES.slice(1)
  const ids = names.map((name) => `picker-row-${name}`)
  await waitFor(() => expect(within(sheet).getAllByTestId(/^picker-row-/).map((row) => row.dataset.testid)).toEqual(ids))
  const providerRows = within(sheet).getAllByTestId(/^picker-row-/)
  for (const row of providerRows) {
    expect(row).toBeVisible()
    expect(row).toHaveAttribute('role', 'option')
  }
  expect(within(sheet).getByTestId('picker-row-R40 Blocked')).toHaveAttribute('aria-disabled', 'true')
  const inventory = await assertSweep(`sheet:Connect-picker:${state}-providers`)
  expect(inventory.filter((row) => row.id.startsWith('picker-row-')).map((row) => row.id),
    'T4-5: the DOM-wide inventory must actually include every expanded/search result').toEqual(ids)
  expect.soft(providerRows.map((row) => ({ id: row.dataset.testid, height: tokenAxisU3(row.classList, 'h') })),
    'T4-8/FR-070: EVERY provider option, including disabled/search rows, needs a pointer-coarse touch-minimum token').toEqual(ids.map((id) => ({ id, height: true })))
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
