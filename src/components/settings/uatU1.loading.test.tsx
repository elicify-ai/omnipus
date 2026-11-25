// U1-4; independent oracle: settings-redesign-spec.md::US-9 AS1,
// Loading names the section (all eleven examples), and Reduced motion (FR-075).
// U1-C8: the eleven section rows already passed in the original RED run
// (1 failed /11 passed); retain them as REGRESSION PINS, not proof that the
// reported browser visibility defect was fixed. jsdom establishes mounted
// DOM, data-visible markers and motion-reduce:animate-none class contracts.
// It does not load the Tailwind output here or measure computed animation,
// opacity, rotation, stacking or actual visible pixels. FR-075's no-motion
// outcome and real loading-card visibility still need exact-commit browser UAT.
// HTTP and the motion preference are the only seams. All assertions retained.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor } from '@testing-library/react'
import type { SettingsSectionId } from './settingsSections'
import { heldG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

const rows: { section: SettingsSectionId; label: string; path: string }[] = [
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

// A moving indicator must carry its reduced-motion stop contract, even when
// it appears inside a different loading card (for example Voice input).
function expectMotionStops(container: HTMLElement) {
  const moving = container.querySelectorAll<HTMLElement>('.animate-pulse, .animate-spin, .skeleton-shimmer')
  expect(moving.length, 'instrument: real loading indicators were mounted').toBeGreaterThan(0)
  const unstopped = [...moving].filter((n) => !n.classList.contains('motion-reduce:animate-none'))
  expect(unstopped.map((n) => n.outerHTML), 'U1-4/FR-075: no loading skeleton pulses or spinner rotates under reduced motion').toEqual([])
}

describe('U1-4 — all eleven real sections retain loading cards and reduced-motion contracts', () => {
  it.each(rows)('$label pending read shows its named status, spinner and visible placeholder cards', async ({ section, label, path }) => {
    const t = transportG1()
    const held = heldG1()
    t.on('GET', path, () => held.promise)
    if (section === 'limits') t.on('GET', '/performance', () => held.promise)
    const { container } = sectionG1(section)
    await waitFor(() => expect(t.calls.filter((c) => c.method === 'GET' && c.path === path)).toHaveLength(1))
    const status = await screen.findByText(`Loading ${label}…`)
    expect(status).toHaveAttribute('role', 'status')
    expect(status.querySelector('svg'), `U1-4: ${label} has a real spinner`).not.toBeNull()
    await waitFor(() => expect(container.querySelectorAll('[aria-hidden="true"][data-visible="true"].skeleton-shimmer').length,
      `U1-4/US-9 AS1: ${label} has visible kit placeholder cards, including About`).toBeGreaterThan(0))
    expectMotionStops(container)
    expect(t.writes(), 'reading a loading section must not save anything').toEqual([])
  })

  it('Providers nested Voice input loading cards stop pulsing under reduced motion too', async () => {
    const t = transportG1()
    const held = heldG1()
    t.on('GET', '/integrations/providers', () => held.promise)
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: media === '(prefers-reduced-motion: reduce)', media, onchange: null,
      addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }))
    const { container } = sectionG1('providers')
    await waitFor(() => expect(t.calls.filter((c) => c.path === '/integrations/providers')).toHaveLength(1))
    await screen.findByTestId('provider-row-openrouter')
    expectMotionStops(container)
    expect(t.writes()).toEqual([])
  })
})
