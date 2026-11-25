// T4-6 / U4-3 — FR-075: every rendered animation is inspected, not two named icons.
// Match compiled shipped CSS at reduce. No matchMedia echo and no jsdom frame measurements.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SettingsSectionId } from './settingsSections'
import { heldG1, jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { motionSweepU4, touchSweepU4 } from '../../../tests/helpers/settings-u4-sweep'
import { isStaticU4 } from '../../../tests/helpers/settings-u4-motion'

const SECTIONS: { section: SettingsSectionId; label: string; path: string }[] = [
  { section: 'providers', label: 'Providers', path: '/providers' },
  { section: 'memory', label: 'Memory & recaps', path: '/settings/memory' },
  { section: 'system-agents', label: 'System agents', path: '/agents' },
  { section: 'third-party-agents', label: 'Third-party agents', path: '/settings/third-party-agents' },
  { section: 'limits', label: 'Limits', path: '/settings/context' },
  { section: 'protection', label: 'Protection', path: '/security/sandbox-config' },
  { section: 'sandbox', label: 'Sandbox & network', path: '/security/sandbox-config' },
  { section: 'keys', label: 'Keys & secrets', path: '/credentials' },
  { section: 'gateway', label: 'Gateway', path: '/config' },
  { section: 'data', label: 'Data & retention', path: '/settings/retention' },
  { section: 'about', label: 'About', path: '/about' },
]
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function assertMotion(state: string, requireMoving = false) {
  const rows = await motionSweepU4(state)
  if (requireMoving) expect(rows.filter((row) => !isStaticU4(row.normal)).length,
    `${state}: reach a real normal-motion loading animation, not only static row markers`).toBeGreaterThan(0)
  expect.soft(rows.filter((row) => !row.pass), `${state}/FR-075: every rendered animate-* candidate must stop under reduced motion`).toEqual([])
}

it.each(SECTIONS)('T4-5/6 — discover every control and animation while $label loads, then after it settles', async ({ section, label, path }) => {
  const t = transportG1()
  // FR-065: hold every read that owns this section-wide loading frame.
  // Limits has two independent reads; letting Performance answer immediately
  // would leave only a closed group's pending read, not a visible loading frame.
  const pendingReads = [path, ...(section === 'limits' ? ['/performance'] : [])]
    .map((readPath) => ({ path: readPath, held: heldG1() }))
  for (const read of pendingReads) t.on('GET', read.path, () => read.held.promise)
  const { client } = sectionG1(section)
  try {
    const loading = await screen.findByText(`Loading ${label}…`)
    expect(loading).toHaveAttribute('role', 'status')
    expect(loading, 'FR-065: inspect a visible, still-pending loading frame').toBeVisible()
    await waitFor(() => expect(t.calls.some((call) => call.path === path)).toBe(true))
    // Loading may genuinely have no controls. Still list and assert any that do
    // render; no loaded sweep is excused on the basis of the loading inventory.
    const controls = await touchSweepU4(`loading:${section}`)
    expect.soft(controls.filter((control) => !control.pass), `${section}/FR-070 pending controls`).toEqual([])
    await assertMotion(`loading:${section}`, true)
  } finally {
    for (const read of pendingReads) read.held.resolve(jsonG1(t.defaults[read.path]))
  }
  await waitFor(() => expect(client.isFetching()).toBe(0))
  await assertMotion(`loaded:${section}`)
  expect(t.writes()).toEqual([])
})

const GROUPS: { section: SettingsSectionId; path: string }[] = [
  { section: 'providers', path: '/providers/default-model' },
  { section: 'providers', path: '/integrations/providers' },
  { section: 'protection', path: '/doctor' },
  { section: 'protection', path: '/security/prompt-guard' },
  { section: 'protection', path: '/audit-log' },
  { section: 'sandbox', path: '/security/sandbox-status' },
  { section: 'sandbox', path: '/security/exec-proxy-status' },
]
it.each(GROUPS)('U4-3 / T4-6 — sweep every animated element in $section while $path is pending', async ({ section, path }) => {
  const t = transportG1()
  const pending = heldG1()
  t.on('GET', path, () => pending.promise)
  sectionG1(section)
  try {
    if (path === '/audit-log') await userEvent.click(await screen.findByRole('button', { name: 'View Log' }))
    await waitFor(() => expect(t.calls.some((call) => call.path === path)).toBe(true))
    await assertMotion(`group:${section}:${path}`, true)
  } finally {
    pending.resolve(jsonG1(t.defaults[path]))
  }
})

it('U4-3 / T4-6 — H8 Sandbox status refresh: sweep all animations while the real status read is held', async () => {
  const t = transportG1()
  const { client } = sectionG1('sandbox')
  const refresh = await screen.findByRole('button', { name: 'Refresh sandbox status' })
  await waitFor(() => expect(client.isFetching()).toBe(0))
  expect(refresh).toBeEnabled()
  const pending = heldG1()
  t.on('GET', '/security/sandbox-status', () => pending.promise)
  fireEvent.click(refresh)
  try {
    await waitFor(() => expect(refresh).toBeDisabled())
    expect(refresh.querySelector('svg'), 'H8: real refresh icon mounted, not an empty sweep').not.toBeNull()
    await assertMotion('refresh:sandbox-status', true)
  } finally {
    pending.resolve(jsonG1(t.defaults['/security/sandbox-status']))
  }
})
