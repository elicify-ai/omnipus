// Settings G1 F10/F11/F12. FR-065/066, UI Screens and States/read dependencies.
// Loaded drafts must survive failed background reads; a failed group cannot
// write while healthy siblings still can. Real QueryClient refetches, not flags.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SettingsSectionId } from './settingsSections'
import { contextG1, heldG1, jsonG1, performanceG1, retentionG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
function expectDirtyGuard(item: string) {
  const event = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(event)
  expect(event.defaultPrevented, `${item}: read failure must not disarm the draft leave guard`).toBe(true)
}

it('F10 FR-066 — failed retention background read preserves draft, write block and leave guard through Retry', async () => {
  const t = transportG1()
  const { client } = sectionG1('data')
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Keep chat history for' }), { target: { value: '30' } })
  expectDirtyGuard('F10 before failure')
  t.on('GET', '/settings/retention', () => jsonG1({ error: 'G1 retention disk unavailable' }, 500))
  await act(async () => { await client.refetchQueries({ queryKey: ['settings', 'retention'] }) })
  await waitFor(() => expect(client.getQueryState(['settings', 'retention'])?.status, 'F10 instrument: the real background read must be in the error state').toBe('error'))
  expectDirtyGuard('F10 during failure')
  // Both a hidden Save and a disabled Save are valid write blocks. If the
  // action remains reachable, drive it and prove no request can escape.
  const blockedSave = screen.queryByRole('button', { name: 'Save changes' })
  if (blockedSave) await userEvent.click(blockedSave)
  expect(t.writes('/settings/retention'), 'F10: failed retention reads must block the actual write path').toEqual([])
  t.on('GET', '/settings/retention', () => jsonG1(retentionG1))
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('spinbutton', { name: 'Keep chat history for' }), 'F10: Retry must not reset the unsaved 30-day draft to saved 90').toHaveValue(30)
  expect(screen.getByText(/1 unsaved change/)).toBeVisible()
  expectDirtyGuard('F10 after Retry')
})

describe('F11 FR-066 — failed Limits read excludes only its draft from Save', () => {
  it.each(['context', 'performance'] as const)('%s failure blocks its write, preserving that draft while saving the healthy sibling', async (failed) => {
    const t = transportG1()
    const { client } = sectionG1('limits')
    await userEvent.click(await screen.findByRole('button', { name: 'Context and tool results' }))
    await userEvent.click(screen.getByRole('button', { name: 'Agents and goals' }))
    const context = await screen.findByRole('textbox', { name: 'MCP tool result cap' })
    const performance = await screen.findByRole('spinbutton', { name: 'Tries per goal' })
    fireEvent.change(context, { target: { value: '22000' } })
    fireEvent.change(performance, { target: { value: '26' } })
    const failedPath = failed === 'context' ? '/settings/context' : '/performance'
    const healthyPath = failed === 'context' ? '/performance' : '/settings/context'
    const storedContext = { ...contextG1 }
    const storedPerformance = { ...performanceG1 }
    t.on('GET', healthyPath, () => jsonG1(failed === 'context' ? storedPerformance : storedContext))
    t.on('GET', failedPath, () => jsonG1({ error: `G1 ${failed} read unavailable` }, 500))
    t.on('PUT', '/performance', ({ body }) => { Object.assign(storedPerformance, body); return jsonG1(storedPerformance) })
    t.on('PUT', '/settings/context', ({ body }) => { Object.assign(storedContext, body); return jsonG1(storedContext) })
    await act(async () => { await client.refetchQueries({ queryKey: [failed === 'context' ? 'context-settings' : 'performance-settings'] }) })
    await waitFor(() => expect(client.getQueryState([failed === 'context' ? 'context-settings' : 'performance-settings'])?.status, 'F11 instrument: the owning real query must have failed').toBe('error'))
    expectDirtyGuard('F11 failed group')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(t.writes(healthyPath)).toHaveLength(1))
    await waitFor(() => expect(client.isFetching()).toBe(0))
    await waitFor(() => expect(screen.queryByText(/^2 unsaved changes$/)).not.toBeInTheDocument())
    expect(t.writes(failedPath), 'F11: count > 0 is not write permission after the owning read failed').toEqual([])
    expect(screen.getByText(/1 unsaved change/), 'F11: only the healthy group becomes saved').toBeVisible()
    expectDirtyGuard('F11 remaining failed draft')
    t.on('GET', failedPath, () => jsonG1(failed === 'context' ? contextG1 : performanceG1))
    await userEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0])
    expect(await screen.findByRole(failed === 'context' ? 'textbox' : 'spinbutton', { name: failed === 'context' ? 'MCP tool result cap' : 'Tries per goal' })).toHaveValue(failed === 'context' ? '22000' : 26)
  })
})

// Failed-read names come from the spec's UI States / Read dependencies table;
// they test what failed, without prescribing a full product-copy sentence.
const READ_ROWS: { section: SettingsSectionId; name: string; path: string; failedRead: RegExp }[] = [
  { section: 'providers', name: 'Providers', path: '/providers', failedRead: /providers/i },
  { section: 'memory', name: 'Memory & recaps', path: '/settings/memory', failedRead: /memory/i },
  { section: 'system-agents', name: 'System agents', path: '/agents', failedRead: /system agents/i },
  { section: 'third-party-agents', name: 'Third-party agents', path: '/settings/third-party-agents', failedRead: /third-party/i },
  { section: 'limits', name: 'Limits', path: '/settings/context', failedRead: /context/i },
  { section: 'protection', name: 'Protection', path: '/security/sandbox-config', failedRead: /sandbox|approvals/i },
  { section: 'sandbox', name: 'Sandbox & network', path: '/security/sandbox-config', failedRead: /sandbox/i },
  { section: 'keys', name: 'Keys & secrets', path: '/credentials', failedRead: /keys|credentials|secrets/i },
  { section: 'gateway', name: 'Gateway', path: '/config', failedRead: /gateway/i },
  { section: 'data', name: 'Data & retention', path: '/settings/retention', failedRead: /retention/i },
  { section: 'about', name: 'About', path: '/about', failedRead: /system info/i },
]

describe('F12 FR-065/066 — every real Settings section has honest loading and recoverable error states', () => {
  it.each(READ_ROWS)('$name loading names the section while its real read is delayed', async ({ section, name, path }) => {
    const t = transportG1()
    const held = heldG1()
    t.on('GET', path, () => held.promise)
    if (section === 'limits') t.on('GET', '/performance', () => held.promise)
    const { container } = sectionG1(section)
    await waitFor(() => expect(t.calls.some((c) => c.path === path)).toBe(true))
    expect(screen.queryByText(`Loading ${name}…`), `F12/FR-065: the delayed ${name} read must show Loading ${name}…`).not.toBeNull()
    expect(container.querySelector('svg.animate-spin'), `F12: ${name} must show the catalogued Spinner glyph`).not.toBeNull()
    // The UI States table calls for Skeleton cards/rows except for About,
    // whose table cell specifies Spinner only. Wait past the kit's reserve.
    if (section !== 'about') {
      await waitFor(() => expect(container.querySelector('[aria-hidden="true"][data-visible="true"].skeleton-shimmer'), `F12: ${name} must show the catalogued Skeleton while its read is pending`).not.toBeNull())
    }
  })

  it.each(READ_ROWS)('$name failed read quotes cause and offers a working Retry', async ({ section, name, path, failedRead }) => {
    const t = transportG1()
    t.on('GET', path, () => jsonG1({ error: `G1 ${name} source unavailable` }, 500))
    if (section === 'limits') t.on('GET', '/performance', () => jsonG1({ error: 'G1 Limits performance source unavailable' }, 500))
    sectionG1(section)
    await waitFor(() => expect(t.calls.some((c) => c.path === path)).toBe(true))
    // Dispatcher adjudication, 2026-11-22: FR-066 uses ApiError.userMessage;
    // a 500 intentionally exposes this safe cause, never the raw server body.
    const cause = 'The server is unavailable. Please try again in a moment.'
    const scope = section === 'limits' ? screen.getByTestId('limits-group-context') : document.body
    let callout!: HTMLElement
    await waitFor(() => {
      const errors = within(scope).queryAllByRole('alert').filter((node) => node.matches('[data-slot="callout"][data-variant="error"]'))
      expect(errors, `F12/FR-066: ${name} needs an actual error Callout for its failed read`).toHaveLength(1)
      callout = errors[0]
    })
    const title = callout.querySelector('[data-slot="callout-title"]')
    expect(title, `F12: ${name} must name the failed read`).not.toBeNull()
    expect(title, `F12: ${name} must identify its failed read, not just show a generic error title`).toHaveTextContent(failedRead)
    const description = callout.querySelector('[data-slot="callout-description"]')
    expect(description, `F12: ${name} must show the agreed safe cause and an actionable Retry instruction`).toHaveTextContent(cause)
    expect(description).toHaveTextContent(/\bretry\b/i)
    expect(within(callout).getAllByRole('button', { name: 'Retry' })).toHaveLength(1)
    const before = t.calls.filter((c) => c.path === path).length
    const healthyReadsBefore = t.calls.filter((c) => c.method === 'GET' && c.path !== path).map((c) => c.path)
    t.on('GET', path, () => jsonG1(t.defaults[path]))
    await userEvent.click(within(callout).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(t.calls.filter((c) => c.path === path).length, `F12: ${name} Retry must actually re-read its failed source`).toBeGreaterThan(before))
    await waitFor(() => expect(callout, `F12: ${name} Retry must remove its now-recovered error`).not.toBeInTheDocument())
    expect(t.calls.filter((c) => c.method === 'GET' && c.path !== path).map((c) => c.path), 'F12/FR-066: Retry must not refetch other read groups').toEqual(healthyReadsBefore)
    expect(t.writes()).toEqual([])
  })
})
